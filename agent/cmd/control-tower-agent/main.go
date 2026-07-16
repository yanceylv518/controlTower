package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"controltower/agent/internal/channelcollector"
	"controltower/agent/internal/channelcontrol"
	"controltower/agent/internal/config"
	"controltower/agent/internal/dockercollector"
	"controltower/agent/internal/erroralert"
	"controltower/agent/internal/healthcheck"
	"controltower/agent/internal/localbuffer"
	"controltower/agent/internal/logcollector"
	"controltower/agent/internal/metricaggregator"
	"controltower/agent/internal/nginxtiming"
	"controltower/agent/internal/preflight"
	"controltower/agent/internal/reporter"
	"controltower/agent/internal/samples"
	"controltower/agent/internal/state"
	"controltower/agent/internal/syscollector"
)

var agentVersion = "0.1.0"
var activeNginxTiming *nginxtiming.Aggregator

type controlTowerReporter interface {
	Heartbeat(context.Context, reporter.AgentHeartbeatRequest) (reporter.AgentHeartbeatResponse, error)
	Report(context.Context, reporter.AgentReportRequest) error
}

type serverMetricCollector interface {
	Collect(context.Context) reporter.ServerMetricPayload
}

type healthChecker interface {
	Check(context.Context, string) reporter.HealthCheckPayload
}

type channelSnapshotCollector interface {
	Collect(context.Context, int) ([]channelcollector.Snapshot, error)
}
type channelController interface {
	Update(context.Context, channelcontrol.UpdateRequest) (channelcontrol.Result, error)
}
type dockerStatusCollector interface {
	Collect(context.Context) []reporter.DockerStatusPayload
}

func main() {
	log.Printf("control tower agent %s", agentVersion)
	if err := run(); err != nil {
		log.Fatalf("control tower agent failed: %v", err)
	}
}

func run() error {
	configPath := flag.String("config", os.Getenv("CT_AGENT_CONFIG"), "path to control tower agent config file")
	preflightOnly := flag.Bool("preflight", false, "run startup checks and exit")
	flag.Parse()

	cfg, err := config.LoadFromPath(*configPath)
	if err != nil {
		return err
	}
	client := reporter.NewClient(cfg.ServerURL, cfg.AgentToken, time.Duration(cfg.ReportTimeoutSeconds)*time.Second)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	activeNginxTiming = startNginxTiming(ctx, cfg)

	if *preflightOnly {
		passCtx, cancel := context.WithTimeout(ctx, collectPassTimeout(cfg))
		defer cancel()
		result := preflight.Run(passCtx, cfg)
		for _, check := range result.Checks {
			log.Printf("preflight %s %s: %s", check.Status, check.Name, check.Message)
		}
		return result.Err()
	}

	if fakeEventEnabled() {
		passCtx, cancel := context.WithTimeout(ctx, collectPassTimeout(cfg))
		defer cancel()
		return sendFakeReport(passCtx, client, cfg)
	}

	var alertNotifier *erroralert.Notifier
	var nameRefresher *channelNameRefresher
	if cfg.WeComWebhookURL != "" {
		alertNotifier = erroralert.New(cfg.WeComWebhookURL, cfg.InstanceID, cfg.AlertErrorWindow, cfg.AlertErrorThreshold, log.Printf).
			WithWindowMaxAge(time.Duration(cfg.AlertWindowMaxAgeMinutes) * time.Minute).
			WithRemindInterval(time.Duration(cfg.AlertRemindMinutes) * time.Minute).
			WithEventLog(filepath.Join(cfg.DataDir, "alert-events.jsonl"))
		if cfg.AlertNoCacheEnabled {
			alertNotifier.WithNoCacheRule(cfg.AlertNoCacheMinPromptTokens, cfg.AlertNoCacheWindow)
		}
		nameRefresher = newChannelNameRefresher()
	}

	// Standalone alert-only mode: no server configured, so skip heartbeat and
	// reporting entirely and only collect from the source logs table to feed
	// the WeCom error alert.
	if cfg.ServerURL == "" {
		return runCollectorLoop(ctx, cfg, func(passCtx context.Context) error {
			return collectAndAlertOnce(passCtx, cfg, alertNotifier, nameRefresher)
		})
	}

	metricCollector := syscollector.New(cfg.DataDir)
	checker := healthcheck.New(time.Duration(cfg.LogQueryTimeoutSeconds) * time.Second)
	var channelControllerClient channelController
	if cfg.NewAPIControlEnabled {
		channelControllerClient = channelcontrol.NewWithCredentials(cfg.NewAPIAdminAPIURL, cfg.NewAPIAdminAccessToken, cfg.NewAPIAdminUsername, cfg.NewAPIAdminPassword, cfg.NewAPIAdminUserID, channelcontrol.NewFileTokenStore(filepath.Join(cfg.DataDir, "new-api-admin-token")), nil)
	}
	var dockerCollector dockerStatusCollector
	if cfg.DockerEnabled {
		dockerCollector = dockercollector.New()
	}
	return runCollectorLoop(ctx, cfg, func(passCtx context.Context) error {
		return collectAndReportFullPass(passCtx, client, cfg, metricCollector, checker, dockerCollector, channelControllerClient, alertNotifier, nameRefresher)
	})
}

type standaloneCollector interface {
	Collect(ctx context.Context, afterID int64, limit int) ([]logcollector.Event, int64, error)
	Backlog(ctx context.Context, afterID int64) (logcollector.BacklogStats, error)
}

// channelNameRefresher keeps the alert notifier's channel id to name mapping
// fresh. Lookup failures degrade to id-only labels: the channels table needs
// an extra read grant that v1.0 deployments may not have.
type channelNameRefresher struct {
	interval    time.Duration
	lastAttempt time.Time
	warned      bool
}

func newChannelNameRefresher() *channelNameRefresher {
	return &channelNameRefresher{interval: 10 * time.Minute}
}

func (r *channelNameRefresher) maybeRefresh(ctx context.Context, db *sql.DB, notifier *erroralert.Notifier) {
	if r == nil || notifier == nil || db == nil {
		return
	}
	if !r.lastAttempt.IsZero() && time.Since(r.lastAttempt) < r.interval {
		return
	}
	r.lastAttempt = time.Now()
	states, err := channelcollector.FetchStates(ctx, db)
	if err != nil {
		if !r.warned {
			log.Printf("control tower channel state lookup failed; alerts will show channel ids only and disabled channels stay monitored (grant SELECT ON channels to enable): %v", err)
			r.warned = true
		}
		return
	}
	names := make(map[int64]string, len(states))
	disabled := make(map[int64]bool)
	for id, state := range states {
		names[id] = state.Name
		if state.Disabled {
			disabled[id] = true
		}
	}
	notifier.UpdateChannelNames(names)
	notifier.UpdateDisabledChannels(disabled)
}

func collectAndAlertOnce(ctx context.Context, cfg config.Config, notifier *erroralert.Notifier, nameRefresher *channelNameRefresher) error {
	db, err := logcollector.OpenMySQL(cfg.LogDSN)
	if err != nil {
		return err
	}
	defer db.Close()
	nameRefresher.maybeRefresh(ctx, db, notifier)
	stateStore := state.NewFileStore(filepath.Join(cfg.DataDir, "state.json"))
	return runStandalonePass(ctx, cfg, logcollector.NewMySQLCollector(db), notifier, stateStore)
}

func runStandalonePass(ctx context.Context, cfg config.Config, collector standaloneCollector, notifier *erroralert.Notifier, stateStore state.FileStore) error {
	current, err := stateStore.Load()
	if err != nil {
		return err
	}
	// Fresh install: start from the current end of the logs table instead of
	// replaying history, which would fire alerts for long-resolved incidents.
	if current.LastLogID == 0 && current.LastSuccessReportAt.IsZero() {
		stats, err := collector.Backlog(ctx, 0)
		if err != nil {
			return err
		}
		current.LastLogID = stats.SourceLatestLogID
		current.LastSuccessReportAt = time.Now().UTC()
		log.Printf("control tower standalone mode: starting from current log id %d", current.LastLogID)
		return stateStore.Save(current)
	}
	events, lastLogID, err := collector.Collect(ctx, current.LastLogID, cfg.LogBatchSize)
	if err != nil {
		return err
	}
	alertStats := notifier.Process(ctx, events)
	log.Printf("control tower alert pass: after_log_id=%d last_log_id=%d events=%d errors=%d channel_dimensions=%d user_dimensions=%d alerts_triggered=%d alerts_sent=%d alerts_failed=%d",
		current.LastLogID, lastLogID, alertStats.EventCount, alertStats.ErrorCount,
		alertStats.ChannelDimensions, alertStats.UserDimensions, alertStats.AlertsTriggered,
		alertStats.AlertsSent, alertStats.AlertsSendFailures)
	current.LastLogID = lastLogID
	current.LastSuccessReportAt = time.Now().UTC()
	return stateStore.Save(current)
}

func runCollectorLoop(ctx context.Context, cfg config.Config, collect func(context.Context) error) error {
	if cfg.RunOnce {
		passCtx, cancel := context.WithTimeout(ctx, collectPassTimeout(cfg))
		defer cancel()
		return collect(passCtx)
	}

	interval := time.Duration(cfg.LogPollIntervalSeconds) * time.Second
	failures := 0
	for {
		passCtx, cancel := context.WithTimeout(ctx, collectPassTimeout(cfg))
		err := collect(passCtx)
		cancel()
		wait := interval
		if err != nil {
			failures++
			if backoff := reporter.BackoffDelay(failures); backoff > wait {
				wait = backoff
			}
			log.Printf("control tower agent collector pass failed; retrying in %s", wait)
		} else {
			failures = 0
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil
		case <-timer.C:
		}
	}
}

func collectPassTimeout(cfg config.Config) time.Duration {
	return time.Duration(cfg.ReportTimeoutSeconds+cfg.LogQueryTimeoutSeconds) * time.Second
}

func collectAndReportOnce(ctx context.Context, client controlTowerReporter, cfg config.Config, metricCollector serverMetricCollector, checker healthChecker, dockerCollector dockerStatusCollector) error {
	return collectAndReportFullPass(ctx, client, cfg, metricCollector, checker, dockerCollector, nil, nil, nil)
}

func collectAndReportOnceWithController(ctx context.Context, client controlTowerReporter, cfg config.Config, metricCollector serverMetricCollector, checker healthChecker, dockerCollector dockerStatusCollector, controller channelController) error {
	return collectAndReportFullPass(ctx, client, cfg, metricCollector, checker, dockerCollector, controller, nil, nil)
}

func collectAndReportFullPass(ctx context.Context, client controlTowerReporter, cfg config.Config, metricCollector serverMetricCollector, checker healthChecker, dockerCollector dockerStatusCollector, controller channelController, notifier *erroralert.Notifier, nameRefresher *channelNameRefresher) error {
	stateStore := state.NewFileStore(filepath.Join(cfg.DataDir, "state.json"))
	bufferStore := localbuffer.NewFileStore(filepath.Join(cfg.DataDir, "report-buffer.json"))
	current, err := stateStore.Load()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	sequence := now.Unix()

	// The local segment deliberately runs before every Server RPC. A sick or
	// unreachable Control Tower must never stop new-api log collection or the
	// independent local WeCom alert path.
	db, err := logcollector.OpenMySQL(cfg.LogDSN)
	if err != nil {
		return err
	}
	defer db.Close()
	nameRefresher.maybeRefresh(ctx, db, notifier)

	collector := logcollector.NewMySQLCollector(db)
	var channelCollector channelSnapshotCollector
	if cfg.ChannelSnapshotEnabled {
		channelCollector = channelcollector.NewMySQLCollectorWithInterval(db, time.Duration(cfg.ChannelSnapshotIntervalSeconds)*time.Second)
	}
	events, lastLogID, err := collector.Collect(ctx, current.LastLogID, cfg.LogBatchSize)
	if err != nil {
		current.ConsecutiveReportFailures++
		_ = stateStore.Save(current)
		return err
	}

	// Alert evaluation runs right after collection so a report failure below
	// does not delay or drop WeCom notifications.
	alertStats := notifier.Process(ctx, events)
	log.Printf("control tower alert pass: after_log_id=%d last_log_id=%d events=%d errors=%d channel_dimensions=%d user_dimensions=%d alerts_triggered=%d alerts_sent=%d alerts_failed=%d",
		current.LastLogID, lastLogID, alertStats.EventCount, alertStats.ErrorCount,
		alertStats.ChannelDimensions, alertStats.UserDimensions, alertStats.AlertsTriggered,
		alertStats.AlertsSent, alertStats.AlertsSendFailures)

	backlog := logcollector.BacklogStats{}
	if stats, backlogErr := collector.Backlog(ctx, lastLogID); backlogErr != nil {
		log.Printf("control tower backlog telemetry failed: %v", backlogErr)
	} else {
		backlog = stats
	}

	report := buildReport(ctx, cfg, now, sequence+1, lastLogID, backlog, events, metricCollector, checker, dockerCollector, channelCollector)

	// Server segment. ServerLastLogID can only affect the next collection pass
	// now: this intentional one-pass delay is the trade-off that keeps local
	// collection and alerting independent from heartbeat availability.
	flushedLastLogID, flushed, err := flushBufferedReports(ctx, client, bufferStore)
	if err != nil {
		return bufferFailedPass(bufferStore, stateStore, &current, report, lastLogID, now, cfg.MaxLocalBufferEvents, err)
	}
	if flushed && flushedLastLogID > current.LastLogID {
		current.LastLogID = flushedLastLogID
	}
	heartbeat, err := client.Heartbeat(ctx, reporter.AgentHeartbeatRequest{InstanceID: cfg.InstanceID, AgentID: cfg.AgentID, AgentVersion: agentVersion, ReportedAt: now, Sequence: sequence, LastLogID: current.LastLogID})
	if err != nil {
		return bufferFailedPass(bufferStore, stateStore, &current, report, lastLogID, now, cfg.MaxLocalBufferEvents, err)
	}
	report.CommandResults = executeCommands(ctx, controller, heartbeat.Commands)
	if heartbeat.ServerLastLogID > current.LastLogID {
		current.LastLogID = heartbeat.ServerLastLogID
	}
	if reportIsEmpty(report) {
		if lastLogID > current.LastLogID {
			current.LastLogID = lastLogID
		}
		current.LastSuccessReportAt = now
		current.ConsecutiveReportFailures = 0
		return stateStore.Save(current)
	}

	if err := client.Report(ctx, report); err != nil {
		return bufferFailedPass(bufferStore, stateStore, &current, report, lastLogID, now, cfg.MaxLocalBufferEvents, err)
	}
	if activeNginxTiming != nil {
		activeNginxTiming.Ack(len(report.NginxTimingBuckets))
	}

	if lastLogID > current.LastLogID {
		current.LastLogID = lastLogID
	}
	current.LastSuccessReportAt = now
	current.ConsecutiveReportFailures = 0
	return stateStore.Save(current)
}

func bufferFailedPass(bufferStore localbuffer.FileStore, stateStore state.FileStore, current *state.State, report reporter.AgentReportRequest, lastLogID int64, now time.Time, maxEvents int, cause error) error {
	current.ConsecutiveReportFailures++
	if reportShouldAdvanceFromBuffer(report) {
		report.NginxTimingBuckets = nil
		report.NginxSlowSamples = nil
		if err := bufferStore.Append(localbuffer.Entry{CreatedAt: now, LastLogID: lastLogID, Report: report}, maxEvents); err != nil {
			_ = stateStore.Save(*current)
			return err
		}
		if lastLogID > current.LastLogID {
			current.LastLogID = lastLogID
		}
	}
	_ = stateStore.Save(*current)
	return cause
}

func flushBufferedReports(ctx context.Context, client controlTowerReporter, store localbuffer.FileStore) (int64, bool, error) {
	var flushedLastLogID int64
	flushed := false
	for {
		entries, err := store.Load()
		if err != nil {
			return flushedLastLogID, flushed, err
		}
		if len(entries) == 0 {
			return flushedLastLogID, flushed, nil
		}
		entry := entries[0]
		if err := client.Report(ctx, entry.Report); err != nil {
			return flushedLastLogID, flushed, err
		}
		if _, ok, err := store.DropFirst(); err != nil {
			return flushedLastLogID, flushed, err
		} else if !ok {
			return flushedLastLogID, flushed, nil
		}
		if entry.LastLogID > flushedLastLogID {
			flushedLastLogID = entry.LastLogID
		}
		flushed = true
	}
}

func buildReport(ctx context.Context, cfg config.Config, reportedAt time.Time, sequence int64, lastLogID int64, backlog logcollector.BacklogStats, events []logcollector.Event, metricCollector serverMetricCollector, checker healthChecker, dockerCollector dockerStatusCollector, channelCollector channelSnapshotCollector) reporter.AgentReportRequest {
	serverMetrics := make([]reporter.ServerMetricPayload, 0, 1)
	if metricCollector != nil {
		serverMetrics = append(serverMetrics, metricCollector.Collect(ctx))
	}
	healthChecks := make([]reporter.HealthCheckPayload, 0, 1)
	if checker != nil {
		healthChecks = append(healthChecks, checker.Check(ctx, cfg.NewAPIStatusURL))
	}
	dockerStatuses := []reporter.DockerStatusPayload(nil)
	if dockerCollector != nil {
		dockerStatuses = dockerCollector.Collect(ctx)
	}
	channelSnapshots := []reporter.ChannelSnapshotPayload(nil)
	if channelCollector != nil {
		if snapshots, err := channelCollector.Collect(ctx, cfg.ChannelSnapshotLimit); err != nil {
			log.Printf("control tower channel snapshot collection failed: %v", err)
		} else {
			channelSnapshots = toChannelSnapshotPayloads(snapshots)
		}
	}
	report := reporter.AgentReportRequest{
		InstanceID:        cfg.InstanceID,
		AgentID:           cfg.AgentID,
		AgentVersion:      agentVersion,
		ReportedAt:        reportedAt,
		Sequence:          sequence,
		LastLogID:         lastLogID,
		SourceLatestLogID: backlog.SourceLatestLogID,
		BacklogEstimate:   backlog.BacklogEstimate,
		MetricBatchID:     metricBatchID(cfg.AgentID, events),
		LogEvents:         toPayloads(selectLogEventsForReport(cfg, events)),
		LogSamples:        selectLogSamplesForReport(cfg, events),
		AggregatedMetrics: metricaggregator.Aggregate(cfg.InstanceID, events, cfg.CacheHitMinPromptTokens),
		ServerMetrics:     serverMetrics,
		DockerStatuses:    dockerStatuses,
		HealthChecks:      healthChecks,
		ChannelSnapshots:  channelSnapshots,
	}
	if activeNginxTiming != nil {
		buckets, slowSamples := activeNginxTiming.Snapshot()
		for _, bucket := range buckets {
			report.NginxTimingBuckets = append(report.NginxTimingBuckets, reporter.NginxTimingBucketPayload{BucketAt: bucket.BucketAt, RequestCount: bucket.RequestCount, UpstreamCount: bucket.UpstreamCount, Status4xx: bucket.Status4xx, Status5xx: bucket.Status5xx, Status504: bucket.Status504, RTP50: bucket.RTP50, RTP95: bucket.RTP95, RTMax: bucket.RTMax, UHTP50: bucket.UHTP50, UHTP95: bucket.UHTP95, UHTMax: bucket.UHTMax, TransferP50: bucket.TransferP50, TransferP95: bucket.TransferP95, TransferMax: bucket.TransferMax, BytesTotal: bucket.BytesTotal, SlowCount: bucket.SlowCount, SlowTTFTCount: bucket.SlowTTFTCount, SlowTransferCount: bucket.SlowTransferCount})
		}
		for _, sample := range slowSamples {
			report.NginxSlowSamples = append(report.NginxSlowSamples, reporter.NginxSlowSamplePayload{OccurredAt: sample.OccurredAt, Path: sample.Path, Status: sample.Status, RT: sample.RT, UHT: sample.UHT, URT: sample.URT, Bytes: sample.Bytes, RequestID: sample.RequestID})
		}
	}
	return report
}

func startNginxTiming(ctx context.Context, cfg config.Config) *nginxtiming.Aggregator {
	if cfg.NginxAccessLog == "" {
		return nil
	}
	if cfg.ServerURL == "" {
		log.Printf("WARN nginx timing requires Server reporting; standalone mode disabled")
		return nil
	}
	aggregator := nginxtiming.NewAggregator(cfg.NginxSlowRTSeconds)
	go nginxtiming.Tailer{Path: cfg.NginxAccessLog, Aggregator: aggregator}.Run(ctx)
	return aggregator
}

func metricBatchID(agentID string, events []logcollector.Event) string {
	if len(events) == 0 {
		return ""
	}
	return fmt.Sprintf("%s:%d:%d", agentID, events[0].SourceLogID, events[len(events)-1].SourceLogID)
}

func reportShouldAdvanceFromBuffer(report reporter.AgentReportRequest) bool {
	return len(report.LogEvents) > 0 || len(report.LogSamples) > 0 || len(report.AggregatedMetrics) > 0 || len(report.CommandResults) > 0
}

func selectLogEventsForReport(cfg config.Config, events []logcollector.Event) []logcollector.Event {
	if cfg.LogEventMode == "full_debug" {
		return events
	}
	return nil
}

func selectLogSamplesForReport(cfg config.Config, events []logcollector.Event) []reporter.LogSamplePayload {
	if cfg.LogEventMode != "aggregate_with_samples" {
		return nil
	}
	return samples.Select(events, cfg.LogSampleLimit, cfg.SlowLogThresholdSeconds)
}
func reportIsEmpty(report reporter.AgentReportRequest) bool {
	return len(report.LogEvents) == 0 && len(report.AggregatedMetrics) == 0 && len(report.ServerMetrics) == 0 && len(report.DockerStatuses) == 0 && len(report.HealthChecks) == 0 && len(report.ChannelSnapshots) == 0 && len(report.CommandResults) == 0 && len(report.NginxTimingBuckets) == 0 && len(report.NginxSlowSamples) == 0
}

func toPayloads(events []logcollector.Event) []reporter.LogEventPayload {
	payloads := make([]reporter.LogEventPayload, 0, len(events))
	for _, event := range events {
		payloads = append(payloads, reporter.LogEventPayload{
			SourceLogID:       event.SourceLogID,
			CreatedAt:         event.CreatedAt,
			LogType:           event.LogType,
			UserID:            event.UserID,
			Username:          event.Username,
			ChannelID:         event.ChannelID,
			ModelName:         event.ModelName,
			TokenID:           event.TokenID,
			TokenName:         event.TokenName,
			PromptTokens:      event.PromptTokens,
			CompletionTokens:  event.CompletionTokens,
			TotalTokens:       event.TotalTokens,
			Quota:             event.Quota,
			UseTime:           event.UseTime,
			IsStream:          event.IsStream,
			Group:             event.Group,
			RequestID:         event.RequestID,
			UpstreamRequestID: event.UpstreamRequestID,
			ErrorSummary:      event.ErrorSummary,
			CacheTokens:       event.CacheTokens,
			CacheFieldPresent: event.CacheFieldPresent,
		})
	}
	return payloads
}

func toChannelSnapshotPayloads(snapshots []channelcollector.Snapshot) []reporter.ChannelSnapshotPayload {
	payloads := make([]reporter.ChannelSnapshotPayload, 0, len(snapshots))
	for _, snapshot := range snapshots {
		payloads = append(payloads, reporter.ChannelSnapshotPayload{
			ChannelID:   snapshot.ChannelID,
			ChannelName: snapshot.ChannelName,
			Status:      snapshot.Status,
			Weight:      snapshot.Weight,
			ModelsText:  snapshot.ModelsText,
			GroupName:   stringPtr(snapshot.GroupName),
			Priority:    int64Ptr(snapshot.Priority),
			CapturedAt:  snapshot.CapturedAt,
		})
	}
	return payloads
}
func stringPtr(value string) *string { return &value }
func int64Ptr(value int64) *int64    { return &value }
func sendFakeReport(ctx context.Context, client controlTowerReporter, cfg config.Config) error {
	now := time.Now().UTC()
	sequence := now.Unix()
	lastLogID := now.UnixNano()
	if _, err := client.Heartbeat(ctx, reporter.AgentHeartbeatRequest{
		InstanceID:   cfg.InstanceID,
		AgentID:      cfg.AgentID,
		AgentVersion: agentVersion,
		ReportedAt:   now,
		Sequence:     sequence,
		LastLogID:    lastLogID,
	}); err != nil {
		return err
	}
	return client.Report(ctx, reporter.AgentReportRequest{
		InstanceID:   cfg.InstanceID,
		AgentID:      cfg.AgentID,
		AgentVersion: agentVersion,
		ReportedAt:   now,
		Sequence:     sequence + 1,
		LastLogID:    lastLogID,
		LogEvents: []reporter.LogEventPayload{
			{
				SourceLogID:       lastLogID,
				CreatedAt:         now,
				LogType:           "consume",
				UserID:            1001,
				Username:          "local-smoke-user",
				ChannelID:         2001,
				ModelName:         "control-tower-smoke-model",
				TokenID:           3001,
				TokenName:         "local-smoke-token",
				PromptTokens:      12,
				CompletionTokens:  18,
				TotalTokens:       30,
				Quota:             60,
				UseTime:           1.25,
				IsStream:          true,
				Group:             "local-smoke",
				RequestID:         "local-smoke-request",
				UpstreamRequestID: "local-smoke-upstream",
			},
		},
		ServerMetrics: []reporter.ServerMetricPayload{
			{
				CollectedAt:             now,
				CPUPercent:              10.5,
				MemoryUsedPercent:       40.5,
				DiskUsedPercent:         55.5,
				NetworkRxBytesPerSecond: 100,
				NetworkTxBytesPerSecond: 200,
				Load1m:                  0.5,
			},
		},
	})
}

func fakeEventEnabled() bool {
	value := os.Getenv("CT_AGENT_FAKE_EVENT")
	if value == "" {
		return false
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false
	}
	return parsed
}

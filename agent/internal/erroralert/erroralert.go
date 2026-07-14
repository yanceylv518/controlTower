package erroralert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"controltower/agent/internal/logcollector"
)

const (
	DefaultWindow    = 10
	DefaultThreshold = 3
)

// Notifier keeps a sliding window of recent request outcomes per channel and
// per user, collected directly from the source logs table, and pushes a
// WeCom group message when a dimension reaches the error threshold within
// its window. State lives in memory only: a restart starts with empty windows.
type Notifier struct {
	webhookURL     string
	instanceID     string
	window         int
	threshold      int
	client         *http.Client
	now            func() time.Time
	logf           func(format string, args ...any)
	windowMaxAge   time.Duration // 0 = no time decay
	remindInterval time.Duration // 0 = no reminders
	nocacheEnabled bool
	nocacheMin     int64
	nocacheWindow  int
	eventLog       *eventLogger

	mu               sync.Mutex
	states           map[string]*dimensionState
	channelNames     map[int64]string
	disabledChannels map[int64]bool
}

type dimensionState struct {
	title            string
	label            string
	outcomes         []outcome // newest last, capped at window
	lastErrorSummary string
	errorRule        ruleState
	// cache-miss tracking, channel dimensions only: qualifying requests are
	// successful completions whose prompt exceeds the configured size.
	cacheOutcomes []cacheOutcome
	lastNoCache   string
	nocacheRule   ruleState
}

type ruleState struct {
	alerted        bool
	episodeStartAt time.Time
	episodeTotal   int
	lastRemindAt   time.Time
}

type outcome struct {
	isError bool
	at      time.Time
}

type cacheOutcome struct {
	cached bool
	at     time.Time
}

// WithNoCacheRule enables the per-channel cache-miss rule: when the most
// recent `window` successful requests with prompt_tokens > minPromptTokens
// all report no cached tokens, the channel's prompt cache is presumed broken.
func (n *Notifier) WithNoCacheRule(minPromptTokens int64, window int) *Notifier {
	if window <= 0 {
		window = DefaultWindow
	}
	n.nocacheEnabled, n.nocacheMin, n.nocacheWindow = true, minPromptTokens, window
	return n
}

// WithEventLog persists episode transitions as rotating JSON lines.
func (n *Notifier) WithEventLog(path string) *Notifier {
	n.eventLog = newEventLogger(path, n.logf)
	return n
}

// ProcessStats describes one alert evaluation pass without exposing log
// contents or other request-sensitive fields.
type ProcessStats struct {
	EventCount         int
	ErrorCount         int
	ChannelDimensions  int
	UserDimensions     int
	AlertsTriggered    int
	AlertsSent         int
	AlertsSendFailures int
}

func New(webhookURL string, instanceID string, window int, threshold int, logf func(format string, args ...any)) *Notifier {
	if window <= 0 {
		window = DefaultWindow
	}
	if threshold <= 0 {
		threshold = DefaultThreshold
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Notifier{
		webhookURL: webhookURL,
		instanceID: instanceID,
		window:     window,
		threshold:  threshold,
		client:     &http.Client{Timeout: 5 * time.Second},
		now:        time.Now,
		logf:       logf,
		states:     make(map[string]*dimensionState),
	}
}

// WithWindowMaxAge drops window entries older than maxAge during evaluation,
// so stale errors on low-traffic dimensions eventually leave the window and
// the episode re-arms. Zero disables time decay.
func (n *Notifier) WithWindowMaxAge(maxAge time.Duration) *Notifier {
	n.windowMaxAge = maxAge
	return n
}

// WithRemindInterval re-sends a reminder message while an episode keeps
// firing, so a dimension that never recovers is not silent forever after its
// first alert. Zero disables reminders.
func (n *Notifier) WithRemindInterval(interval time.Duration) *Notifier {
	n.remindInterval = interval
	return n
}

// Process feeds newly collected events (ordered oldest first, exactly as the
// log collector returns them) into the windows, then sends one message per
// dimension that is at or above the threshold and has not alerted yet in the
// current episode. A dimension re-arms once its window drops back below the
// threshold. Failed sends re-arm immediately so the next pass retries.
func (n *Notifier) Process(ctx context.Context, events []logcollector.Event) ProcessStats {
	var stats ProcessStats
	if n == nil {
		return stats
	}
	stats.EventCount = len(events)
	channelDimensions := make(map[int64]struct{})
	userDimensions := make(map[int64]struct{})
	n.mu.Lock()
	for _, event := range events {
		if event.LogType == "error" {
			stats.ErrorCount++
		}
		if event.ChannelID > 0 && !n.disabledChannels[event.ChannelID] {
			channelDimensions[event.ChannelID] = struct{}{}
			key := "channel:" + strconv.FormatInt(event.ChannelID, 10)
			n.observeLocked(key, "渠道错误激增", n.channelLabelLocked(event.ChannelID), event, true)
		}
		if event.UserID > 0 {
			userDimensions[event.UserID] = struct{}{}
			label := fmt.Sprintf("客户 %d", event.UserID)
			if event.Username != "" {
				label = fmt.Sprintf("客户 %s(%d)", event.Username, event.UserID)
			}
			key := "user:" + strconv.FormatInt(event.UserID, 10)
			n.observeLocked(key, "客户错误激增", label, event, false)
		}
	}
	pending, records := n.evaluateLocked()
	n.mu.Unlock()
	if n.eventLog != nil {
		n.eventLog.append(records)
	}
	stats.ChannelDimensions = len(channelDimensions)
	stats.UserDimensions = len(userDimensions)
	stats.AlertsTriggered = len(pending)

	for _, message := range pending {
		if err := n.send(ctx, message.content); err != nil {
			stats.AlertsSendFailures++
			n.logf("control tower wecom alert failed: %v", err)
			n.mu.Lock()
			if state, ok := n.states[message.key]; ok {
				rule := message.ruleState(state)
				switch message.kind {
				case "remind":
					rule.lastRemindAt = message.prevRemindAt
				default:
					rule.alerted = false
				}
			}
			n.mu.Unlock()
			continue
		}
		stats.AlertsSent++
	}
	return stats
}

// UpdateChannelNames replaces the channel id to name mapping used in alert
// labels. Existing window labels refresh as new events for the dimension
// arrive.
func (n *Notifier) UpdateChannelNames(names map[int64]string) {
	if n == nil || names == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.channelNames = names
}

// UpdateDisabledChannels replaces the set of disabled channels. Disabled
// channels are excluded from monitoring: their events are ignored and any
// ongoing episode is closed silently, so a channel that was disabled because
// of its errors stops alerting. Re-enabling starts from a fresh window.
func (n *Notifier) UpdateDisabledChannels(disabled map[int64]bool) {
	if n == nil || disabled == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.disabledChannels = disabled
}

func channelIDFromKey(key string) (int64, bool) {
	raw, ok := strings.CutPrefix(key, "channel:")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	return id, err == nil
}

func (n *Notifier) channelLabelLocked(channelID int64) string {
	if name := n.channelNames[channelID]; name != "" {
		return fmt.Sprintf("渠道 %d(%s)", channelID, name)
	}
	return fmt.Sprintf("渠道 %d", channelID)
}

func (n *Notifier) observeLocked(key string, title string, label string, event logcollector.Event, channelDim bool) {
	state := n.states[key]
	if state == nil {
		state = &dimensionState{title: title, label: label}
		n.states[key] = state
	}
	state.label = label
	isError := event.LogType == "error"
	at := event.CreatedAt
	// Unix 0 covers NULL created_at coalesced to 0 by the collector; without
	// this, such an event would enter the window with a 1970 timestamp and be
	// silently dropped by time decay before it could ever count.
	if at.IsZero() || at.Unix() <= 0 {
		at = n.now()
	}
	state.outcomes = append(state.outcomes, outcome{isError: isError, at: at})
	if len(state.outcomes) > n.window {
		state.outcomes = state.outcomes[len(state.outcomes)-n.window:]
	}
	if isError {
		if state.errorRule.alerted {
			state.errorRule.episodeTotal++
		}
		if event.ErrorSummary != "" {
			state.lastErrorSummary = truncate(event.ErrorSummary, 120)
		}
	}
	if channelDim && n.nocacheEnabled && event.LogType == "consume" && event.PromptTokens > n.nocacheMin {
		cached := event.CacheFieldPresent && event.CacheTokens != nil && *event.CacheTokens > 0
		state.cacheOutcomes = append(state.cacheOutcomes, cacheOutcome{cached: cached, at: at})
		if len(state.cacheOutcomes) > n.nocacheWindow {
			state.cacheOutcomes = state.cacheOutcomes[len(state.cacheOutcomes)-n.nocacheWindow:]
		}
		if !cached {
			if state.nocacheRule.alerted {
				state.nocacheRule.episodeTotal++
			}
			if event.ModelName != "" {
				state.lastNoCache = fmt.Sprintf("%s, prompt %d tokens", event.ModelName, event.PromptTokens)
			}
		}
	}
}

type pendingMessage struct {
	key          string
	content      string
	kind         string // "alert" or "remind"
	rule         string
	prevRemindAt time.Time
}

func (m pendingMessage) ruleState(state *dimensionState) *ruleState {
	if m.rule == "nocache" {
		return &state.nocacheRule
	}
	return &state.errorRule
}

func (n *Notifier) evaluateLocked() ([]pendingMessage, []EventRecord) {
	now := n.now()
	var pending []pendingMessage
	var records []EventRecord
	for key, state := range n.states {
		if id, isChannel := channelIDFromKey(key); isChannel && n.disabledChannels[id] {
			if state.errorRule.alerted || state.nocacheRule.alerted {
				n.logf("control tower alert trigger: dimension=%s kind=disposed (channel disabled)", key)
				records = append(records, EventRecord{Time: now, Dimension: key, Label: state.label, Rule: "channel", Kind: "disposed"})
			}
			delete(n.states, key)
			continue
		}
		if n.windowMaxAge > 0 {
			cutoff := now.Add(-n.windowMaxAge)
			kept := state.outcomes[:0]
			for _, item := range state.outcomes {
				if item.at.After(cutoff) {
					kept = append(kept, item)
				}
			}
			state.outcomes = kept
			keptCache := state.cacheOutcomes[:0]
			for _, item := range state.cacheOutcomes {
				if item.at.After(cutoff) {
					keptCache = append(keptCache, item)
				}
			}
			state.cacheOutcomes = keptCache
		}
		errors := countMatches(state.outcomes, func(o outcome) bool { return o.isError })
		p, r := n.evaluateRuleLocked(key, state, "error", &state.errorRule, errors, len(state.outcomes), n.threshold, now)
		pending, records = append(pending, p...), append(records, r...)
		if n.nocacheEnabled && len(state.cacheOutcomes) > 0 {
			misses := 0
			for _, item := range state.cacheOutcomes {
				if !item.cached {
					misses++
				}
			}
			// The rule fires only on a full window of misses: threshold equals
			// the window size, and any cached hit breaks the streak count.
			if len(state.cacheOutcomes) == n.nocacheWindow || state.nocacheRule.alerted {
				p, r = n.evaluateRuleLocked(key, state, "nocache", &state.nocacheRule, misses, len(state.cacheOutcomes), n.nocacheWindow, now)
				pending, records = append(pending, p...), append(records, r...)
			}
		}
		if len(state.outcomes) == 0 && len(state.cacheOutcomes) == 0 && !state.errorRule.alerted && !state.nocacheRule.alerted {
			delete(n.states, key)
		}
	}
	return pending, records
}

func (n *Notifier) evaluateRuleLocked(key string, state *dimensionState, rule string, rs *ruleState, matches, windowCount, threshold int, now time.Time) ([]pendingMessage, []EventRecord) {
	if matches < threshold {
		if rs.alerted {
			record := n.eventRecord(now, key, state.label, rule, "rearm", matches, threshold, rs)
			*rs = ruleState{}
			return nil, []EventRecord{record}
		}
		return nil, nil
	}
	if !rs.alerted {
		rs.alerted, rs.episodeStartAt, rs.episodeTotal, rs.lastRemindAt = true, now, matches, now
		n.logf("control tower alert trigger: dimension=%s rule=%s kind=alert window=%d matches=%d", key, rule, windowCount, matches)
		return []pendingMessage{{key: key, rule: rule, kind: "alert", content: n.alertContent(state, rule, matches, windowCount, now)}}, []EventRecord{n.eventRecord(now, key, state.label, rule, "alert", matches, threshold, rs)}
	}
	if n.remindInterval > 0 && now.Sub(rs.lastRemindAt) >= n.remindInterval {
		prev := rs.lastRemindAt
		rs.lastRemindAt = now
		return []pendingMessage{{key: key, rule: rule, kind: "remind", prevRemindAt: prev, content: n.remindContent(state, rule, matches, windowCount, rs, now)}}, []EventRecord{n.eventRecord(now, key, state.label, rule, "remind", matches, threshold, rs)}
	}
	return nil, nil
}

func countMatches(items []outcome, match func(outcome) bool) int {
	n := 0
	for _, item := range items {
		if match(item) {
			n++
		}
	}
	return n
}

func (n *Notifier) alertContent(state *dimensionState, rule string, matches, windowCount int, now time.Time) string {
	if rule == "nocache" {
		content := fmt.Sprintf("[\u544a\u8b66] 【Control Tower 告警】渠道缓存疑似失效\n实例: %s\n%s 最近 %d 条输入超过 %d tokens 的请求全部未命中缓存",
			n.instanceID, state.label, windowCount, n.nocacheMin)
		if state.lastNoCache != "" {
			content += "\n最新一条: " + state.lastNoCache
		}
		content += "\n时间: " + now.Local().Format("2006-01-02 15:04:05")
		return content
	}
	content := fmt.Sprintf("[\u544a\u8b66] 【Control Tower 告警】%s\n实例: %s\n%s 最近 %d 条请求中 %d 条失败",
		state.title, n.instanceID, state.label, windowCount, matches)
	if state.lastErrorSummary != "" {
		content += "\n最新错误: " + state.lastErrorSummary
	}
	content += "\n时间: " + now.Local().Format("2006-01-02 15:04:05")
	return content
}

func (n *Notifier) remindContent(state *dimensionState, rule string, matches, windowCount int, rs *ruleState, now time.Time) string {
	if rule == "nocache" {
		return fmt.Sprintf("[\u544a\u8b66] 【Control Tower 告警】渠道缓存持续未命中\n实例: %s\n%s 自 %s 起大输入请求持续无缓存，累计 %d 条（输入 > %d tokens）\n时间: %s",
			n.instanceID, state.label, rs.episodeStartAt.Local().Format("01-02 15:04"), rs.episodeTotal, n.nocacheMin, now.Local().Format("2006-01-02 15:04:05"))
	}
	title := strings.TrimSuffix(state.title, "激增") + "持续"
	content := fmt.Sprintf("[\u544a\u8b66] 【Control Tower 告警】%s\n实例: %s\n%s 自 %s 起持续异常，累计 %d 条错误，最近 %d 条请求中 %d 条失败",
		title, n.instanceID, state.label, rs.episodeStartAt.Local().Format("01-02 15:04"), rs.episodeTotal, windowCount, matches)
	if state.lastErrorSummary != "" {
		content += "\n最新错误: " + state.lastErrorSummary
	}
	content += "\n时间: " + now.Local().Format("2006-01-02 15:04:05")
	return content
}

func (n *Notifier) eventRecord(now time.Time, key, label, rule, kind string, matches, threshold int, rs *ruleState) EventRecord {
	return EventRecord{Time: now, Dimension: key, Label: label, Rule: rule, Kind: kind, WindowCount: matches, Threshold: threshold, EpisodeStart: rs.episodeStartAt, EpisodeTotal: rs.episodeTotal}
}

func (n *Notifier) send(ctx context.Context, content string) error {
	payload := map[string]any{"msgtype": "text", "text": map[string]string{"content": content}}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.webhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("wecom webhook returned HTTP %d", resp.StatusCode)
	}
	// WeCom robots answer HTTP 200 even on rejection; the real outcome is
	// in the errcode field of the response body.
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fmt.Errorf("read wecom response: %w", err)
	}
	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil
	}
	if result.ErrCode != 0 {
		return fmt.Errorf("wecom errcode %d: %s", result.ErrCode, result.ErrMsg)
	}
	return nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

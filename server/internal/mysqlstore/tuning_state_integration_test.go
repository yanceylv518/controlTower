package mysqlstore

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"

	"github.com/stretchr/testify/require"
)

func TestContinuousStateRetryFieldsMySQLIntegration(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_MYSQL_TEST_DSN to run MySQL integration test")
	}
	db, err := Open(dsn)
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, ApplyDir(context.Background(), db, "../../migrations"))
	s := New(db)
	now := time.Now().UTC().Truncate(time.Second)
	site := fmt.Sprintf("tuning-retry-test-%d", time.Now().UnixNano())
	_, err = db.Exec(`INSERT INTO instances(id,name,site_id,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,'test','local','',1,?,?)`, site, site, site, now, now)
	require.NoError(t, err)
	defer func() {
		_, err := db.Exec(`DELETE FROM tuning_continuous_states WHERE instance_id=?`, site)
		require.NoError(t, err)
		_, err = db.Exec(`DELETE FROM instances WHERE id=?`, site)
		require.NoError(t, err)
	}()

	for _, tc := range []struct {
		name     string
		failedAt *time.Time
		observed *int64
	}{
		{name: "paused with failure time and observation", failedAt: &now, observed: retryTestWeight(92)},
		{name: "zero is a valid observation", failedAt: &now, observed: retryTestWeight(0)},
		{name: "legacy missing failure time", observed: retryTestWeight(55)},
		{name: "null fields"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := tuning.ContinuousState{InstanceID: site, ChannelID: 199, ModelName: "m", Phase: "normal",
				PausedReason: "write_failed", WriteFailureStreak: 3, LastWriteError: "connection reset by peer",
				LastWriteFailureAt: tc.failedAt, LastObservedWeight: tc.observed, UpdatedAt: now}
			// Simulate successive engine ticks: read the row and persist it again.
			for tick := 0; tick < 3; tick++ {
				require.NoError(t, s.PutContinuousState(state))
				states, err := s.ListContinuousStates(site)
				require.NoError(t, err)
				require.Len(t, states, 1)
				state = states[0]
				require.Equal(t, tc.failedAt, state.LastWriteFailureAt, "tick %d must preserve retry time", tick)
				require.Equal(t, tc.observed, state.LastObservedWeight, "tick %d must preserve observation anchor", tick)
				require.Equal(t, "write_failed", state.PausedReason)
				require.Equal(t, 3, state.WriteFailureStreak)
				require.Equal(t, "connection reset by peer", state.LastWriteError)
				state.UpdatedAt = state.UpdatedAt.Add(30 * time.Second)
			}
		})
	}

	t.Run("engine retries across database reloads", func(t *testing.T) {
		for _, legacy := range []bool{false, true} {
			failedAt := now.Add(-10 * time.Minute)
			state := tuning.ContinuousState{InstanceID: site, ChannelID: 199, ModelName: "m", Phase: "normal", KError: 1,
				PausedReason: "write_failed", WriteFailureStreak: 3, LastWriteError: "previous failure",
				LastWriteFailureAt: &failedAt, UpdatedAt: now}
			if legacy {
				state.LastWriteFailureAt = nil
			}
			require.NoError(t, s.PutContinuousState(state))
			f := &retryIntegrationStore{Store: s, site: site, now: now, writeErr: errors.New("still unavailable")}
			e := tuning.NewEngine(f)
			e.Tick(now)
			require.Equal(t, 1, f.attempts, "legacy=%v", legacy)
			for elapsed := 30 * time.Second; elapsed < 10*time.Minute; elapsed += 30 * time.Second {
				e.Tick(now.Add(elapsed))
			}
			require.Equal(t, 1, f.attempts, "database reload must preserve backoff; legacy=%v", legacy)
			states, err := s.ListContinuousStates(site)
			require.NoError(t, err)
			for _, got := range states {
				if got.ChannelID == 199 {
					require.Equal(t, &now, got.LastWriteFailureAt)
					require.Equal(t, "write_failed", got.PausedReason)
					require.Equal(t, 4, got.WriteFailureStreak)
				}
			}
			f.writeErr = nil
			e.Tick(now.Add(10 * time.Minute))
			require.Equal(t, 2, f.attempts, "due retry must run; legacy=%v", legacy)
			states, err = s.ListContinuousStates(site)
			require.NoError(t, err)
			for _, got := range states {
				if got.ChannelID == 199 {
					require.Empty(t, got.PausedReason)
					require.Zero(t, got.WriteFailureStreak)
					require.Nil(t, got.LastWriteFailureAt)
					require.Empty(t, got.LastWriteError)
					require.Equal(t, retryTestWeight(10), got.LastWrittenWeight)
				}
			}
		}
	})
}

func TestSaveChannelBaseValuesAuditsOnlyChangedRowsMySQLIntegration(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_MYSQL_TEST_DSN to run MySQL integration test")
	}
	db, err := Open(dsn)
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, ApplyDir(context.Background(), db, "../../migrations"))

	site := fmt.Sprintf("tuning-base-audit-%d", time.Now().UnixNano())
	now := time.Now().UTC().Truncate(time.Microsecond)
	defer func() {
		_, cleanupErr := db.Exec(`DELETE FROM operation_audits WHERE instance_id=? AND operation_type='tuning.base_update'`, site)
		require.NoError(t, cleanupErr)
		_, cleanupErr = db.Exec(`DELETE FROM channel_base_values WHERE instance_id=?`, site)
		require.NoError(t, cleanupErr)
	}()

	values := make([]tuning.ChannelBaseValue, 14)
	for i := range values {
		values[i] = tuning.ChannelBaseValue{ChannelID: int64(40000000 + i), ModelName: "model", BaseWeight: int64(100 + i), BasePriority: 5, MaxRPM: 60, MaxTPM: 6000}
		_, err = db.Exec(`INSERT INTO channel_base_values(instance_id,channel_id,model_name,base_weight,base_priority,max_rpm,max_tpm,updated_at,updated_by) VALUES(?,?,?,?,?,?,?,?,?)`, site, values[i].ChannelID, values[i].ModelName, values[i].BaseWeight, values[i].BasePriority, values[i].MaxRPM, values[i].MaxTPM, now, "seed")
		require.NoError(t, err)
	}
	values[6].BaseWeight++
	store := New(db)
	requestID := "request-" + site
	requestMetadata := storage.OperationAudit{
		ActorID: "operator", ActorType: "human", ActorRole: "admin", SourceComponent: "tuning", TriggerType: "manual",
		RequestID: requestID, CorrelationID: requestID, ClientIP: "203.0.113.8", AuthMethod: "session",
		HTTPMethod: http.MethodPut, Route: "PUT /api/dashboard/tuning/base-values",
	}
	require.NoError(t, store.SaveChannelBaseValuesWithAudit(site, "operator", values, now.Add(time.Second), requestMetadata))
	require.NoError(t, store.UpdateOperationAuditHTTPStatus(requestMetadata.RequestID, http.StatusOK))

	var count int
	var targetID, actorID, status string
	err = db.QueryRow(`SELECT COUNT(*),COALESCE(MAX(target_id),''),COALESCE(MAX(actor_id),''),COALESCE(MAX(status),'') FROM operation_audits WHERE instance_id=? AND operation_type='tuning.base_update'`, site).Scan(&count, &targetID, &actorID, &status)
	require.NoError(t, err)
	require.Equal(t, 1, count, "one modified channel must produce one base-value audit")
	require.Equal(t, fmt.Sprint(values[6].ChannelID), targetID)
	require.Equal(t, "operator", actorID)
	require.Equal(t, "succeeded", status)
	var auditRequestID, correlationID, clientIP, authMethod, httpMethod, route string
	var httpStatus int
	err = db.QueryRow(`SELECT request_id,correlation_id,client_ip,auth_method,http_method,route,http_status FROM operation_audits WHERE instance_id=? AND operation_type='tuning.base_update'`, site).Scan(&auditRequestID, &correlationID, &clientIP, &authMethod, &httpMethod, &route, &httpStatus)
	require.NoError(t, err)
	require.Equal(t, requestMetadata.RequestID, auditRequestID)
	require.Equal(t, requestMetadata.CorrelationID, correlationID)
	require.Equal(t, requestMetadata.ClientIP, clientIP)
	require.Equal(t, requestMetadata.AuthMethod, authMethod)
	require.Equal(t, requestMetadata.HTTPMethod, httpMethod)
	require.Equal(t, requestMetadata.Route, route)
	require.Equal(t, http.StatusOK, httpStatus)

	require.NoError(t, store.SaveChannelBaseValues(site, "retry-operator", values, now.Add(2*time.Second)))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM operation_audits WHERE instance_id=? AND operation_type='tuning.base_update'`, site).Scan(&count))
	require.Equal(t, 1, count, "repeating the saved values must not add another audit")

	var updatedBy, updatedAt string
	require.NoError(t, db.QueryRow(`SELECT updated_by,DATE_FORMAT(updated_at,'%Y-%m-%d %H:%i:%s.%f') FROM channel_base_values WHERE instance_id=? AND channel_id=?`, site, values[6].ChannelID).Scan(&updatedBy, &updatedAt))
	require.Equal(t, "operator", updatedBy, "a no-op retry must not rewrite the editor")
	require.Equal(t, now.Add(time.Second).Format("2006-01-02 15:04:05.000000"), updatedAt, "a no-op retry must not rewrite the update time")
}

func retryTestWeight(v int64) *int64 { return &v }

// Keep state persistence real while supplying deterministic metrics and control
// responses. No external new-api service is contacted by this integration test.
type retryIntegrationStore struct {
	Store
	site     string
	now      time.Time
	writeErr error
	attempts int
}

func (s *retryIntegrationStore) ListEnabledSites() ([]string, error) {
	return []string{s.site}, nil
}

func (s *retryIntegrationStore) GetPolicy(string) (tuning.PolicyRecord, bool, error) {
	p := tuning.DefaultPolicy()
	p.DispatchModes = map[string]string{"m": "auto"}
	return tuning.PolicyRecord{InstanceID: s.site, Policy: p, Mode: "auto", UpdatedAt: s.now}, true, nil
}

func (s *retryIntegrationStore) ListChannelBaseValues(string, string) ([]tuning.ChannelBaseValue, error) {
	return []tuning.ChannelBaseValue{
		{ChannelID: 199, ModelName: "m", Models: []string{"m"}, BaseWeight: 10, CurrentWeight: 20, SnapshotAt: s.now.Add(-time.Hour)},
		{ChannelID: 999, ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 100},
	}, nil
}

func (s *retryIntegrationStore) QueryMetrics(string, time.Time, time.Time) ([]tuning.ChannelMetric, error) {
	var metrics []tuning.ChannelMetric
	for _, id := range []int64{199, 999} {
		metrics = append(metrics, tuning.ChannelMetric{ChannelID: id, RequestCount: 100, TTFTP50: 1, TTFTP90: 1, TTFTP95: 1,
			SpeedSamples: 100, SpeedTTFTP50: 1, SpeedTTFTP90: 1, SpeedTTFTP95: 1,
			OTPS: 100, OTPSSamples: 100, OTPSSampleTokens: 1000, OTPSStatsVersion: 1})
	}
	return metrics, nil
}

func (s *retryIntegrationStore) CreateContinuousWeightChange(tuning.Recommendation, string, time.Time) (string, error) {
	s.attempts++
	return "test-command", s.writeErr
}

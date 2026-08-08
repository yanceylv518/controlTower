package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/billing"
)

type fakeBillingChannelReadStore struct {
	latest       billing.Job
	jobs         map[string]billing.Job
	rowsByJob    map[string][]billing.AggregateRow
	queriedJobID string
}

func (f *fakeBillingChannelReadStore) QueryBillingChannelAggregates(context.Context, string, time.Time, time.Time, int64) ([]billing.AggregateRow, error) {
	return nil, nil
}
func (f *fakeBillingChannelReadStore) QueryBillingChannelAggregatesForJob(_ context.Context, jobID string, _ int64) ([]billing.AggregateRow, error) {
	f.queriedJobID = jobID
	return f.rowsByJob[jobID], nil
}
func (f *fakeBillingChannelReadStore) ListBillingChannelSettings(context.Context, string) (map[int64]billing.ChannelSetting, error) {
	return map[int64]billing.ChannelSetting{}, nil
}
func (f *fakeBillingChannelReadStore) PutBillingChannelSetting(context.Context, billing.ChannelSetting) error {
	return nil
}
func (f *fakeBillingChannelReadStore) ListBillingPrices(context.Context, string) ([]billing.PriceRecord, error) {
	return nil, nil
}
func (f *fakeBillingChannelReadStore) ListBillingGroupRatios(context.Context, string) ([]billing.GroupRatio, error) {
	return nil, nil
}
func (f *fakeBillingChannelReadStore) BillingRatioSnapshots(context.Context, string, time.Time, time.Time) (map[string]string, error) {
	return map[string]string{}, nil
}
func (f *fakeBillingChannelReadStore) LatestBillingJob(context.Context, string, string, time.Time, time.Time) (billing.Job, error) {
	if f.latest.ID == "" {
		return billing.Job{}, sql.ErrNoRows
	}
	return f.latest, nil
}
func (f *fakeBillingChannelReadStore) BillingJob(_ context.Context, jobID string) (billing.Job, error) {
	job, ok := f.jobs[jobID]
	if !ok {
		return billing.Job{}, sql.ErrNoRows
	}
	return job, nil
}

func channelReadJob(id, status string, from, to time.Time) billing.Job {
	return billing.Job{ID: id, InstanceID: "site-a", JobType: "channel_generate", From: from, To: to, Status: status}
}

func runChannelRead(t *testing.T, store *fakeBillingChannelReadStore, jobID, format string) *httptest.ResponseRecorder {
	t.Helper()
	query := "/api/dashboard/billing/channels?instance_id=site-a&channel_id=7&from=2026-07-01+00%3A00%3A00&to=2026-08-01+00%3A00%3A00"
	if jobID != "" {
		query += "&job_id=" + jobID
	}
	if format != "" {
		query += "&format=" + format
	}
	recorder := httptest.NewRecorder()
	BillingChannelsHandler{Store: store, Source: fakeBillingLiveSource{}}.ServeHTTP(recorder, httptest.NewRequest("GET", query, nil))
	return recorder
}

func TestBillingChannelsRejectsMismatchedJobID(t *testing.T) {
	from, _ := time.ParseInLocation("2006-01-02 15:04:05", "2026-07-01 00:00:00", billing.BusinessLocation)
	to := from.AddDate(0, 1, 0)
	wrong := channelReadJob("wrong", "complete", from, to)
	wrong.InstanceID = "site-b"
	store := &fakeBillingChannelReadStore{jobs: map[string]billing.Job{"wrong": wrong}}
	recorder := runChannelRead(t, store, "wrong", "")
	if recorder.Code != 409 || !strings.Contains(recorder.Body.String(), `"error":"billing_not_generated"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestBillingChannelsReportsGeneratingProgress(t *testing.T) {
	from, _ := time.ParseInLocation("2006-01-02 15:04:05", "2026-07-01 00:00:00", billing.BusinessLocation)
	to := from.AddDate(0, 1, 0)
	running := channelReadJob("running", "running", from, to)
	running.TotalSteps, running.CompletedSteps = 20, 5
	store := &fakeBillingChannelReadStore{jobs: map[string]billing.Job{"running": running}}
	recorder := runChannelRead(t, store, "running", "")
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != 409 || body["error"] != "billing_generating" || body["progress"] != float64(25) || body["job_id"] != "running" {
		t.Fatalf("status=%d body=%v", recorder.Code, body)
	}
}

func TestBillingChannelsCSVUsesRequestedJobVersion(t *testing.T) {
	from, _ := time.ParseInLocation("2006-01-02 15:04:05", "2026-07-01 00:00:00", billing.BusinessLocation)
	to := from.AddDate(0, 1, 0)
	requested := channelReadJob("requested", "complete", from, to)
	latest := channelReadJob("latest", "complete", from, to)
	store := &fakeBillingChannelReadStore{
		latest: latest,
		jobs:   map[string]billing.Job{"requested": requested, "latest": latest},
		rowsByJob: map[string][]billing.AggregateRow{
			"requested": {{UserID: 7, Username: "channel-7", Day: from, ModelName: "model-a", GroupName: "default", RequestCount: 37}},
			"latest":    {{UserID: 7, Username: "channel-7", Day: from, ModelName: "model-a", GroupName: "default", RequestCount: 99}},
		},
	}
	recorder := runChannelRead(t, store, "requested", "csv")
	if recorder.Code != 200 {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.queriedJobID != "requested" || !strings.Contains(recorder.Body.String(), ",37,") || strings.Contains(recorder.Body.String(), ",99,") {
		t.Fatalf("queried job=%q csv=%s", store.queriedJobID, recorder.Body.String())
	}
}

func TestBillingChannelsUsesCurrentNewAPICurrency(t *testing.T) {
	from, _ := time.ParseInLocation("2006-01-02 15:04:05", "2026-07-01 00:00:00", billing.BusinessLocation)
	to := from.AddDate(0, 1, 0)
	store := &fakeBillingChannelReadStore{latest: channelReadJob("latest", "complete", from, to), rowsByJob: map[string][]billing.AggregateRow{"latest": {}}}
	source := fakeBillingLiveSource{ratio: `{"QuotaPerUnit":"500000","USDExchangeRate":"7.2","general_setting":"{\"quota_display_type\":\"CNY\"}"}`}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/dashboard/billing/channels?instance_id=site-a&from=2026-07-01+00%3A00%3A00&to=2026-08-01+00%3A00%3A00", nil)
	BillingChannelsHandler{Store: store, Source: source}.ServeHTTP(recorder, request)
	if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), `"currency":{"type":"CNY","symbol":"¥","exchange_rate":"7.2"}`) {
		t.Fatalf("unexpected response: %d %s", recorder.Code, recorder.Body.String())
	}
}

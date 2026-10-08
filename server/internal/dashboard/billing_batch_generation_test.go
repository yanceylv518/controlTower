package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type batchStoreTest struct {
	saved     []billing.AutomaticTarget
	cancelled []billing.AutomaticTarget
	failure   error
}

type batchPreviewStoreTest struct {
	batchStoreTest
	resolved bool
	read     []billing.AutomaticTarget
}

func (s *batchPreviewStoreTest) BillingBatchMembers(_ context.Context, targets []billing.AutomaticTarget) ([]billing.AutomaticTarget, error) {
	s.resolved = true
	return append(targets, billing.AutomaticTarget{SubjectID: 99}), nil
}

func (s *batchPreviewStoreTest) BillingGenerationProgress(_ context.Context, target billing.AutomaticTarget) (billing.GenerationProgress, error) {
	s.read = append(s.read, target)
	return billing.GenerationProgress{SubjectID: target.SubjectID}, nil
}

func TestBillingSelectionPreviewDoesNotExpandOrRegisterBatch(t *testing.T) {
	for _, kind := range []string{"user_statement", "upstream_statement"} {
		s := &batchPreviewStoreTest{}
		w := httptest.NewRecorder()
		(BillingBatchGenerationHandler{Store: s}).ServeHTTP(w, httptest.NewRequest("GET", "/?action=preview&instance_id=site&kind="+kind+"&subject_ids=7,8&from=2025-09-01T00:00:00%2B08:00&to=2025-10-01T00:00:00%2B08:00", nil))
		if w.Code != 200 || s.resolved || len(s.saved) != 0 || len(s.read) != 2 {
			t.Fatalf("preview changed membership or registered work: %d %+v", w.Code, s)
		}
		for _, target := range s.read {
			if target.Kind != kind || target.InstanceID != "site" || !target.ProgressUntil.Equal(target.To) {
				t.Fatalf("wrong preview scope: %+v", target)
			}
		}
	}
	// A preview for this month must not reuse an older batch's frozen cutoff.
	now := time.Now().In(billing.BusinessLocation)
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, billing.BusinessLocation)
	if !from.Before(billing.CompleteDayBoundary(now)) {
		return
	}
	s := &batchPreviewStoreTest{}
	w := httptest.NewRecorder()
	path := "/?action=preview&instance_id=site&subject_ids=7&from=" + from.UTC().Format(time.RFC3339) + "&to=" + from.AddDate(0, 1, 0).UTC().Format(time.RFC3339)
	(BillingBatchGenerationHandler{Store: s}).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != 200 || len(s.read) != 1 || !s.read[0].ProgressUntil.Equal(billing.CompleteDayBoundary(now)) {
		t.Fatalf("current cutoff: %d %+v", w.Code, s.read)
	}
}

func (s *batchStoreTest) PutBillingAutomaticTargets(_ context.Context, v []billing.AutomaticTarget) error {
	if s.failure != nil {
		return s.failure
	}
	s.saved = v
	return nil
}
func (s *batchStoreTest) BillingGenerationProgress(_ context.Context, v billing.AutomaticTarget) (billing.GenerationProgress, error) {
	return billing.GenerationProgress{SubjectID: v.SubjectID, Outcome: "registered"}, nil
}
func TestBillingBatchUsersAndMonth(t *testing.T) {
	for _, tc := range []struct {
		ids, from   string
		code, count int
	}{
		{"[7,8,7]", "2025-09-01", 202, 2}, {"[]", "2025-09-01", 400, 0}, {"[7,0]", "2025-09-01", 400, 0}, {"[7,8]", "2025-09-02", 400, 0},
		{"[" + strings.Repeat("7,", 50) + "8]", "2025-09-01", 400, 0},
	} {
		s := &batchStoreTest{}
		w := httptest.NewRecorder()
		body := `{"instance_id":"site","subject_ids":` + tc.ids + `,"from":"` + tc.from + `T00:00:00+08:00","to":"2025-10-01T00:00:00+08:00"}`
		(BillingBatchGenerationHandler{Store: s}).ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader(body)))
		if w.Code != tc.code || len(s.saved) != tc.count {
			t.Fatal(tc, w.Code, w.Body.String(), s.saved)
		}
	}
	s := &batchStoreTest{}
	w := httptest.NewRecorder()
	(BillingBatchGenerationHandler{Store: s}).ServeHTTP(w, httptest.NewRequest("GET", "/?instance_id=site&subject_ids=7,8&from=2025-09-01T00:00:00%2B08:00&to=2025-10-01T00:00:00%2B08:00", nil))
	if w.Code != 200 || len(s.saved) != 0 || !strings.Contains(w.Body.String(), `"subject_id":8`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func (s *batchStoreTest) CancelBillingGeneration(_ context.Context, v []billing.AutomaticTarget) error {
	s.cancelled = v
	return nil
}
func TestBillingBatchCancellationAndBusy(t *testing.T) {
	body := `{"instance_id":"site","subject_ids":[7,8],"from":"2025-09-01T00:00:00+08:00","to":"2025-10-01T00:00:00+08:00","overwrite":true}`
	s := &batchStoreTest{}
	w := httptest.NewRecorder()
	(BillingBatchGenerationHandler{Store: s}).ServeHTTP(w, httptest.NewRequest("POST", "/?action=cancel", strings.NewReader(body)))
	if w.Code != 200 || len(s.cancelled) != 2 || len(s.saved) != 0 {
		t.Fatal(w.Code, s)
	}
	s = &batchStoreTest{failure: billing.ErrGenerationInProgress}
	w = httptest.NewRecorder()
	(BillingBatchGenerationHandler{Store: s}).ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader(body)))
	if w.Code != 409 || len(s.saved) != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	s = &batchStoreTest{}
	w = httptest.NewRecorder()
	(BillingBatchGenerationHandler{Store: s}).ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader(body)))
	if w.Code != 202 || len(s.saved) != 2 || !s.saved[0].Overwrite {
		t.Fatal(w.Code, s)
	}
}

type historyStoreTest struct {
	batchStoreTest
	limit, offset int
}

func (s *historyStoreTest) ListBillingGenerationTasks(_ context.Context, site, kind string, limit, offset int) ([]billing.GenerationTask, int, error) {
	s.limit = limit
	s.offset = offset
	v, _ := s.BillingGenerationTask(context.Background(), site, "task")
	return []billing.GenerationTask{v}, 21, nil
}
func (s *historyStoreTest) BillingGenerationTask(_ context.Context, site, id string) (billing.GenerationTask, error) {
	return billing.GenerationTask{ID: id, InstanceID: site, Kind: "user_statement", SubjectIDs: []int64{7, 8}, Items: []billing.GenerationProgress{{SubjectID: 7}, {SubjectID: 8}}}, nil
}
func TestBillingGenerationHistoryGroupedAndPaged(t *testing.T) {
	s := &historyStoreTest{}
	h := BillingBatchGenerationHandler{Store: s}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/?instance_id=site&action=history&page=2", nil))
	if w.Code != 200 || s.limit != 20 || s.offset != 20 || strings.Count(w.Body.String(), `"id":"task"`) != 1 || strings.Contains(w.Body.String(), `"subject_id":7`) {
		t.Fatal(w.Code, w.Body.String(), s)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/?instance_id=site&action=history-detail&id=task", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"subject_id":7`) || !strings.Contains(w.Body.String(), `"subject_id":8`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestBillingBatchUpstreamKind(t *testing.T) {
	for _, kind := range []string{"upstream_statement", "invalid"} {
		s := &batchStoreTest{}
		w := httptest.NewRecorder()
		body := `{"kind":"` + kind + `","instance_id":"site","subject_ids":[7,8],"from":"2025-09-01T00:00:00+08:00","to":"2025-10-01T00:00:00+08:00"}`
		(BillingBatchGenerationHandler{Store: s}).ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader(body)))
		if kind == "invalid" {
			if w.Code != 400 || len(s.saved) != 0 {
				t.Fatal(w.Code, s.saved)
			}
			continue
		}
		if w.Code != 202 || len(s.saved) != 2 || s.saved[0].Kind != kind {
			t.Fatal(w.Code, w.Body.String(), s.saved)
		}
		w = httptest.NewRecorder()
		(BillingBatchGenerationHandler{Store: s}).ServeHTTP(w, httptest.NewRequest("POST", "/?action=cancel", strings.NewReader(body)))
		if w.Code != 200 || len(s.cancelled) != 2 || s.cancelled[0].Kind != kind {
			t.Fatal(w.Code, s.cancelled)
		}
	}
}

func TestBillingBatchKindPermissions(t *testing.T) {
	for _, tc := range []struct {
		permission, kind string
		code             int
	}{
		{"billing.channels", "upstream_statement", 200},
		{"billing.users", "upstream_statement", 403},
		{"billing.channels", "user_statement", 403},
		{"billing.users", "user_statement", 200},
	} {
		path := "/api/dashboard/billing/generation-batch?instance_id=site&kind=" + tc.kind + "&subject_ids=7&from=2025-09-01T00:00:00%2B08:00&to=2025-10-01T00:00:00%2B08:00"
		preview := menuRequest(t, tc.permission, "GET", path+"&action=preview", BillingBatchGenerationHandler{Store: &batchStoreTest{}})
		if preview.Code != tc.code {
			t.Fatal("preview permission", tc, preview.Code)
		}
		w := menuRequest(t, tc.permission, "GET", path, BillingBatchGenerationHandler{Store: &batchStoreTest{}})
		if w.Code != tc.code {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
}

func TestBillingBatchZeroOutputDefaultsAndOverrides(t *testing.T) {
	for _, kind := range []string{"user_statement", "upstream_statement"} {
		for _, option := range []string{"", `,"exclude_zero_output":true`, `,"exclude_zero_output":false`} {
			store := &batchStoreTest{}
			w := httptest.NewRecorder()
			body := `{"instance_id":"site","kind":"` + kind + `","subject_ids":[7],"from":"2025-09-01T00:00:00+08:00","to":"2025-10-01T00:00:00+08:00"` + option + `}`
			(BillingBatchGenerationHandler{Store: store}).ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader(body)))
			want := kind == "upstream_statement"
			if option != "" {
				want = strings.Contains(option, "true")
			}
			if w.Code != 202 || len(store.saved) != 1 || store.saved[0].ExcludeZeroOutput != want {
				t.Fatal(kind, option, w.Code, w.Body.String(), store.saved)
			}
		}
	}
}

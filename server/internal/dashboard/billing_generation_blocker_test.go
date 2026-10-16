package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

type blockerBatchStore struct {
	batchStoreTest
	site string
}

func (s *blockerBatchStore) BillingGenerationProgress(_ context.Context, target billing.AutomaticTarget) (billing.GenerationProgress, error) {
	outcome := "awaiting_monthly"
	if target.SubjectID == 8 {
		outcome = "complete"
	}
	return billing.GenerationProgress{SubjectID: target.SubjectID, Outcome: outcome}, nil
}
func (s *blockerBatchStore) BillingGenerationBlocker(_ context.Context, site string) (*billing.GenerationBlocker, error) {
	s.site = site
	return &billing.GenerationBlocker{Kind: "billing", Period: "daily", Status: "running", Reason: "source_timeout", Attempts: 3}, nil
}
func TestBillingProgressExplainsBlockOutsideBatch(t *testing.T) {
	s := &blockerBatchStore{}
	w := httptest.NewRecorder()
	(BillingBatchGenerationHandler{Store: s}).ServeHTTP(w, httptest.NewRequest("GET", "/?instance_id=site&subject_ids=7,8&from=2025-09-01T00:00:00%2B08:00&to=2025-10-01T00:00:00%2B08:00", nil))
	var response struct {
		Items []billing.GenerationProgress `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || s.site != "site" || len(response.Items) != 2 {
		t.Fatal(w.Code, w.Body.String())
	}
	p := response.Items[0]
	if p.Pending != 0 || p.Running != 0 || p.Blocker == nil || p.Blocker.Reason != "source_timeout" || response.Items[1].Blocker != nil {
		t.Fatal(response)
	}
}

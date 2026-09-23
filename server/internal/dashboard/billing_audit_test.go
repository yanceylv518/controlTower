package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/billing"
	"controltower/server/internal/storage"
)

type auditDiscountStore struct {
	items  []billing.DiscountRule
	audits []storage.OperationAudit
}

func (s *auditDiscountStore) ListBillingDiscountRules(_ context.Context, site, kind string) ([]billing.DiscountRule, error) {
	out := []billing.DiscountRule{}
	for _, item := range s.items {
		if item.InstanceID == site && item.DiscountType == kind {
			out = append(out, item)
		}
	}
	return out, nil
}
func (s *auditDiscountStore) PutBillingDiscountRule(_ context.Context, item billing.DiscountRule) (billing.DiscountRule, error) {
	for index := range s.items {
		if s.items[index].ID == item.ID {
			s.items[index] = item
			return item, nil
		}
	}
	return billing.DiscountRule{}, http.ErrMissingFile
}
func (s *auditDiscountStore) DeleteBillingDiscountRule(_ context.Context, _ string, _ int64) error {
	return nil
}
func (s *auditDiscountStore) InsertOperationAudit(item storage.OperationAudit) error {
	s.audits = append(s.audits, item)
	return nil
}

func TestBillingDiscountUpdateAuditsBeforeAndAfter(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	store := &auditDiscountStore{items: []billing.DiscountRule{{ID: 17, InstanceID: "site-a", DiscountType: billing.DiscountUpstreamChannel, SubjectID: 17, ChannelID: 4, Discount: "0.8", EffectiveFrom: from}}}
	body := `{"id":17,"instance_id":"site-a","discount_type":"upstream_channel","subject_id":17,"channel_id":4,"discount":"0.9","effective_from":"2026-09-01T00:00:00Z","remark":"adjusted"}`
	w := httptest.NewRecorder()
	BillingDiscountHandler{Store: store}.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/dashboard/billing/discounts", strings.NewReader(body)))
	if w.Code != http.StatusOK || len(store.audits) != 1 {
		t.Fatalf("status=%d body=%s audits=%+v", w.Code, w.Body.String(), store.audits)
	}
	audit := store.audits[0]
	if audit.OperationType != "billing.discount.update" || !strings.Contains(audit.BeforeSummary, `"discount":"0.8"`) || !strings.Contains(audit.AfterSummary, `"discount":"0.9"`) {
		t.Fatalf("before/after missing: %+v", audit)
	}
}

type auditUpstreamStore struct {
	items  []billing.Upstream
	audits []storage.OperationAudit
}

func (s *auditUpstreamStore) ListBillingUpstreams(_ context.Context, site string) ([]billing.Upstream, error) {
	out := []billing.Upstream{}
	for _, item := range s.items {
		if item.InstanceID == site {
			out = append(out, item)
		}
	}
	return out, nil
}
func (s *auditUpstreamStore) PutBillingUpstream(_ context.Context, item billing.Upstream) (billing.Upstream, error) {
	for index := range s.items {
		if s.items[index].ID == item.ID {
			s.items[index] = item
			return item, nil
		}
	}
	return billing.Upstream{}, http.ErrMissingFile
}
func (s *auditUpstreamStore) DeleteBillingUpstream(context.Context, string, int64) error { return nil }
func (s *auditUpstreamStore) InsertOperationAudit(item storage.OperationAudit) error {
	s.audits = append(s.audits, item)
	return nil
}

type auditUpstreamSource struct{}

func (auditUpstreamSource) CurrentChannels(context.Context, string) ([]billing.ConfiguredChannel, error) {
	return []billing.ConfiguredChannel{{ChannelID: 4, ChannelName: "primary"}}, nil
}

func TestBillingUpstreamUpdateAuditsBeforeAndAfter(t *testing.T) {
	store := &auditUpstreamStore{items: []billing.Upstream{{ID: 3, InstanceID: "site-a", Name: "old", Enabled: true, Channels: []billing.UpstreamChannel{{ChannelID: 4, ChannelName: "primary"}}}}}
	body := `{"id":3,"instance_id":"site-a","name":"new","enabled":false,"remark":"maintenance","channels":[{"channel_id":4}]}`
	w := httptest.NewRecorder()
	BillingUpstreamConfigHandler{Store: store, Source: auditUpstreamSource{}}.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/dashboard/billing/upstreams", strings.NewReader(body)))
	if w.Code != http.StatusOK || len(store.audits) != 1 {
		t.Fatalf("status=%d body=%s audits=%+v", w.Code, w.Body.String(), store.audits)
	}
	audit := store.audits[0]
	var before, after billing.Upstream
	if err := json.Unmarshal([]byte(audit.BeforeSummary), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(audit.AfterSummary), &after); err != nil {
		t.Fatal(err)
	}
	if audit.OperationType != "billing.upstream.update" || before.Name != "old" || after.Name != "new" || after.Enabled {
		t.Fatalf("unexpected audit snapshots: %+v before=%+v after=%+v", audit, before, after)
	}
}

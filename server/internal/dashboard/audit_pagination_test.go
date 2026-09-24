package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
)

func TestAuditListCountAndCursor(t *testing.T) {
	s := ingest.NewMemoryStore()
	at := time.Date(2026, 9, 24, 0, 0, 0, 123456000, time.UTC)
	for i := 0; i < 5; i++ {
		if err := s.InsertOperationAudit(storage.OperationAudit{ID: fmt.Sprintf("audit-%d", i), OperationType: "settings.update", ActorID: "admin", CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	h := CommandHandler{Store: s}
	read := func(query string) struct {
		Items   []operationAuditItem `json:"items"`
		Total   int64                `json:"total"`
		HasMore bool                 `json:"has_more"`
	} { w := httptest.NewRecorder(); h.Audits(w, httptest.NewRequest("GET", "/api/dashboard/operation-audits?"+query, nil)); if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}; var result struct {
		Items   []operationAuditItem `json:"items"`
		Total   int64                `json:"total"`
		HasMore bool                 `json:"has_more"`
	}; if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}; return result }
	first := read("list_only=true&limit=2")
	if first.Total != -1 || !first.HasMore || first.Items[0].ID != "audit-4" {
		t.Fatalf("first=%+v", first)
	}
	cursor := "&before_time=" + url.QueryEscape(at.Format(time.RFC3339Nano)) + "&before_id="
	second := read("list_only=true&limit=2" + cursor + first.Items[1].ID)
	if !second.HasMore || len(second.Items) != 2 || second.Items[0].ID != "audit-2" {
		t.Fatalf("second=%+v", second)
	}
	last := read("list_only=true&limit=2" + cursor + second.Items[1].ID)
	if last.HasMore || len(last.Items) != 1 || last.Items[0].ID != "audit-0" {
		t.Fatalf("last=%+v", last)
	}
	count := read("count_only=true" + cursor + "audit-0")
	if count.Total != 5 || len(count.Items) != 0 {
		t.Fatalf("count=%+v", count)
	}
}

func TestAuditInvalidCursorAndCancellation(t *testing.T) {
	s := ingest.NewMemoryStore()
	h := CommandHandler{Store: s}
	for _, query := range []string{"before_time=bad&before_id=x", "before_id=x", "before_time=2026-09-24T00:00:00Z", "list_only=true&count_only=true", "before_time=2026-09-24T00:00:00Z&before_id=x&offset=1"} {
		w := httptest.NewRecorder()
		h.Audits(w, httptest.NewRequest("GET", "/?"+query, nil))
		if w.Code != 400 {
			t.Fatalf("query=%s code=%d", query, w.Code)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.QueryOperationAuditsContext(ctx, storage.OperationAuditQuery{ListOnly: true})
	if err != context.Canceled {
		t.Fatalf("cancellation=%v", err)
	}
}

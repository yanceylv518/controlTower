package mysqlstore

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
)

func TestAuditSearchAcrossStores(t *testing.T) {
	type auditStore interface {
		InsertOperationAudit(storage.OperationAudit) error
		QueryOperationAudits(storage.OperationAuditQuery) (storage.OperationAuditPage, error)
	}
	run := func(t *testing.T, store auditStore, instance string) {
		now := time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC)
		for index, audit := range []storage.OperationAudit{
			{TargetID: "7", BeforeSummary: `{"channel_name":"香港主线","model":"GPT-4","weight":7,"disabled":false}`, AfterSummary: `{"request":{"channels":[7,17],"model":"gpt-5","name":"香港 备用","escaped":"literal %_!","unicode":"café"},"secret":"[redacted]","request_ref":"0123456789abcdef0123456789abcdef","error":"status_code = 429"}`, ErrorSummary: "upstream status_code=429 busy"},
			{TargetID: "17", AfterSummary: `{"weight":70,"model":"gpt-4","name":"香港主线","nested":[{"number":17}],"request_ref":"0123456789abcdef0123456789abcdefextra","error":"status_code=4290"}`, ErrorSummary: "upstream status_code=4290 exhausted"},
			{TargetID: "8", OperationType: "billing.price_update", AfterSummary: `{"model":"GPT-4","name":"香港主线","weight":7}`},
			{TargetID: "9", BeforeSummary: "invalid JSON GPT-4", AfterSummary: `"root scalar"`},
		} {
			audit.ID, audit.InstanceID = fmt.Sprintf("%s-%d", instance, index), instance
			if audit.OperationType == "" {
				audit.OperationType = "tuning.base_update"
			}
			audit.ActorID, audit.Status, audit.ClientIP = "tester", "succeeded", "127.0.0.1"
			if index == 3 {
				audit.ClientIP = "127.0.0.11"
			}
			audit.RequestID, audit.CorrelationID = fmt.Sprintf("request-%d", index), "correlation"
			audit.CreatedAt, audit.UpdatedAt = now.Add(time.Duration(index)*time.Second), now
			if err := store.InsertOperationAudit(audit); err != nil {
				t.Fatal(err)
			}
		}
		for _, test := range []struct {
			name, mode, query, operation string
			targets                      []string
		}{
			{"nested and previous values", "smart", "GPT-4 香港 gpt-5", "tuning.base_update", []string{"7"}},
			{"all terms required", "smart", "GPT-4 nonexistent", "", nil},
			{"numeric IDs are complete", "smart", "7", "tuning.base_update", []string{"7"}},
			{"array number", "smart", "17 香港 备用", "tuning.base_update", []string{"7"}},
			{"quoted phrase", "smart", `"香港 备用" GPT-4`, "", []string{"7"}},
			{"literal wildcards", "smart", "%_!", "", []string{"7"}},
			{"JSON keys excluded", "smart", "weight", "", nil},
			{"redactions excluded", "smart", "redacted", "", nil},
			{"malformed history ignored", "smart", "invalid", "", nil},
			{"root scalar", "smart", `"root scalar"`, "", []string{"9"}},
			{"no accent folding", "smart", "cafe", "tuning.base_update", nil},
			{"type constrains snapshot search", "smart", "GPT-4 香港", "billing.price_update", []string{"8"}},
			{"unified request exact", "smart", "request-0", "", []string{"7"}},
			{"unified request not substring", "smart", "request-", "", nil},
			{"unified IP exact", "smart", "127.0.0.1", "", []string{"7", "17", "8"}},
			{"unified IP not substring", "smart", "127.0.0", "", nil},
			{"unified numeric error code", "smart", "429", "", []string{"7"}},
			{"unified status code assignment", "smart", "status_code=429", "", []string{"7"}},
			{"unified mixed error and content", "smart", "429 GPT-4", "", []string{"7"}},
			{"unified snapshot request ID exact", "smart", "0123456789abcdef0123456789abcdef", "", []string{"7"}},
			{"target exact", "target", "7", "", []string{"7"}},
			{"target no partial", "target", "1", "", nil},
			{"IP exact", "ip", "127.0.0", "", nil},
			{"error only", "error", "429 busy", "", []string{"7"}},
			{"error excludes snapshots", "error", "gpt-5", "", nil},
			{"legacy remains metadata only", "", "gpt-4", "", nil},
			{"legacy substring", "", "7", "tuning.base_update", []string{"7", "17"}},
		} {
			t.Run(test.name, func(t *testing.T) {
				q := storage.OperationAuditQuery{InstanceID: instance, SearchMode: test.mode, Search: test.query, OperationType: test.operation, Limit: 1, From: now, To: now.Add(time.Hour)}
				count, err := store.QueryOperationAudits(q)
				if err != nil {
					t.Fatal(err)
				}
				q.ListOnly = true
				var targets []string
				for {
					page, err := store.QueryOperationAudits(q)
					if err != nil {
						t.Fatal(err)
					}
					for _, item := range page.Items {
						targets = append(targets, item.TargetID)
					}
					if !page.HasMore {
						break
					}
					last := page.Items[len(page.Items)-1]
					q.BeforeTime, q.BeforeID = last.CreatedAt, last.ID
				}
				// 同一查询按时间倒序返回；验收只比较命中集合。
				want := append([]string(nil), test.targets...)
				slices.Sort(targets)
				slices.Sort(want)
				if !reflect.DeepEqual(targets, want) || count.Total != int64(len(want)) {
					t.Fatalf("targets=%v total=%d want=%v", targets, count.Total, want)
				}
				q.CountOnly, q.ListOnly = true, false
				total, err := store.QueryOperationAudits(q)
				if err != nil || total.Total != count.Total {
					t.Fatalf("count-only=%+v err=%v", total, err)
				}
			})
		}
	}
	t.Run("memory", func(t *testing.T) { run(t, ingest.NewMemoryStore(), "search-memory") })
	t.Run("mysql", func(t *testing.T) {
		dsn := os.Getenv("CT_MYSQL_TEST_DSN")
		if dsn == "" {
			t.Skip("set CT_MYSQL_TEST_DSN for MySQL search parity")
		}
		db, err := Open(dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if err := ApplyDir(context.Background(), db, "../../migrations"); err != nil {
			t.Fatal(err)
		}
		instance := fmt.Sprintf("audit-search-%d", time.Now().UnixNano())
		defer func() {
			if _, err := db.Exec("DELETE FROM operation_audits WHERE instance_id=?", instance); err != nil {
				t.Error(err)
			}
		}()
		run(t, New(db), instance)
	})
}

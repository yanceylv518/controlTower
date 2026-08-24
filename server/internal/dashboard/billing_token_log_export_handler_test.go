package dashboard

import (
	"context"
	"database/sql"
	"encoding/csv"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/billing"
)

type tokenLogExportStoreStub struct{}

func (tokenLogExportStoreStub) ListBillingPrices(context.Context, string) ([]billing.PriceRecord, error) {
	return []billing.PriceRecord{{ModelName: "model-a", Price: billing.Price{EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Input: "1", Output: "2", Cache: "0.5", CacheWrite: "1.25"}}}, nil
}
func (tokenLogExportStoreStub) ListBillingGroupRatios(context.Context, string) ([]billing.GroupRatio, error) {
	return []billing.GroupRatio{{GroupName: "default", Ratio: "1"}}, nil
}
func (tokenLogExportStoreStub) ListBillingModelMetadata(context.Context, string) ([]billing.ModelMetadata, error) {
	return []billing.ModelMetadata{{ModelName: "model-a", MaxContextTokens: 2_000_000}}, nil
}

type tokenLogExportSourceStub struct{}

func (tokenLogExportSourceStub) TokenDetailedLogsPage(_ context.Context, _ string, _, _ int64, _, _ time.Time, cursor billing.LogCursor, _ int) ([]billing.PagedLogRecord, error) {
	if cursor.ID != 0 {
		return nil, nil
	}
	return []billing.PagedLogRecord{{
		ID: 1, CreatedUnix: 1_786_032_000, UserID: 7, Username: "alice", TokenID: 3, TokenName: "production-key",
		RequestID: "req-1", ModelName: "model-a", GroupName: "default", PromptTokens: sql.NullInt64{Int64: 1_000_000, Valid: true}, CompletionTokens: sql.NullInt64{Int64: 500_000, Valid: true},
	}}, nil
}

func TestTokenLogExportWritesIdentityOnceAndRequestAmount(t *testing.T) {
	query := url.Values{"instance_id": {"site-a"}, "user_id": {"7"}, "token_id": {"3"}, "from": {"2026-08-01 00:00:00"}, "to": {"2026-08-02 00:00:00"}}
	recorder := httptest.NewRecorder()
	handler := BillingTokenLogExportHandler{Store: tokenLogExportStoreStub{}, Source: tokenLogExportSourceStub{}}
	handler.ServeHTTP(recorder, httptest.NewRequest("GET", "/?"+query.Encode(), nil))
	if recorder.Code != 200 {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(recorder.Body.String(), "\ufeff")))
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if rows[0][0] != "用户名" || rows[0][1] != "alice" || rows[2][0] != "Token 名称" || rows[2][1] != "production-key" {
		t.Fatalf("identity rows=%v", rows[:4])
	}
	if got := strings.Join(rows[5], "|"); got != "请求时间|Request ID|模型|普通输入Token|缓存读取Token|缓存写入Token|输出Token|账单金额|Quota" {
		t.Fatalf("headers=%s", got)
	}
	if got := rows[6][7]; got != "2.000000" {
		t.Fatalf("amount=%q row=%v", got, rows[6])
	}
}

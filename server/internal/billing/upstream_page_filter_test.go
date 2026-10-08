package billing

import (
	"context"
	"database/sql"
	"reflect"
	"sort"
	"testing"
	"time"
)

type upstreamFilterStore struct {
	settlementRunnerStore
	models              map[int64][]string
	processed, abnormal int64
}

func (s *upstreamFilterStore) BillingStatementChannelIDs(context.Context, string) (map[int64]bool, error) {
	return map[int64]bool{1: true, 2: true}, nil
}
func (s *upstreamFilterStore) BillingStatementChannelModels(context.Context, string) (map[int64][]string, error) {
	return s.models, nil
}
func (s *upstreamFilterStore) CompleteBillingStep(_ context.Context, _ Job, _ JobStep, processed, abnormal int64) error {
	s.completed = true
	s.processed, s.abnormal = processed, abnormal
	return nil
}

type upstreamFilterPages struct {
	PageSource
	logs            []PagedLogRecord
	calls, readRows int
}

func (s *upstreamFilterPages) page(from, to time.Time, cursor LogCursor, limit int, models map[int64][]string) ([]PagedLogRecord, error) {
	s.calls++
	var out []PagedLogRecord
	for _, v := range s.logs {
		if v.CreatedUnix < from.Unix() || v.CreatedUnix >= to.Unix() || v.CreatedUnix < cursor.CreatedUnix || (v.CreatedUnix == cursor.CreatedUnix && v.ID <= cursor.ID) {
			continue
		}
		if names := models[v.ChannelID]; len(names) > 0 {
			match := false
			for _, name := range names {
				match = match || name == v.ModelName
			}
			if !match {
				continue
			}
		}
		out = append(out, v)
		if len(out) == limit {
			break
		}
	}
	s.readRows += len(out)
	return out, nil
}
func (s *upstreamFilterPages) DetailedChannelsLogsPage(_ context.Context, _ string, _ []int64, from, to time.Time, cursor LogCursor, limit int) ([]PagedLogRecord, error) {
	return s.page(from, to, cursor, limit, nil)
}

type optimizedUpstreamPages struct {
	*upstreamFilterPages
	wantModels map[int64][]string
	fallback   bool
	t          *testing.T
}

func (s optimizedUpstreamPages) DetailedChannelsModelsLogsPage(_ context.Context, _ string, ids []int64, models map[int64][]string, from, to time.Time, cursor LogCursor, limit int) ([]PagedLogRecord, error) {
	if len(ids) != 2 || !reflect.DeepEqual(models, s.wantModels) {
		s.t.Fatal("runner did not pass frozen bindings", ids, models)
	}
	if s.fallback {
		models = nil
	}
	return s.page(from, to, cursor, limit, models)
}

func TestUpstreamModelPushdownPreservesSettlementAndResume(t *testing.T) {
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, BusinessLocation)
	models := map[int64][]string{1: {"m"}, 2: nil}
	var logs []PagedLogRecord
	for i := int64(1); i <= 4105; i++ {
		v := PagedLogRecord{ID: i, CreatedUnix: day.Unix() + i/3, ChannelID: 1, ModelName: "M", Quota: i * 3,
			PromptTokens: sql.NullInt64{Valid: true, Int64: 100}, CompletionTokens: sql.NullInt64{Valid: true, Int64: i % 4},
			ModelRatio: "1", CompletionRatio: "2", GroupRatio: "1", CacheRatio: "0.1", CacheTokens: 3}
		if i%7 == 0 {
			v.ModelName = "m"
			v.ModelRatio = "2"
		}
		if i%5 == 0 {
			v.ChannelID = 2
		}
		if i%3 == 0 {
			v.QuotaBeforeDiscount = "1234"
		}
		if i%11 == 0 {
			v.QuotaBeforeDiscount = "0"
		}
		logs = append(logs, v)
	}
	endRule := day.Add(500 * time.Second)
	rules := []StatementDiscount{
		{DiscountType: DiscountUpstreamChannel, SubjectID: 9, ChannelID: 1, Discount: "0.8", EffectiveFrom: day, EffectiveTo: &endRule},
		{DiscountType: DiscountUpstreamChannel, SubjectID: 9, ChannelID: 1, Discount: "0.6", EffectiveFrom: endRule},
	}
	job := Job{ID: "job", InstanceID: "site", JobType: "upstream_statement", UpstreamID: 9, DataSource: "source", UsageVersion: SettlementUsageVersion, PricingSource: PricingSourceNewAPI, ExcludeZeroOutput: true}
	for _, cursor := range []LogCursor{{}, {CreatedUnix: logs[1999].CreatedUnix, ID: logs[1999].ID}} {
		var baseline *upstreamFilterStore
		for _, mode := range []string{"legacy", "pushdown", "fallback"} {
			store := &upstreamFilterStore{settlementRunnerStore: settlementRunnerStore{rules: rules}, models: models}
			pages := &upstreamFilterPages{logs: logs}
			var source PageSource = pages
			if mode != "legacy" {
				source = optimizedUpstreamPages{pages, models, mode == "fallback", t}
			}
			if err := (JobRunner{Store: store, Source: source}).processStep(context.Background(), job, JobStep{From: day, To: day.AddDate(0, 0, 1), Cursor: cursor}); err != nil {
				t.Fatal(err)
			}
			if !store.completed || len(store.details) == 0 || store.details[0].Charge.UnitPrices == nil {
				t.Fatal("missing completed details/prices")
			}
			sort.Slice(store.details, func(i, j int) bool { return store.details[i].SourceLogID < store.details[j].SourceLogID })
			if baseline == nil {
				baseline = store
				continue
			}
			if !reflect.DeepEqual(store.details, baseline.details) || store.processed != baseline.processed || store.abnormal != baseline.abnormal {
				t.Fatalf("%s changed records, prices, amount, or anomaly counts at cursor %+v", mode, cursor)
			}
			if mode == "pushdown" && pages.readRows != int(store.processed) {
				t.Fatal("unrelated models fetched")
			}
			t.Logf("%s cursor=%d pages=%d read_rows=%d billed=%d anomalous=%d", mode, cursor.ID, pages.calls, pages.readRows, len(store.details), store.abnormal)
		}
	}
}

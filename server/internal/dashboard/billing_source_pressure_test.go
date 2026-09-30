package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type pressureMoneySource struct{ calls, roles int }

func (s *pressureMoneySource) BillingUserRole(context.Context, string, int64) (int, error) {
	s.roles++
	return 1, nil
}

func (s *pressureMoneySource) RatioSnapshot(context.Context, string) (string, error) {
	return "", fmt.Errorf("full price snapshot must not be read")
}
func (s *pressureMoneySource) MoneyOptionsSnapshot(context.Context, string) (string, error) {
	s.calls++
	return `{"QuotaPerUnit":"500000"}`, nil
}

type pressureFillStore struct {
	automaticStoreTest
	fullQueue bool
}

func (s *pressureFillStore) BillingStatementQueueFull(context.Context) (bool, error) {
	return s.fullQueue, nil
}
func (s *pressureFillStore) FailedStatementMoneySnapshot(context.Context, string) (*billing.MoneySnapshot, error) {
	return nil, nil
}
func (s *pressureFillStore) MissingBillingMonths(context.Context, billing.AutomaticTarget, time.Time) ([]time.Time, error) {
	return nil, nil
}
func (s *pressureFillStore) MissingBillingDays(_ context.Context, v billing.AutomaticTarget, _ time.Time) ([]time.Time, error) {
	return []time.Time{v.From, v.From.AddDate(0, 0, 1), v.From.AddDate(0, 0, 2)}, nil
}
func TestBillingFillObservesMoneyOnceAndFullQueueDoesNotReadSource(t *testing.T) {
	for _, full := range []bool{false, true} {
		store := &pressureFillStore{fullQueue: full}
		source := &pressureMoneySource{}
		from := time.Date(2025, 1, 1, 0, 0, 0, 0, billing.BusinessLocation)
		err := (BillingAutomation{Store: store, Source: source}).Fill(context.Background(), billing.AutomaticTarget{InstanceID: "a", Kind: "user_statement", SubjectID: 7, From: from, To: from.AddDate(0, 0, 3)})
		if err != nil {
			t.Fatal(err)
		}
		if full {
			if source.calls != 0 || len(store.jobs) != 0 {
				t.Fatal("full queue touched source")
			}
		} else {
			if source.calls != 1 || len(store.jobs) != 3 {
				t.Fatal(source.calls, len(store.jobs))
			}
			for _, job := range store.jobs {
				if job.MoneySnapshot.ID != store.jobs[0].MoneySnapshot.ID {
					t.Fatal("same fill observed options repeatedly")
				}
			}
		}
	}
}

func TestCompletedBillingTargetSkipsRoleAndMoneyReads(t *testing.T) {
	source := &pressureMoneySource{}
	store := &boundedAutomaticStore{automaticStoreTest: &automaticStoreTest{}}
	if err := (BillingAutomation{Store: store, Source: source}).Fill(context.Background(), billing.AutomaticTarget{InstanceID: "a", Kind: "user_statement", SubjectID: 7}); err != nil {
		t.Fatal(err)
	}
	if source.roles != 0 || source.calls != 0 {
		t.Fatal("idle target queried source metadata", source)
	}
}

func TestBillingMoneyQueryExcludesModelConfigurations(t *testing.T) {
	calls := 0
	db := sql.OpenDB(reviewConnector{func(ctx context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
		calls++
		if strings.Contains(q, "ModelRatio") || strings.Contains(q, "GroupRatio") || strings.Contains(q, "CompletionRatio") {
			t.Fatal(q)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded options query")
		}
		return &reviewRows{columns: []string{"key", "value"}, values: [][]driver.Value{{"USDExchangeRate", "7.2"}}}, nil
	}})
	defer db.Close()
	h := &PassthroughHandler{Config: customerQueryConfig{}, pools: map[string]passthroughPool{"a": {encrypted: "test-pool", db: db}}}
	snapshot, err := captureBillingMoney(context.Background(), BillingReadonlySource{h}, "a")
	if err != nil || snapshot.QuotaPerUnit != "500000" || calls != 1 {
		t.Fatal(snapshot, err, calls)
	}
}

func TestReportFailureCountWaitsForBillingBudget(t *testing.T) {
	site := "failure-count-pressure-test"
	calls := 0
	db := sql.OpenDB(reviewConnector{func(ctx context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > readonlyQueryTimeout {
			t.Fatal("unbounded count")
		}
		return &reviewRows{columns: []string{"count"}, values: [][]driver.Value{{int64(7)}}}, nil
	}})
	defer db.Close()
	h := &PassthroughHandler{Config: customerQueryConfig{}, pools: map[string]passthroughPool{site: {encrypted: "test-pool", db: db}}}
	release, err := sourceBillingBudget.acquire(context.Background(), site)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	_, err = h.BillingFailedRequestCount(ctx, site, time.Now(), time.Now())
	cancel()
	release()
	if !errors.Is(err, context.DeadlineExceeded) || calls != 0 {
		t.Fatal("count bypassed busy billing reader", err, calls)
	}
	n, err := h.BillingFailedRequestCount(context.Background(), site, time.Now(), time.Now())
	if err != nil || n != 7 || calls != 1 {
		t.Fatal(n, err, calls)
	}
}

type pressureReportStore struct{ SettlementReportStore }

func (pressureReportStore) BillingDataSource(context.Context) (string, error) { return "source", nil }
func (pressureReportStore) ListBillingDiscountRules(context.Context, string, string) ([]billing.DiscountRule, error) {
	return nil, nil
}
func (pressureReportStore) ListBillingUpstreams(context.Context, string) ([]billing.Upstream, error) {
	return nil, nil
}
func TestReportChecksIndexAndStopsOnShortSourcePage(t *testing.T) {
	for _, hasIndex := range []bool{false, true} {
		t.Run(fmt.Sprint(hasIndex), func(t *testing.T) {
			logCalls, optionCalls := 0, 0
			db := sql.OpenDB(reviewConnector{func(ctx context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
				if strings.Contains(q, "information_schema.STATISTICS") {
					rows := &reviewRows{columns: []string{"index", "sequence", "column"}}
					if hasIndex {
						rows.values = [][]driver.Value{{"time_idx", int64(1), "created_at"}}
					}
					return rows, nil
				}
				if strings.Contains(q, "FROM options") {
					optionCalls++
					return &reviewRows{columns: []string{"key", "value"}}, nil
				}
				if strings.Contains(q, "SELECT COUNT(*)") {
					return &reviewRows{columns: []string{"count"}, values: [][]driver.Value{{int64(0)}}}, nil
				}
				logCalls++
				return &reviewRows{columns: strings.Fields("id created request upstream user username token tokenname channel channelname model group prompt completion quota other"), values: [][]driver.Value{{int64(1), int64(100), "r", "", int64(7), "u", int64(1), "t", int64(1), "c", "m", "g", int64(10), int64(2), int64(100), "{}"}}}, nil
			}})
			defer db.Close()
			site := "pressure-report-" + fmt.Sprint(hasIndex)
			h := &PassthroughHandler{Config: customerQueryConfig{}, pools: map[string]passthroughPool{site: {encrypted: "test-pool", db: db}}}
			handler := SettlementReportHandler{Store: pressureReportStore{}, Source: BillingSources{BillingReadonlySource: BillingReadonlySource{h}}}
			day := time.Date(2025, 1, 1, 0, 0, 0, 0, billing.BusinessLocation)
			doc, err := handler.Calculate(context.Background(), site, day, day.AddDate(0, 0, 1), nil)
			if !hasIndex {
				if err == nil || logCalls != 0 || optionCalls != 0 {
					t.Fatal("missing index still scanned source", err, logCalls)
				}
				return
			}
			if err != nil || logCalls != 1 || len(doc.Items) != 1 || doc.Items[0].Requests != 1 {
				t.Fatal("short page should not issue an empty tail read", err, logCalls, doc)
			}
		})
	}
}

func TestBillingNarrowPageMySQL(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires local CT_MYSQL_TEST_DSN; uses uniquely named disposable fixture tables")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	logsName := fmt.Sprintf("ct_billing_pressure_logs_%d", time.Now().UnixNano())
	channelsName := fmt.Sprintf("ct_billing_pressure_channels_%d", time.Now().UnixNano())
	rewrite := func(q string) string {
		q = strings.ReplaceAll(q, " logs", " "+logsName)
		return strings.ReplaceAll(q, " channels", " "+channelsName)
	}
	created := []string{}
	defer func() {
		for _, table := range created {
			if _, e := conn.ExecContext(context.Background(), "DROP TABLE "+table); e != nil {
				t.Error(e)
			}
		}
	}()
	for _, ddl := range []string{
		"CREATE TABLE channels(id BIGINT PRIMARY KEY,name VARCHAR(30))",
		"CREATE TABLE logs(id BIGINT PRIMARY KEY,created_at BIGINT,type INT,user_id BIGINT,channel_id BIGINT,token_id BIGINT,request_id VARCHAR(30),upstream_request_id VARCHAR(30),username VARCHAR(30),token_name VARCHAR(30),model_name VARCHAR(30),`group` VARCHAR(30),prompt_tokens BIGINT,completion_tokens BIGINT,quota BIGINT,other TEXT,INDEX time_idx(created_at))",
		"INSERT INTO channels VALUES(1,'one'),(2,'two')",
	} {
		if _, err = conn.ExecContext(ctx, rewrite(ddl)); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(ddl, "CREATE TABLE channels") {
			created = append(created, channelsName)
		}
		if strings.HasPrefix(ddl, "CREATE TABLE logs") {
			created = append(created, logsName)
		}
	}
	for i := 1; i <= 37; i++ {
		typ := 2
		if i%7 == 0 {
			typ = 5
		}
		var token any = int64(9)
		if i%3 == 0 {
			token = nil
		}
		_, err = conn.ExecContext(ctx, rewrite("INSERT INTO logs VALUES(?,?,?,?,?,?,?,'up','user','token','m','g',10,2,100,?)"), i, 100+i/4, typ, 7+i%2, 1+i%3, token, fmt.Sprint(i), `{"model_ratio":1,"group_ratio":1,"completion_ratio":2,"quota_before_discount":"200"}`)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []string{"all", "user", "channel", "channels", "token-zero"} {
		var query string
		extra := []any{}
		want := []int64{}
		switch mode {
		case "all":
			query, _ = billingLogsPageQuery(0, 0, -1)
		case "user":
			query, _ = billingLogsPageQuery(7, 0, -1)
			extra = []any{7}
		case "channel":
			query, _ = billingLogsPageQuery(0, 1, -1)
			extra = []any{1}
		case "channels":
			query = billingChannelsLogsPageQuery(2)
			extra = []any{1, 3}
		case "token-zero":
			query, _ = billingLogsPageQuery(0, 0, 0)
			extra = []any{0}
		}
		for i := 1; i <= 37; i++ {
			at := 100 + i/4
			if at < 101 || at >= 109 || i%7 == 0 {
				continue
			}
			if mode == "user" && 7+i%2 != 7 {
				continue
			}
			if mode == "channel" && 1+i%3 != 1 {
				continue
			}
			if mode == "channels" && 1+i%3 == 2 {
				continue
			}
			if mode == "token-zero" && i%3 != 0 {
				continue
			}
			want = append(want, int64(i))
		}
		got := []int64{}
		cursor := billing.LogCursor{}
		for page := 0; page < 30; page++ {
			args := billingPageRangeArgs(time.Unix(101, 0), time.Unix(109, 0), cursor)
			args = append(args, extra...)
			args = append(args, 3)
			rows, e := conn.QueryContext(ctx, rewrite(query), args...)
			if e != nil {
				t.Fatal(mode, e)
			}
			values, e := scanBillingLogRows(rows, nil)
			rows.Close()
			if e != nil {
				t.Fatal(e)
			}
			for _, v := range values {
				got = append(got, v.ID)
				if v.QuotaBeforeDiscount != "200" {
					t.Fatal("price evidence lost")
				}
			}
			if len(values) < 3 {
				break
			}
			last := values[len(values)-1]
			cursor = billing.LogCursor{CreatedUnix: last.CreatedUnix, ID: last.ID}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s got=%v want=%v", mode, got, want)
		}
	}
}

package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBillingModelFilterMySQLEquivalence(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires local CT_MYSQL_TEST_DSN; only writes uniquely named fixture tables")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	suffix := fmt.Sprint(time.Now().UnixNano())
	logsName, channelsName := "ct_billing_filter_logs_"+suffix, "ct_billing_filter_channels_"+suffix
	rewrite := func(q string) string {
		return strings.ReplaceAll(strings.ReplaceAll(q, " logs", " "+logsName), " channels", " "+channelsName)
	}
	var created []string
	defer func() {
		cleanup, cancelCleanup := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelCleanup()
		for _, table := range created {
			if _, err := conn.ExecContext(cleanup, "DROP TABLE "+table); err != nil {
				t.Error(err)
			}
		}
	}()
	for _, ddl := range []string{
		"CREATE TABLE channels(id BIGINT PRIMARY KEY,name VARCHAR(30))",
		"CREATE TABLE logs(id BIGINT PRIMARY KEY,created_at BIGINT,type INT,user_id BIGINT,channel_id BIGINT,token_id BIGINT,request_id VARCHAR(30),upstream_request_id VARCHAR(30),username VARCHAR(30),token_name VARCHAR(30),model_name VARCHAR(80),`group` VARCHAR(30),prompt_tokens BIGINT,completion_tokens BIGINT,quota BIGINT,other TEXT,INDEX time_idx(created_at),INDEX channel_time_idx(channel_id,type,created_at)) DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci",
	} {
		if _, err = conn.ExecContext(ctx, rewrite(ddl)); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(ddl, "CREATE TABLE channels") {
			created = append(created, channelsName)
		} else {
			created = append(created, logsName)
		}
	}
	if _, err = conn.ExecContext(ctx, rewrite("INSERT INTO channels VALUES(1,'one'),(2,'two')")); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	insert, err := tx.PrepareContext(ctx, rewrite("INSERT INTO logs VALUES(?,?,?,?,?,?,?,'up','user','token',?,'g',100,?,?,?)"))
	if err != nil {
		t.Fatal(err)
	}
	defer insert.Close()
	names := []any{"m", "M", "m ", "é", "e", "中文", "' OR 1=1 --", "", nil, "e\u0301", "unbound"}
	for i := 0; i < 187; i++ {
		typ := 2
		if i%17 == 0 {
			typ = 5
		}
		var token any = int64(8)
		if i%3 == 0 {
			token = nil
		}
		var output any = int64(i % 9)
		if i%19 == 0 {
			output = nil
		}
		other := fmt.Sprintf(`{"model_ratio":%d,"group_ratio":1,"completion_ratio":2,"cache_ratio":0.1,"cache_tokens":3,"quota_before_discount":"%d","user_model_discount":"0.8"}`, 1+i%2, i*7)
		if i%23 == 0 {
			other = "invalid-json"
		}
		// IDs are deliberately not monotonic with timestamps, and exceed 2^53.
		id := int64(9007199254740993 + (187-i)*3)
		if _, err = insert.ExecContext(ctx, id, 100+i/8, typ, 7+i%2, 1+i%4, token, fmt.Sprint(id), names[i%len(names)], output, i*5, other); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ids := []int64{1, 2, 4} // channel 4 has no name; unbound channel 3 must stay excluded
	from, to := time.Unix(102, 0), time.Unix(120, 0)
	for _, tc := range []struct {
		name   string
		models map[int64][]string
	}{
		{"mixed", map[int64][]string{1: {"m", "é"}, 2: nil, 4: {"中文", ""}, 3: {"unbound"}}},
		{"exact", map[int64][]string{1: {"M", "m ", "e\u0301"}, 2: {"' OR 1=1 --", ""}, 4: {"m", "m"}}},
		{"unrestricted", nil},
		{"large-fallback", map[int64][]string{1: make([]string, 1001)}},
	} {
		for _, initial := range []billing.LogCursor{{}, {CreatedUnix: 110, ID: 9007199254741340}, {CreatedUnix: 120, ID: 1}} {
			read := func(optimized bool) ([]billing.PagedLogRecord, int) {
				cursor := initial
				var out []billing.PagedLogRecord
				nRead := 0
				for page := 0; page < 200; page++ {
					q := billingChannelsLogsPageQuery(len(ids))
					args := []any{from.Unix(), to.Unix(), cursor.CreatedUnix, cursor.CreatedUnix, cursor.ID}
					if optimized {
						var extra []any
						q, extra = billingChannelsModelsPageQuery(ids, tc.models)
						args = append(billingPageRangeArgs(from, to, cursor), extra...)
					} else {
						for _, id := range ids {
							args = append(args, id)
						}
					}
					args = append(args, 3)
					rows, e := conn.QueryContext(ctx, rewrite(q), args...)
					if e != nil {
						t.Fatal(tc.name, e)
					}
					values, e := scanBillingLogRows(rows, nil)
					rows.Close()
					if e != nil {
						t.Fatal(e)
					}
					nRead += len(values)
					for _, v := range values {
						allowed := len(tc.models[v.ChannelID]) == 0
						for _, name := range tc.models[v.ChannelID] {
							allowed = allowed || v.ModelName == name
						}
						if allowed {
							out = append(out, v)
						}
					}
					if len(values) < 3 {
						return out, nRead
					}
					last := values[len(values)-1]
					cursor = billing.LogCursor{CreatedUnix: last.CreatedUnix, ID: last.ID}
				}
				t.Fatal("cursor stalled")
				return nil, 0
			}
			before, oldRead := read(false)
			after, newRead := read(true)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("%s cursor=%+v full records changed: before=%+v after=%+v", tc.name, initial, before, after)
			}
			if tc.name == "exact" || tc.name == "mixed" {
				if newRead != len(after) {
					t.Fatalf("SQL included models Go would reject: read=%d matched=%d", newRead, len(after))
				}
				if initial.CreatedUnix == 0 && (newRead == 0 || newRead >= oldRead) {
					t.Fatal("fixture did not exercise exclusion", oldRead, newRead)
				}
			}
			t.Logf("%s cursor=%d old_read=%d new_read=%d identical_matched=%d", tc.name, initial.CreatedUnix, oldRead, newRead, len(after))
		}
	}
}

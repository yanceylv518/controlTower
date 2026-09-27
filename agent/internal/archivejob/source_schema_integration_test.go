package archivejob

import (
	"encoding/json"
	"strings"
	"testing"
)

// Exercise the actual source-DDL -> monthly-table -> raw-write contract, instead
// of assuming that the reader's channel column is the writer's column.
func TestSourceSchemaPreservedMySQL(t *testing.T) {
	for _, channel := range []string{"channel", "channel_id"} {
		t.Run(channel, func(t *testing.T) {
			e, ctx := fixture(t)
			if channel == "channel_id" {
				if _, err := e.source.ExecContext(ctx, "ALTER TABLE logs RENAME COLUMN channel TO channel_id"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := e.source.ExecContext(ctx, "ALTER TABLE logs ADD COLUMN content LONGTEXT"); err != nil {
				t.Fatal(err)
			}
			start, _ := dateBounds("2026-07-07")
			body := strings.Repeat("归档", 3000)
			if _, err := e.source.ExecContext(ctx, "INSERT INTO logs VALUES(1,?,2,7,199,'m',10,0,9007199254740993,'{}',?)", start, body); err != nil {
				t.Fatal(err)
			}
			conn, err := e.target.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err = e.prepare(ctx, conn); err != nil {
				t.Fatal(err)
			}
			if err = e.ensureMonth(ctx, conn, "logs_202607"); err != nil {
				t.Fatal(err)
			}
			rows, err := readRows(ctx, e.source, "SELECT * FROM logs WHERE id=1")
			if err != nil {
				t.Fatal(err)
			}
			tx, err := conn.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, _, err = writeRaw(ctx, tx, rows[0], &state{}); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			archived, err := readRows(ctx, conn, "SELECT * FROM logs_202607 WHERE id=1")
			if err != nil || len(archived) != 1 {
				t.Fatalf("read: %v %v", archived, err)
			}
			if rowHash(rows[0]) != rowHash(archived[0]) {
				t.Fatal("raw schema or values changed")
			}
			_, aggregate, err := parseAggregate(archived[0])
			if err != nil {
				t.Fatal(err)
			}
			dimensions, _ := json.Marshal(aggregate.Dimensions)
			var values map[string]any
			if err = json.Unmarshal(dimensions, &values); err != nil {
				t.Fatal(err)
			}
			if values[channel] != "199" || aggregate.Amounts["quota"] != "9007199254740993" {
				t.Fatalf("lost source dimensions/precision: %s %+v", dimensions, aggregate.Amounts)
			}
		})
	}
}

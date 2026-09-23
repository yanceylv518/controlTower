package logarchive

import "testing"

func TestLatestRawPositionMySQL(t *testing.T) {
	w, ctx, _, _ := scanFixture(t)
	for _, query := range []string{
		"CREATE TABLE logs_202601(id BIGINT PRIMARY KEY,created_at BIGINT)",
		"CREATE TABLE logs_202602(id BIGINT PRIMARY KEY,created_at BIGINT)",
		"CREATE TABLE logs_202603(id BIGINT PRIMARY KEY,created_at BIGINT)",
		"INSERT INTO logs_202601 VALUES(9007199254740993,1767225600)",
		"INSERT INTO logs_202602 VALUES(100,1769904000)",
	} {
		if _, err := w.target.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	p, err := w.LatestRawPosition(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// A newer month or an empty latest table cannot hide a higher source ID.
	if p.Table != "logs_202601" || p.ID != 9007199254740993 || p.LogTime.Unix() != 1767225600 {
		t.Fatalf("wrong raw endpoint: %+v", p)
	}
}

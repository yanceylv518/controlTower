package containerlogs

import (
	"compress/gzip"
	"context"
	cl "controltower/internal/containerlog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompleteIndexPruningReusesCacheAndRebuildsAppend(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	path := filepath.Join(dir, "app.log")
	record := func(at time.Time) string { return at.Format(time.RFC3339) + " NEEDLE\n" }
	if err := os.WriteFile(path, []byte(record(now.Add(-2*time.Hour))), 0600); err != nil {
		t.Fatal(err)
	}
	q := cl.Query{SourceID: strings.Repeat("a", 64), Container: "c", From: now.Add(-15 * time.Minute), To: now, Keyword: "NEEDLE"}
	engine := NewIndexEngine()
	first := engine.Query(context.Background(), dir, q, time.UTC)
	second := engine.Query(context.Background(), dir, q, time.UTC)
	if !first.Complete || first.IndexedBytes == 0 || !second.Complete || second.ScannedBytes != 0 || len(second.Lines) != 0 {
		t.Fatalf("cache reuse: first=%+v second=%+v", first, second)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString(record(now.Add(-time.Minute)))
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	appended := engine.Query(context.Background(), dir, q, time.UTC)
	if !appended.Complete || len(appended.Lines) != 1 {
		t.Fatalf("appended match pruned: %+v", appended)
	}
}

func TestSnapshotDoesNotProbeGzipAndPageBoundsContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log.gz")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := gzip.NewWriter(file)
	_, err = writer.Write([]byte(strings.Repeat("x", 128*1024)))
	if err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	file.Close()
	now := time.Now().UTC()
	q := cl.Query{SourceID: strings.Repeat("a", 64), Container: "c", From: now.Add(-time.Minute), To: now}
	s := &searchSession{query: q, dir: dir}
	if err = s.snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.files) != 1 || s.compressed != nil || s.indexedBytes != 0 {
		t.Fatal("snapshot processed content")
	}
	engine := NewIndexEngine()
	engine.pageBytes = 1024
	result := engine.Query(context.Background(), dir, q, time.UTC)
	if result.Status != "succeeded" || result.Complete || result.NextCursor == "" || result.ScannedBytes != 1024 {
		t.Fatalf("gzip did not yield within budget: %+v", result)
	}
	engine.Release(result.NextCursor)
}

func TestPruningMustKeepMatchingRecords(t *testing.T) {
	loc := time.UTC
	now := time.Now().UTC().Truncate(time.Second)
	line := func(at time.Time, text string) string {
		return "[GIN] " + at.Format("2006/01/02 - 15:04:05") + " | 200 | 1ms | 10.0.0.1 | POST /v1 " + text + "\n"
	}
	for _, tc := range []struct {
		name, content string
		mod           time.Time
	}{
		{"out_of_order", line(now.Add(2*time.Hour), "future") + line(now.Add(-5*time.Minute), "NEEDLE"), now},
		{"mtime_not_record_time", line(now.Add(-5*time.Minute), "NEEDLE"), now.Add(-2 * time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "app.log")
			if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, tc.mod, tc.mod); err != nil {
				t.Fatal(err)
			}
			q := cl.Query{SourceID: strings.Repeat("a", 64), Container: "c", From: now.Add(-15 * time.Minute), To: now, Keyword: "NEEDLE"}
			result := NewIndexEngine().Query(context.Background(), dir, q, loc)
			if len(result.Lines) != 1 {
				t.Fatalf("matching record lost: %+v", result)
			}
		})
	}
}

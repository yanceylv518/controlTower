package containerlogs

import (
	"bytes"
	"compress/gzip"
	"context"
	cl "controltower/internal/containerlog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func indexTestQuery() cl.Query {
	now := time.Now().UTC().Truncate(time.Second)
	return cl.Query{SourceID: strings.Repeat("a", 64), Container: "new-api", From: now.Add(-time.Minute), To: now, Keyword: "needle"}
}

func finishIndexed(t *testing.T, e *IndexEngine, dir string, q cl.Query) ([]string, int64, int) {
	t.Helper()
	var lines []string
	var bytes int64
	for page := 0; page < 1000; page++ {
		r := e.Query(context.Background(), dir, q, time.UTC)
		if r.Status != "succeeded" || r.ScannedBytes > e.pageBytes {
			t.Fatalf("invalid page: %#v", r)
		}
		lines = append(lines, r.Lines...)
		bytes += r.ScannedBytes
		if r.NextCursor == "" {
			if !r.Complete {
				t.Fatal("completion not explicit", r)
			}
			return lines, bytes, page + 1
		}
		if r.Complete {
			t.Fatal("incomplete page marked complete")
		}
		q.Cursor = r.NextCursor
	}
	t.Fatal("continuation did not converge")
	return nil, 0, 0
}

func TestIndexFindsMiddleBeyondBoth64MiBWindows(t *testing.T) {
	dir := t.TempDir()
	q := indexTestQuery()
	f, err := os.Create(filepath.Join(dir, "large.log"))
	if err != nil {
		t.Fatal(err)
	}
	// Sparse zero regions deliberately include unparseable records, but a valid
	// middle record must still be located after incremental indexing.
	if _, err = f.Seek(70*1024*1024, 0); err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("\n" + q.From.Add(time.Second).Format(time.RFC3339) + " needle-middle\n" + q.From.Add(-time.Hour).Format(time.RFC3339) + " old\n")
	if err = f.Truncate(140 * 1024 * 1024); err != nil {
		t.Fatal(err)
	}
	f.Close()
	engine := NewIndexEngine()
	lines, _, pages := finishIndexed(t, engine, dir, q)
	if len(lines) != 1 || !strings.Contains(lines[0], "needle-middle") || pages < 3 {
		t.Fatal("middle was lost", pages, lines)
	}
}

func TestSparseIndexReuseAppendAndContinuationIdentity(t *testing.T) {
	dir := t.TempDir()
	q := indexTestQuery()
	old := q.From.Add(-time.Hour).Format(time.RFC3339) + " " + strings.Repeat("x", 1024) + "\n"
	filename := filepath.Join(dir, "app.log")
	f, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4096; i++ {
		_, _ = f.WriteString(old)
	}
	_, _ = f.WriteString(q.From.Add(time.Second).Format(time.RFC3339) + " needle-one\n")
	f.Close()
	engine := NewIndexEngine()
	engine.pageBytes = 256 * 1024
	first := engine.Query(context.Background(), dir, q, time.UTC)
	if first.NextCursor == "" || first.Complete || first.Phase != "indexing" {
		t.Fatal(first)
	}
	bad := q
	bad.Cursor = first.NextCursor
	bad.Keyword = "changed"
	if r := engine.Query(context.Background(), dir, bad, time.UTC); r.Status != "failed" {
		t.Fatal("cursor changed filters")
	}
	resume := q
	resume.Cursor = first.NextCursor
	second := engine.Query(context.Background(), dir, resume, time.UTC)
	replay := engine.Query(context.Background(), dir, resume, time.UTC)
	if second.NextCursor != replay.NextCursor || second.IndexedBytes != replay.IndexedBytes {
		t.Fatal("retry advanced cursor")
	}
	resume.Cursor = second.NextCursor
	lines, _, _ := finishIndexed(t, engine, dir, resume)
	if len(lines) != 1 {
		t.Fatal(lines)
	}
	_, cachedBytes, _ := finishIndexed(t, engine, dir, q)
	if cachedBytes > 1024*1024 {
		t.Fatal("cached time lookup scanned unrelated prefix", cachedBytes)
	}
	f, err = os.OpenFile(filename, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(q.From.Add(2*time.Second).Format(time.RFC3339) + " needle-two\n")
	f.Close()
	lines, appendBytes, _ := finishIndexed(t, engine, dir, q)
	if len(lines) != 2 || appendBytes > 2*1024*1024 {
		t.Fatal("append failed to reuse index", appendBytes, lines)
	}
}

func TestCompressedContinuationAndResultPagination(t *testing.T) {
	dir := t.TempDir()
	q := indexTestQuery()
	f, err := os.Create(filepath.Join(dir, "history.log.gz"))
	if err != nil {
		t.Fatal(err)
	}
	z := gzip.NewWriter(f)
	for i := 0; i < cl.MaxLines+25; i++ {
		_, _ = z.Write([]byte(q.From.Add(time.Second).Format(time.RFC3339) + " needle\n"))
	}
	z.Close()
	f.Close()
	engine := NewIndexEngine()
	engine.pageBytes = fileScanLimit
	lines, _, pages := finishIndexed(t, engine, dir, q)
	if len(lines) != cl.MaxLines+25 || pages < 2 {
		t.Fatal("archive page lost/duplicated records", len(lines), pages)
	}
}

func TestIndexCursorExpiresAndDetectsRotation(t *testing.T) {
	dir := t.TempDir()
	q := indexTestQuery()
	filename := filepath.Join(dir, "app.log")
	data := strings.Repeat(q.From.Format(time.RFC3339)+" needle\n", 100)
	if err := os.WriteFile(filename, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	engine := NewIndexEngine()
	engine.pageBytes = 100
	first := engine.Query(context.Background(), dir, q, time.UTC)
	q.Cursor = first.NextCursor
	if q.Cursor == "" {
		t.Fatal("missing cursor")
	}
	engine.sessions[0].expires = time.Now().Add(-time.Second)
	if r := engine.Query(context.Background(), dir, q, time.UTC); r.Status != "failed" {
		t.Fatal("expired cursor reused")
	}
	q.Cursor = ""
	first = engine.Query(context.Background(), dir, q, time.UTC)
	q.Cursor = first.NextCursor
	if err := os.WriteFile(filename, []byte("replaced\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if r := engine.Query(context.Background(), dir, q, time.UTC); r.Status != "failed" {
		t.Fatal("changed file silently continued")
	}
}

func TestIndexedParserAcrossPagesAndAppendToLastRecord(t *testing.T) {
	dir := t.TempDir()
	q := indexTestQuery()
	q.RequestID = "req-1"
	q.ErrorCode = "500"
	name := filepath.Join(dir, "app.log")
	header := q.From.Add(time.Second).Format(time.RFC3339) + " req-1 needle\n"
	if err := os.WriteFile(name, []byte(header+"code="), 0600); err != nil {
		t.Fatal(err)
	}
	engine := NewIndexEngine()
	engine.pageBytes = 7
	lines, _, _ := finishIndexed(t, engine, dir, q)
	if len(lines) != 0 {
		t.Fatal(lines)
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("500 password=private\n")
	f.Close()
	lines, _, _ = finishIndexed(t, engine, dir, q)
	if len(lines) != 2 || strings.Contains(strings.Join(lines, "\n"), "private") {
		t.Fatal("partial-line append or multiline match failed", lines)
	}
}

// Whole-file pruning requires a complete index; file metadata alone is insufficient.
func TestCompleteIndexPrunesFilesOutsideWindow(t *testing.T) {
	dir := t.TempDir()
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, loc)
	line := func(at time.Time, text string) string {
		return "[GIN] " + at.Format("2006/01/02 - 15:04:05") + " | 200 | 1ms | 10.0.0.1 | POST /v1 " + text + "\n"
	}
	write := func(name, content string, mod time.Time) {
		path := filepath.Join(dir, name)
		if strings.HasSuffix(name, ".gz") {
			var buf bytes.Buffer
			w := gzip.NewWriter(&buf)
			_, _ = w.Write([]byte(content))
			_ = w.Close()
			if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	// Rotated two days ago: last write long before the window.
	write("app.log.2026-09-06.gz", line(now.Add(-48*time.Hour), "old NEEDLE"), now.Add(-47*time.Hour))
	// Written after the window: first record already past the window end.
	write("app.log.future", line(now.Add(3*time.Hour), "future NEEDLE"), now.Add(4*time.Hour))
	// Overlapping: must be indexed and matched.
	write("app.log", line(now.Add(-2*time.Hour), "early")+line(now.Add(-10*time.Minute), "hit NEEDLE"), now)
	q := cl.Query{SourceID: strings.Repeat("a", 64), Container: "c", From: now.Add(-30 * time.Minute), To: now, Keyword: "NEEDLE"}
	s := &searchSession{query: q, dir: dir}
	if err := s.snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.files) != 3 || s.pruned != 0 {
		t.Fatalf("pruning wrong: files=%v pruned=%d", s.files, s.pruned)
	}
	result := NewIndexEngine().Query(context.Background(), dir, q, loc)
	if result.Status != "succeeded" || !result.Complete || len(result.Lines) != 1 || !strings.Contains(result.Lines[0], "hit NEEDLE") || !strings.Contains(result.Note, "跳过 1 个") {
		t.Fatalf("pruned query wrong: %+v", result)
	}
}

package containerlogs

import (
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOnlyLatestTwoFilesAcrossPlainAndGzip(t *testing.T) {
	dir := t.TempDir()
	q := streamTestQuery()
	now := time.Now().UTC()
	for n, name := range []string{"app.log.old", "app.log", "app.log.gz"} {
		path := filepath.Join(dir, name)
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		line := q.From.Add(time.Second).Format(time.RFC3339) + " needle " + name + "\n"
		if strings.HasSuffix(name, ".gz") {
			gz := gzip.NewWriter(f)
			if _, err = gz.Write([]byte(line)); err != nil {
				t.Fatal(err)
			}
			if err = gz.Close(); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err = f.WriteString(line); err != nil {
				t.Fatal(err)
			}
		}
		f.Close()
		mod := now.Add(time.Duration(n-3) * time.Minute)
		if err = os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	s := &searchSession{dir: dir, query: q}
	if err := s.snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.files) != 2 || s.excluded != 1 || s.files[0].name != "app.log.gz" || s.files[1].name != "app.log" {
		t.Fatalf("wrong latest set: %+v", s.files)
	}
	engine := NewStreamEngine()
	engine.pageBytes = 7
	var lines []string
	var resultNote string
	for n := 0; n < 200; n++ {
		r := engine.Query(context.Background(), dir, q, time.UTC)
		if r.Status != "succeeded" {
			t.Fatal(r)
		}
		lines = append(lines, r.Lines...)
		if n == 0 {
			// New files cannot expand the frozen two-file scope during continuation.
			if err := os.WriteFile(filepath.Join(dir, "app.log.new"), []byte(q.From.Format(time.RFC3339)+" needle unexpected\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if r.NextCursor == "" {
			resultNote = r.Note
			break
		}
		q.Cursor = r.NextCursor
	}
	if len(lines) != 2 || strings.Contains(strings.Join(lines, "\n"), "unexpected") || strings.Contains(strings.Join(lines, "\n"), "app.log.old") || strings.Contains(resultNote, "文件未纳入") {
		t.Fatalf("scope escaped: %v %s", lines, resultNote)
	}
}

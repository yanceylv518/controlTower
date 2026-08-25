package billing

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestFileDetailSpoolWritesAtomicReadablePages(t *testing.T) {
	root := t.TempDir()
	spool := FileDetailSpool{Root: root}
	job := Job{ID: "0123456789abcdef0123456789abcdef"}
	step := JobStep{StepNo: 3}
	cursor := LogCursor{CreatedUnix: 123, ID: 456}
	want := []RequestDetail{{JobID: job.ID, SourceLogID: 456, UserID: 7, ModelName: "model-a", BillDay: time.Now(), Charge: LogCharge{Total: "1.25"}}}
	if err := spool.WritePage(context.Background(), job, step, cursor, want); err != nil {
		t.Fatal(err)
	}
	pages, err := spool.OpenPages(context.Background(), job)
	if err != nil || len(pages) != 1 {
		t.Fatalf("pages=%d err=%v", len(pages), err)
	}
	var got []RequestDetail
	if err = pages[0].Read(func(row RequestDetail) error { got = append(got, row); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SourceLogID != want[0].SourceLogID || got[0].Charge.Total != "1.25" {
		t.Fatalf("unexpected rows: %+v", got)
	}
	if matches, _ := filepath.Glob(filepath.Join(root, job.ID, "*", "*.tmp")); len(matches) != 0 {
		t.Fatalf("temporary page leaked: %v", matches)
	}
	if err = spool.RemoveJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if pages, err = spool.OpenPages(context.Background(), job); err != nil || len(pages) != 0 {
		t.Fatalf("pages after cleanup=%d err=%v", len(pages), err)
	}
}

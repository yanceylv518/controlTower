package containerlogs

import (
	"context"
	cl "controltower/internal/containerlog"
	"strings"
	"testing"
	"time"
)

func TestAutomaticQueryAggregatesPagesIntoOneTask(t *testing.T) {
	q := indexTestQuery()
	a := newAutomaticQuery(cl.Task{ID: "same-task", Query: q})
	calls := 0
	read := func(_ context.Context, got cl.Query) cl.Result {
		calls++
		if calls == 1 {
			if got.Cursor != "" {
				t.Fatal(got)
			}
			return cl.Result{Status: "succeeded", Phase: "indexing", ScannedBytes: 64 * 1024 * 1024, IndexedBytes: 64 * 1024 * 1024, NextCursor: strings.Repeat("a", 64)}
		}
		if calls == 2 {
			if got.Cursor != strings.Repeat("a", 64) {
				t.Fatal("not continued")
			}
			return cl.Result{Status: "succeeded", Phase: "querying", ScannedBytes: 50, Lines: []string{"first"}, NextCursor: strings.Repeat("b", 64)}
		}
		if got.Cursor != strings.Repeat("b", 64) {
			t.Fatal("wrong cursor")
		}
		return cl.Result{Status: "succeeded", Complete: true, Phase: "complete", ScannedBytes: 20, Lines: []string{"second"}}
	}
	for i := 0; i < 2; i++ {
		r := a.step(context.Background(), read)
		if r.Status != "running" || r.NextCursor != "" || r.Complete {
			t.Fatal("page exposed to user", r)
		}
	}
	r := a.step(context.Background(), read)
	if r.Status != "succeeded" || !r.Complete || strings.Join(r.Lines, ",") != "first,second" || r.TotalScannedBytes != 64*1024*1024+70 || a.task.ID != "same-task" {
		t.Fatal(r)
	}
}

func TestAutomaticQueryResultCapAndTimeoutAreExplicit(t *testing.T) {
	a := newAutomaticQuery(cl.Task{Query: indexTestQuery()})
	r := a.step(context.Background(), func(context.Context, cl.Query) cl.Result {
		return cl.Result{Status: "succeeded", Lines: strings.Split(strings.Repeat("line,", cl.MaxLines), ",")[:cl.MaxLines], NextCursor: strings.Repeat("a", 64)}
	})
	if r.Status != "succeeded" || !r.Truncated || r.Complete || r.NextCursor != "" {
		t.Fatal("cap reported complete", r)
	}
	a = newAutomaticQuery(cl.Task{Query: indexTestQuery()})
	a.started = time.Now().Add(-automaticQueryTimeout - time.Second)
	r = a.step(context.Background(), func(context.Context, cl.Query) cl.Result { t.Fatal("ran expired query"); return cl.Result{} })
	if r.Status != "timed_out" || r.Complete || r.Error == "" {
		t.Fatal(r)
	}
}

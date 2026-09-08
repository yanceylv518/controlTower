package containerlog

import (
	"testing"
	"time"
)

func TestQueryBounds(t *testing.T) {
	now := time.Now().UTC()
	q := Query{SourceID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Container: "new-api", From: now.Add(-time.Hour), To: now}
	if q.Validate(now) != nil {
		t.Fatal("valid query rejected")
	}
	boundary := q
	boundary.From = now.Add(-72 * time.Hour)
	boundary.To = boundary.From.Add(time.Hour)
	if err := boundary.Validate(now); err != nil {
		t.Fatalf("72-hour boundary rejected: %v", err)
	}
	boundary.From = boundary.From.Add(-time.Nanosecond)
	boundary.To = boundary.From.Add(time.Hour)
	if boundary.Validate(now) == nil {
		t.Fatal("query older than 72 hours accepted")
	}
	for _, v := range []Query{
		{Container: "--help", From: q.From, To: q.To},
		{Container: q.Container, From: q.From, To: q.From},
		{Container: q.Container, From: now.Add(-2 * time.Hour), To: now},
		{Container: q.Container, From: now.Add(-4 * 24 * time.Hour), To: now.Add(-4*24*time.Hour + time.Hour)},
		{Container: q.Container, From: q.From, To: q.To, RequestID: "$(whoami)"},
	} {
		v.SourceID = q.SourceID
		if v.Validate(now) == nil {
			t.Fatalf("accepted invalid query %#v", v)
		}
	}
}

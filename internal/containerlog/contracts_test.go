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
	for _, v := range []Query{
		{Container: "--help", From: q.From, To: q.To},
		{Container: q.Container, From: q.From, To: q.From},
		{Container: q.Container, From: now.Add(-2 * time.Hour), To: now},
		{Container: q.Container, From: now.Add(-8 * 24 * time.Hour), To: now.Add(-8*24*time.Hour + time.Hour)},
		{Container: q.Container, From: q.From, To: q.To, RequestID: "$(whoami)"},
	} {
		v.SourceID = q.SourceID
		if v.Validate(now) == nil {
			t.Fatalf("accepted invalid query %#v", v)
		}
	}
}

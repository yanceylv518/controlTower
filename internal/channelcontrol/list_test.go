package channelcontrol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListChannelsReadsEveryPage(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer admin-token" || r.Header.Get("New-Api-User") != "7" {
			t.Error("missing admin authentication")
		}
		if r.URL.Query().Get("p") == "1" {
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"id":1,"name":"one","status":1,"weight":10,"priority":3,"models":"m","group":"default","key":"must-not-escape"}],"total":2}}`))
		} else {
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"id":2,"name":"two","status":2,"weight":0,"priority":0}],"total":2}}`))
		}
	}))
	defer server.Close()
	items, err := New(server.URL, "admin-token", 7, server.Client()).List(context.Background())
	if err != nil || calls != 2 || len(items) != 2 || items[0].Priority != 3 || items[1].Status != 2 {
		t.Fatalf("list: %#v calls=%d err=%v", items, calls, err)
	}
}

func TestListChannelsRejectsIncompleteOrRepeatedPages(t *testing.T) {
	for _, body := range []string{
		`{"success":true,"data":{}}`,
		`{"success":true,"data":{"items":[],"total":2}}`,
		`{"success":true,"data":{"items":[{"id":1}],"total":2}}`,
		`{"success":false,"message":"denied"}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			items, err := New(server.URL, "token", 7, server.Client()).List(context.Background())
			if err == nil || items != nil {
				t.Fatalf("incomplete snapshot accepted: %#v %v", items, err)
			}
		})
	}
}

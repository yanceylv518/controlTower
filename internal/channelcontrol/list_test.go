package channelcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
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

// A channel deleted while paging shifts every later offset left by one, so the
// row that would have opened the next page is never returned. The list must be
// rejected instead of being stored as a full snapshot that drops a live
// channel's local configuration.
func TestListChannelsRejectsDeletionDuringPagination(t *testing.T) {
	ids := map[int64]bool{}
	for i := int64(1); i <= 250; i++ {
		ids[i] = true
	}
	page := func(p int) ([]Channel, int) {
		sorted := make([]int64, 0, len(ids))
		for id := range ids {
			sorted = append(sorted, id)
		}
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] > sorted[j] }) // id_sort: id desc
		out := []Channel{}
		for i := (p - 1) * 100; i < p*100 && i < len(sorted); i++ {
			out = append(out, Channel{ID: sorted[i], Name: strconv.FormatInt(sorted[i], 10)})
		}
		return out, len(sorted)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := strconv.Atoi(r.URL.Query().Get("p"))
		if p == 2 {
			delete(ids, 200) // already listed on page 1; page 2 now starts one row later
		}
		items, total := page(p)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"items": items, "total": total}})
	}))
	defer server.Close()
	items, err := New(server.URL, "token", 7, server.Client()).List(context.Background())
	if !errors.Is(err, ErrListChanged) || items != nil {
		t.Fatalf("shifted pagination accepted as a full snapshot: %d items, err=%v", len(items), err)
	}
}

package channelcontrol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUpdatePreservesChannelFieldsWithoutSendingKey(t *testing.T) {
	var putBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer admin-token" || r.Header.Get("New-Api-User") != "7" {
			t.Fatalf("missing admin auth headers")
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":12,"name":"primary","key":"secret","base_url":"https://upstream","status":1,"weight":10,"priority":2}}`))
			return
		}
		if r.Method == http.MethodPut {
			if err := json.NewDecoder(r.Body).Decode(&putBody); err != nil {
				t.Fatalf("decode PUT body: %v", err)
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	weight := uint(20)
	result, err := New(server.URL, "admin-token", 7, server.Client()).Update(context.Background(), UpdateRequest{ChannelID: 12, Weight: &weight})
	if err != nil {
		t.Fatalf("update channel: %v", err)
	}
	if _, ok := putBody["key"]; ok || putBody["weight"] != float64(20) {
		t.Fatalf("unsafe PUT body: %#v", putBody)
	}
	if _, ok := putBody["status"]; ok {
		t.Fatalf("general update must not contain status: %#v", putBody)
	}
	if result.ChannelID != 12 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if strings.Contains(string(mustJSON(putBody)), "secret") {
		t.Fatalf("PUT body leaked secret: %#v", putBody)
	}
}

func TestNoChangeUpdateOmitsStatusReturnedByChannelLookup(t *testing.T) {
	var putBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/channel/12":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":12,"name":"primary","status":1,"weight":10,"priority":2}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/channel/":
			_ = json.NewDecoder(r.Body).Decode(&putBody)
			if _, exists := putBody["status"]; exists {
				_, _ = w.Write([]byte(`{"success":false,"message":"Invalid parameters"}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	if _, err := New(server.URL, "admin-token", 7, server.Client()).Update(context.Background(), UpdateRequest{ChannelID: 12}); err != nil {
		t.Fatalf("no-change update: %v", err)
	}
	if putBody["id"] != float64(12) || putBody["weight"] != float64(10) {
		t.Fatalf("unexpected no-change body: %#v", putBody)
	}
}

func TestStatusUpdateUsesDedicatedEndpoint(t *testing.T) {
	var generalHasStatus bool
	var statusBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":12,"status":1,"weight":10,"priority":2}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/channel/":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			_, generalHasStatus = body["status"]
			_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/channel/12/status":
			_ = json.NewDecoder(r.Body).Decode(&statusBody)
			_, _ = w.Write([]byte(`{"success":true,"data":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	status := 2
	if _, err := New(server.URL, "admin-token", 7, server.Client()).Update(context.Background(), UpdateRequest{ChannelID: 12, Status: &status}); err != nil {
		t.Fatalf("status update: %v", err)
	}
	if generalHasStatus || statusBody["status"] != float64(2) {
		t.Fatalf("status was not isolated: general=%v status=%#v", generalHasStatus, statusBody)
	}
}

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}

func TestCheckUsesReadOnlyChannelList(t *testing.T) {
	var method, path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer admin-token" || r.Header.Get("New-Api-User") != "7" {
			t.Fatalf("missing admin auth headers")
		}
		method, path = r.Method, r.URL.Path
		// Real new-api GetAllChannels returns data as an object (verified in
		// source: common.ApiSuccess with items/total/page keys).
		_, _ = w.Write([]byte(`{"success":true,"data":{"items":[],"total":0,"page":1,"page_size":1}}`))
	}))
	defer server.Close()

	if err := New(server.URL, "admin-token", 7, server.Client()).Check(context.Background()); err != nil {
		t.Fatalf("check: %v", err)
	}
	if method != http.MethodGet || path != "/api/channel/" {
		t.Fatalf("check must be a read-only channel list call: %s %s", method, path)
	}
}

func TestCheckSurfacesAPIFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"message":"unauthorized"}`))
	}))
	defer server.Close()
	err := New(server.URL, "admin-token", 7, server.Client()).Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("api failure must surface: %v", err)
	}
}

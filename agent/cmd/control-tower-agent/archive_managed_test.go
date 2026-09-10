package main

import (
	"context"
	"controltower/agent/internal/config"
	ac "controltower/internal/archivecontrol"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestManagedArchiveAdvertisesWithoutReadingSource(t *testing.T) {
	received := make(chan ac.Status, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/log-archive/poll" || r.Header.Get("Authorization") != "Bearer instance-token" {
			t.Errorf("wrong request")
		}
		var p ac.Status
		_ = json.NewDecoder(r.Body).Decode(&p)
		received <- p
		_ = json.NewEncoder(w).Encode(ac.Response{Config: ac.Default()})
	}))
	defer server.Close()
	stop := startManagedArchive(context.Background(), config.Config{ServerURL: server.URL, AgentToken: "instance-token", AgentID: "agent", LogDSN: "not-a-db"})
	defer stop()
	select {
	case p := <-received:
		if p.Configured || p.State != "unconfigured" || !p.Validate() {
			t.Fatalf("bad advertisement: %+v", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("control did not poll")
	}
}
func TestArchivePollRejectsInvalidServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401); _, _ = w.Write([]byte("secret")) }))
	defer server.Close()
	_, err := archivePoll(context.Background(), server.Client(), config.Config{ServerURL: server.URL}, ac.Status{})
	if err == nil {
		t.Fatal("accepted failed control response")
	}
}

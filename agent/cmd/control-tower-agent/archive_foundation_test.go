package main

import (
	"controltower/agent/internal/config"
	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
	"testing"
	"time"
)

func TestFoundationAgentNeverAcceptsLegacyWriterGrant(t *testing.T) {
	c := ac.Default()
	c.AgentID = "agent"
	c.InstanceID = "instance"
	c.Running = true
	out := ac.Response{Granted: true, Config: c, LeaseSeconds: 120}
	now := time.Now()
	expires := now.Add(120 * time.Second)
	cfg := config.Config{AgentID: "agent", InstanceID: "instance"}
	if !archiveMayRun(out, expires, now, cfg) {
		t.Fatal("legacy grant unexpectedly rejected")
	}
	cfg.LogArchiveIdentity = af.Identity{SiteID: "site", DatasetID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SourceGenerationID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	if archiveMayRun(out, expires, now, cfg) {
		t.Fatal("foundation agent accepted old server grant")
	}
}

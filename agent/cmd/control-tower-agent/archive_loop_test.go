package main

import (
	"controltower/agent/internal/config"
	ac "controltower/internal/archivecontrol"
	"testing"
	"time"
)

func TestArchiveTwoSecondBatchesStayWithinLease(t *testing.T) {
	now := time.Now()
	cfg := config.Config{AgentID: "a", InstanceID: "i"}
	c := ac.Default()
	c.AgentID = "a"
	c.InstanceID = "i"
	c.Running = true
	c.IntervalSeconds = 2
	out := ac.Response{Config: c, Granted: true, LeaseSeconds: 120}
	for _, after := range []time.Duration{0, 2 * time.Second, 4 * time.Second, 14 * time.Second} {
		if !archiveMayRun(out, now.Add(120*time.Second), now.Add(after), cfg) {
			t.Fatal("valid batch blocked", after)
		}
	}
	if archiveMayRun(out, now.Add(44*time.Second), now, cfg) {
		t.Fatal("batch can outlive lease")
	}
	out.Config.Running = false
	if archiveMayRun(out, now.Add(120*time.Second), now, cfg) {
		t.Fatal("paused batch granted")
	}
	out.Config.Running = true
	out.Granted = false
	if archiveMayRun(out, now.Add(120*time.Second), now, cfg) {
		t.Fatal("ungranted batch allowed")
	}
}

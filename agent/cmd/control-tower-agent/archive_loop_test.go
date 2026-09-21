package main

import (
	"testing"
	"time"
)

func TestArchiveTwoSecondBatchesStayWithinLease(t *testing.T) {
	now := time.Now()
	cfg, st, out := validV2Control()
	out.Config.IntervalSeconds = 2
	for _, after := range []time.Duration{0, 2 * time.Second, 4 * time.Second, 14 * time.Second} {
		if _, _, ok := archiveWriterGrant(out, cfg, st.Session, now.Add(120*time.Second), now.Add(after)); !ok {
			t.Fatal("valid batch blocked", after)
		}
	}
	if _, _, ok := archiveWriterGrant(out, cfg, st.Session, now.Add(35*time.Second), now); ok {
		t.Fatal("batch can outlive lease")
	}
	out.Config.Running = false
	if _, _, ok := archiveWriterGrant(out, cfg, st.Session, now.Add(120*time.Second), now); ok {
		t.Fatal("paused batch granted")
	}
	out.Config.Running = true
	out.Granted = false
	if _, _, ok := archiveWriterGrant(out, cfg, st.Session, now.Add(120*time.Second), now); ok {
		t.Fatal("ungranted batch allowed")
	}
}

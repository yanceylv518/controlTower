package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"controltower/agent/internal/config"
	ac "controltower/internal/archivecontrol"
	aj "controltower/internal/archivejob"
)

type archiveReporterStub struct {
	calls  int
	status aj.Status
	err    error
}

func (r *archiveReporterStub) Refresh(ctx context.Context) (aj.Status, error) {
	r.calls++
	return r.status, r.err
}

func TestArchiveCountReportingUsesPausedOwnerLease(t *testing.T) {
	cfg := config.Config{AgentID: "agent", InstanceID: "instance"}
	c := ac.Default()
	c.AgentID, c.InstanceID, c.Tasks = cfg.AgentID, cfg.InstanceID, &aj.Settings{}
	base := ac.Response{Config: c, StatusAccepted: true, Granted: false, LeaseSeconds: 30}
	for _, test := range []struct {
		name string
		edit func(*ac.Response, *ac.Status, *time.Time)
		want int
	}{
		{"paused owner", func(*ac.Response, *ac.Status, *time.Time) {}, 1},
		{"running owner", func(o *ac.Response, _ *ac.Status, _ *time.Time) { o.Config.Running = true; o.Granted = true }, 1},
		{"another owner", func(o *ac.Response, _ *ac.Status, _ *time.Time) { o.StatusAccepted = false }, 0},
		{"another agent", func(o *ac.Response, _ *ac.Status, _ *time.Time) { o.Config.AgentID = "other" }, 0},
		{"another instance", func(o *ac.Response, _ *ac.Status, _ *time.Time) { o.Config.InstanceID = "other" }, 0},
		{"unconfigured", func(_ *ac.Response, s *ac.Status, _ *time.Time) { s.Configured = false }, 0},
		{"expired lease", func(_ *ac.Response, _ *ac.Status, start *time.Time) { *start = time.Now().Add(-30 * time.Second) }, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			o, st, started := base, ac.Status{Configured: true, State: "paused"}, time.Now()
			test.edit(&o, &st, &started)
			r := &archiveReporterStub{status: aj.Status{Protocol: aj.Protocol, Days: []aj.Day{{Date: "2026-01-01", Rows: "7", State: "pending"}}}}
			refreshArchiveJobs(context.Background(), r, cfg, o, started, &st)
			if r.calls != test.want || st.State != "paused" {
				t.Fatalf("reporting call/state: %d, %+v", r.calls, st)
			}
			if test.want != 0 && (st.Engine == nil || st.Engine.Days[0].Rows != "7") {
				t.Fatal("paused owner did not publish counts")
			}
		})
	}
}

func TestArchiveCountFailureRetainsPositionAndTaskError(t *testing.T) {
	cfg := config.Config{AgentID: "agent", InstanceID: "instance"}
	c := ac.Default()
	c.AgentID, c.InstanceID, c.Tasks = cfg.AgentID, cfg.InstanceID, &aj.Settings{}
	out := ac.Response{Config: c, StatusAccepted: true, LeaseSeconds: 30}
	st := ac.Status{Configured: true, State: "error", Engine: &aj.Status{FirstDate: "2026-01-01", Collection: aj.Progress{AfterID: 100, Error: "batch_failed"}}}
	r := &archiveReporterStub{status: aj.Status{CountsError: "database_timeout"}, err: errors.New("read failed")}
	refreshArchiveJobs(context.Background(), r, cfg, out, time.Now(), &st)
	if st.Engine.FirstDate != "2026-01-01" || st.Engine.Collection.AfterID != 100 || st.Engine.CountsError != "database_timeout" {
		t.Fatal("transient read erased position", st.Engine)
	}
	r.err, r.status = nil, aj.Status{FirstDate: "2026-01-01", Collection: aj.Progress{AfterID: 100}}
	refreshArchiveJobs(context.Background(), r, cfg, out, time.Now(), &st)
	if st.Engine.Collection.Error != "batch_failed" || st.Engine.CountsError != "" {
		t.Fatal("count reporting hid task error", st.Engine)
	}
}

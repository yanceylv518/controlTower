package ingest

import (
	"controltower/server/internal/agentgateway"
	"controltower/server/internal/storage"
	"testing"
	"time"
)

type sourceGatedStore struct {
	*MemoryStore
	accept  bool
	batches int
}

func (s *sourceGatedStore) AcceptAgentChannelSnapshots(string) (bool, error) { return s.accept, nil }
func (s *sourceGatedStore) SyncChannelSnapshotsAt(_ string, snapshots []storage.ChannelSnapshot, _ time.Time) error {
	s.batches++
	for _, v := range snapshots {
		if err := s.InsertChannelSnapshot(v); err != nil {
			return err
		}
	}
	return nil
}
func TestReadonlySourceIgnoresOnlyAgentChannelInventory(t *testing.T) {
	for _, complete := range []bool{false, true} {
		for _, accept := range []bool{false, true} {
			s := &sourceGatedStore{MemoryStore: NewMemoryStore(), accept: accept}
			now := time.Now().UTC()
			req := agentgateway.AgentReportRequest{InstanceID: "a", AgentID: "agent", ReportedAt: now, Sequence: 1, ChannelSnapshotComplete: complete,
				ChannelSnapshots: []agentgateway.ChannelSnapshotPayload{{ChannelID: 7, ChannelName: "agent-channel", Status: "enabled", Weight: 1, ModelsText: "m", CapturedAt: now}},
				LogEvents:        []agentgateway.LogEventPayload{{SourceLogID: 1, CreatedAt: now, LogType: "consume", ChannelID: 7, ModelName: "m", TotalTokens: 1}}}
			if err := NewService(s).SaveReport(req); err != nil {
				t.Fatal(err)
			}
			rows, _ := s.QueryChannelSnapshots(storage.ChannelSnapshotQuery{})
			want := 0
			if accept {
				want = 1
			}
			if len(rows) != want || s.LogEventCount() != 1 {
				t.Fatalf("complete=%v accept=%v channels=%d logs=%d", complete, accept, len(rows), s.LogEventCount())
			}
			if !accept && s.batches != 0 {
				t.Fatal("ignored inventory still triggered full snapshot cleanup")
			}
		}
	}
}

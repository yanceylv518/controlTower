package archivecontract

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWorkflowPreparationWireCompatibility(t *testing.T) {
	var old WorkflowStatus
	if err := json.Unmarshal([]byte(`{"phase":"reset_state","imported_rows":"0","completed_days":"0","blocked_days":"0"}`), &old); err != nil || old.Validate() != nil || old.Preparation != nil {
		t.Fatalf("legacy state rejected: %+v %v", old, err)
	}
	now := time.Now().UTC()
	old.Preparation = &WorkflowPreparationProgress{Phase: "reset_state", Table: "archive_log_state", ProcessedRows: 9007199254740993, LastBatchRows: 1000, CommittedBatches: 3, RecordedSince: now, LastCommittedAt: now}
	raw, err := json.Marshal(old)
	if err != nil || !strings.Contains(string(raw), `"processed_rows":"9007199254740993"`) {
		t.Fatalf("lost exact counter: %s %v", raw, err)
	}
	var got WorkflowStatus
	if err = json.Unmarshal(raw, &got); err != nil || got.Validate() != nil || got.Preparation.ProcessedRows != old.Preparation.ProcessedRows {
		t.Fatalf("progress round trip: %+v %v", got, err)
	}
	for _, mutate := range []func(*WorkflowPreparationProgress){
		func(p *WorkflowPreparationProgress) { p.Table = "logs_202609" },
		func(p *WorkflowPreparationProgress) { p.Phase = "unknown" },
		func(p *WorkflowPreparationProgress) { p.LastBatchRows = p.ProcessedRows + 1 },
		func(p *WorkflowPreparationProgress) { p.CommittedBatches = 0 },
		func(p *WorkflowPreparationProgress) { p.LastCommittedAt = now.Add(-time.Second) },
	} {
		p := *old.Preparation
		mutate(&p)
		if p.Validate() == nil {
			t.Fatalf("accepted invalid progress: %+v", p)
		}
	}
}

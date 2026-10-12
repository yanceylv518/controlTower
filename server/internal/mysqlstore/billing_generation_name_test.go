package mysqlstore

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"controltower/server/internal/billing"
)

// Invoked by the isolated upstream MySQL suite, before any source is configured.
func testUpstreamGenerationNames(t *testing.T, s Store) {
	ctx := context.Background()
	const site = "progress-name-test"
	result, err := s.db.ExecContext(ctx, `INSERT INTO billing_upstreams(instance_id,name,created_at,updated_at) VALUES(?,'趋境',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`, site)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
	target := billing.AutomaticTarget{InstanceID: site, Kind: "upstream_statement", SubjectID: id, From: from, To: from.AddDate(0, 1, 0)}
	p, err := s.BillingGenerationProgress(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if p.SubjectName != "趋境" || p.Checked != 0 {
		t.Fatalf("queued progress: %+v", p)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err = json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response["subject_name"] != "趋境" {
		t.Fatalf("missing API name: %s", raw)
	}

	if _, err = s.db.ExecContext(ctx, `UPDATE billing_upstreams SET archived=1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	p, err = s.BillingGenerationProgress(ctx, target)
	if err != nil || p.SubjectName != "趋境" {
		t.Fatalf("archived progress: %+v, %v", p, err)
	}
	target.InstanceID = "other-site"
	p, err = s.BillingGenerationProgress(ctx, target)
	if err != nil || p.SubjectName != "" {
		t.Fatalf("cross-site name leak: %+v, %v", p, err)
	}
	target.InstanceID, target.Kind = site, "user_statement"
	p, err = s.BillingGenerationProgress(ctx, target)
	if err != nil || p.SubjectName != "" {
		t.Fatalf("user/upstream ID collision: %+v, %v", p, err)
	}

	for _, savedName := range []string{"", "历史名称"} {
		taskID := "name-test:" + savedName
		frozen, _ := json.Marshal([]billing.GenerationProgress{{SubjectID: id, SubjectName: savedName, Outcome: "complete", TotalDays: 30, Checked: 30, Complete: 30, Processed: 123}})
		_, err = s.db.ExecContext(ctx, `INSERT INTO billing_generation_tasks(id,instance_id,kind,range_from,range_to,work_until,source,subject_ids_json,progress_json,created_at) VALUES(?,?,'upstream_statement',?,?,?,'manual',?,?,UTC_TIMESTAMP(6))`, taskID, site, from.UTC(), target.To.UTC(), target.To.UTC(), fmt.Sprintf("[%d]", id), string(frozen))
		if err != nil {
			t.Fatal(err)
		}
		task, e := s.BillingGenerationTask(ctx, site, taskID)
		if e != nil {
			t.Fatal(e)
		}
		want := savedName
		if want == "" {
			want = "趋境"
		}
		if len(task.Items) != 1 || task.Items[0].SubjectName != want || task.Items[0].Processed != 123 || task.Items[0].Complete != 30 {
			t.Fatalf("frozen task: %+v", task)
		}
		var stored string
		if err = s.db.QueryRowContext(ctx, `SELECT progress_json FROM billing_generation_tasks WHERE id=?`, taskID).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		var original []billing.GenerationProgress
		if err = json.Unmarshal([]byte(stored), &original); err != nil {
			t.Fatal(err)
		}
		if original[0].SubjectName != savedName {
			t.Fatal("rewrote historical progress")
		}
	}
}

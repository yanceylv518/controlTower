package logarchive

import (
	"context"
	af "controltower/internal/archivecontract"
	"encoding/json"
	"math"
	"sort"
	"time"
)

// Resolve cross-date repairs into one publication set. Bound the dependency
// read; an unusually large legacy graph is an explicit operational blocker.
func (w *Worker) workflowGroup(ctx context.Context, origin string) ([]string, error) {
	rows, err := w.target.QueryContext(ctx, `SELECT COALESCE(cohort_dates_json,affected_dates_json),CAST(JSON_UNQUOTE(JSON_EXTRACT(cursor_after_json,'$.catalog_revision')) AS UNSIGNED) FROM archive_batch_receipts WHERE JSON_LENGTH(COALESCE(cohort_dates_json,affected_dates_json))>1 LIMIT 4097`)
	if err != nil {
		return nil, err
	}
	type edge struct {
		dates    []string
		revision uint64
	}
	edges := []edge{}
	for rows.Next() {
		var e edge
		var raw []byte
		if err = rows.Scan(&raw, &e.revision); err != nil {
			rows.Close()
			return nil, err
		}
		if json.Unmarshal(raw, &e.dates) != nil {
			rows.Close()
			return nil, ErrWriterCheckpoint
		}
		edges = append(edges, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(edges) > 4096 {
		return nil, sealCode("cohort_incomplete")
	}
	selected := map[string]bool{origin: true}
	seen := map[string]bool{}
	minimum := uint64(math.MaxUint64)
	for {
		changed := false
		for date := range selected {
			if seen[date] {
				continue
			}
			seen[date] = true
			changed = true
			var revision uint64
			if err = w.target.QueryRowContext(ctx, `SELECT COALESCE(MAX(v.publish_revision),0) FROM archive_days d LEFT JOIN archive_day_versions v ON v.day_version_id=d.current_version_id WHERE d.log_date=?`, date).Scan(&revision); err != nil {
				return nil, err
			}
			if revision < minimum {
				minimum = revision
			}
		}
		for _, edge := range edges {
			if edge.revision <= minimum {
				continue
			}
			touches := false
			for _, date := range edge.dates {
				touches = touches || selected[date]
			}
			if touches {
				for _, date := range edge.dates {
					if !selected[date] {
						selected[date] = true
						changed = true
					}
				}
			}
		}
		if len(selected) > 31 {
			return nil, sealCode("cohort_incomplete")
		}
		if !changed {
			break
		}
	}
	result := make([]string, 0, len(selected))
	for date := range selected {
		result = append(result, date)
	}
	sort.Strings(result)
	return result, nil
}

func (w *Worker) advanceWorkflowGroup(ctx context.Context, g af.WriterGrant, s *workflowState) error {
	group, err := w.workflowGroup(ctx, s.Origin)
	if err != nil {
		if scanFailureCode(err) == "cohort_incomplete" {
			return w.finishWorkflowDay(ctx, g, s, "cohort_incomplete")
		}
		return err
	}
	s.Group = group
	cutoff := time.Now().Add(-w.delay).In(archiveLocation).Format("2006-01-02")
	for _, date := range group {
		if date >= cutoff {
			return w.finishWorkflowDay(ctx, g, s, "cohort_not_ended")
		}
		var revision uint64
		if err = w.target.QueryRowContext(ctx, `SELECT mutation_revision FROM archive_days WHERE log_date=?`, date).Scan(&revision); err != nil {
			return err
		}
		if value, ok := s.Verified[date]; ok && value == revision {
			continue
		}
		id, e := newArchiveID()
		if e != nil {
			return e
		}
		s.Date = date
		s.Scan = &af.BackfillTask{Identity: g.Identity, TaskID: id, Date: date, Type: "date_backfill", Attempt: 1, Policy: s.Verify.Policy}
		s.Phase = "backfill"
		return w.commitWorkflow(ctx, g, *s)
	}
	id, err := newArchiveID()
	if err != nil {
		return err
	}
	s.Seal = &af.SealTask{Identity: g.Identity, TaskID: id, Dates: group, Attempt: 1, Policy: s.Verify.Policy}
	s.Phase = "seal"
	return w.commitWorkflow(ctx, g, *s)
}

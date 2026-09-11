package mysqlstore

import (
	"context"
	cl "controltower/internal/containerlog"
	"database/sql"
	"encoding/json"
)

// Query metadata is bounded; log bodies are fetched only after an exact match.
// Never reuse another operator's result or an incomplete scan.
func (s Store) FindReusableContainerLog(ctx context.Context, instance, agent string, actor int64, q cl.Query) (cl.Task, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,query_json FROM container_log_tasks WHERE instance_id=? AND agent_id=? AND actor_id=? AND status='succeeded' AND created_at >= UTC_TIMESTAMP()-INTERVAL 1 DAY AND JSON_EXTRACT(result_json,'$.complete')=CAST('true' AS JSON) AND JSON_EXTRACT(result_json,'$.truncated')=CAST('false' AS JSON) ORDER BY created_at DESC LIMIT 1000`, instance, agent, actor)
	if err != nil {
		return cl.Task{}, err
	}
	defer rows.Close()
	normalize := func(v cl.Query) cl.Query {
		v.BatchID = ""
		v.Cursor = ""
		v.From = v.From.UTC()
		v.To = v.To.UTC()
		if v.Kind == "app" {
			v.Kind = ""
		}
		return v
	}
	for rows.Next() {
		var id, raw string
		if err = rows.Scan(&id, &raw); err != nil {
			return cl.Task{}, err
		}
		var previous cl.Query
		if err = json.Unmarshal([]byte(raw), &previous); err != nil {
			return cl.Task{}, err
		}
		// The previous snapshot must have been taken after the requested interval ended.
		if normalize(previous) == normalize(q) {
			rows.Close()
			task, e := s.GetContainerLog(ctx, id, actor)
			if e != nil {
				return cl.Task{}, e
			}
			if !task.CreatedAt.Before(q.To) && task.Result.Complete && !task.Result.Truncated && task.Result.Error == "" {
				return task, nil
			}
			return cl.Task{}, sql.ErrNoRows
		}
	}
	if err = rows.Err(); err != nil {
		return cl.Task{}, err
	}
	return cl.Task{}, sql.ErrNoRows
}

package mysqlstore

import (
	"context"
	cl "controltower/internal/containerlog"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Housekeeping retains query bodies/results for 24 hours; operation_audits retains metadata.
func (s Store) expireContainerLogs(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE container_log_tasks SET status='timed_out',result_json='{"status":"timed_out","lines":[],"error":"Agent 未在时限内返回结果"}' WHERE status='pending' AND created_at < UTC_TIMESTAMP() - INTERVAL 1 HOUR`); err != nil {
		return err
	}
	// A running task has usually uploaded partial batches already; losing the
	// lease must not throw those lines away. Only the status and notes change.
	if _, err := s.db.ExecContext(ctx, `UPDATE container_log_tasks SET status='timed_out',result_json=COALESCE(JSON_SET(result_json,'$.status','timed_out','$.complete',false,'$.truncated',true,'$.error','Agent 未在时限内返回进度','$.note','已返回内容仅为部分结果，请重新查询'),'{"status":"timed_out","lines":[],"error":"Agent 未在时限内返回进度"}') WHERE status='running' AND claimed_at < UTC_TIMESTAMP() - INTERVAL 90 SECOND`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE operation_audits a JOIN container_log_tasks t ON a.id=t.id SET a.status='timed_out' WHERE a.operation_type='logs.query' AND t.status='timed_out' AND a.status<>'timed_out'`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM container_log_tasks WHERE created_at < UTC_TIMESTAMP() - INTERVAL 1 DAY`)
	return err
}

func (s Store) ContainerLogTargets(ctx context.Context) ([]cl.Target, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instance_id,agent_id,containers_json,seen_at FROM container_log_targets WHERE seen_at > UTC_TIMESTAMP() - INTERVAL 1 MINUTE ORDER BY instance_id,agent_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cl.Target{}
	for rows.Next() {
		var t cl.Target
		var b string
		if err = rows.Scan(&t.InstanceID, &t.AgentID, &b, &t.SeenAt); err != nil {
			return nil, err
		}
		var inv cl.Inventory
		if json.Unmarshal([]byte(b), &inv) != nil {
			inv.Error = "日志读取服务需要升级"
		}
		t.Sources, t.DiscoveryError = inv.Sources, inv.Error
		for _, source := range inv.Sources {
			if source.Available {
				t.Containers = append(t.Containers, source.Container)
			}
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s Store) CreateContainerLog(ctx context.Context, t cl.Task) error {
	if err := s.expireContainerLogs(ctx); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Lock the target row to serialize submissions and enforce the bounded queue.
	var containers string
	if err = tx.QueryRowContext(ctx, `SELECT containers_json FROM container_log_targets WHERE instance_id=? AND agent_id=? AND seen_at > UTC_TIMESTAMP() - INTERVAL 1 MINUTE FOR UPDATE`, t.InstanceID, t.AgentID).Scan(&containers); err != nil {
		return errors.New("target unavailable")
	}
	var inv cl.Inventory
	if json.Unmarshal([]byte(containers), &inv) != nil {
		return errors.New("invalid target")
	}
	allowed := false
	for _, n := range inv.Sources {
		if n.Available && cl.ValidSourceID(n.ID) && n.Container == t.Query.Container && n.ID == t.Query.SourceID {
			allowed = true
		}
	}
	if !allowed {
		return errors.New("container unavailable")
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM container_log_tasks WHERE instance_id=? AND agent_id=? AND status IN ('pending','running')`, t.InstanceID, t.AgentID).Scan(&count); err != nil {
		return err
	}
	if count >= 5 {
		return errors.New("queue full")
	}
	b, _ := json.Marshal(t.Query)
	_, err = tx.ExecContext(ctx, `INSERT INTO container_log_tasks(id,instance_id,agent_id,actor_id,actor,actor_name,query_json,status,result_json,created_at) VALUES(?,?,?,?,?,?,?,'pending','{"status":"pending","lines":[]}',?)`, t.ID, t.InstanceID, t.AgentID, t.ActorID, t.Actor, t.ActorName, string(b), t.CreatedAt)
	if err != nil {
		return err
	}
	// Audit contains identity and query conditions, never returned log content.
	metadata, _ := json.Marshal(map[string]any{"actor_user_id": t.ActorID, "actor_name": t.ActorName, "agent_id": t.AgentID, "query": t.Query})
	_, err = tx.ExecContext(ctx, `INSERT INTO operation_audits(id,instance_id,operation_type,target_type,target_id,actor_id,before_summary,after_summary,status,created_at) VALUES(?,?,'logs.query','container_log_task',?,?,'',?,'submitted',?)`, t.ID, t.InstanceID, t.ID, t.Actor, string(metadata), t.CreatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func scanContainerLog(row interface{ Scan(...any) error }) (cl.Task, error) {
	var t cl.Task
	var q, r string
	err := row.Scan(&t.ID, &t.InstanceID, &t.AgentID, &t.ActorID, &t.Actor, &t.ActorName, &q, &r, &t.CreatedAt)
	if err == nil {
		err = json.Unmarshal([]byte(q), &t.Query)
	}
	if err == nil {
		err = json.Unmarshal([]byte(r), &t.Result)
	}
	return t, err
}

const containerLogColumns = `id,instance_id,agent_id,actor_id,actor,actor_name,query_json,result_json,created_at`

func (s Store) ListContainerLogs(ctx context.Context, actor int64) ([]cl.Task, error) {
	if err := s.expireContainerLogs(ctx); err != nil {
		return nil, err
	}
	// Task history omits result bodies; details are fetched separately.
	rows, err := s.db.QueryContext(ctx, `SELECT id,instance_id,agent_id,actor_id,actor,actor_name,query_json,JSON_OBJECT('status',status,'lines',JSON_ARRAY()),created_at FROM container_log_tasks WHERE actor_id=? ORDER BY created_at DESC LIMIT 50`, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []cl.Task{}
	for rows.Next() {
		t, e := scanContainerLog(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s Store) GetContainerLog(ctx context.Context, id string, actor int64) (cl.Task, error) {
	if err := s.expireContainerLogs(ctx); err != nil {
		return cl.Task{}, err
	}
	return scanContainerLog(s.db.QueryRowContext(ctx, `SELECT `+containerLogColumns+` FROM container_log_tasks WHERE id=? AND actor_id=?`, id, actor))
}
func (s Store) PollContainerLogs(ctx context.Context, instance string, p cl.Poll) (*cl.Task, error) {
	if err := s.expireContainerLogs(ctx); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	names, _ := json.Marshal(cl.Inventory{Sources: p.Sources, Error: p.DiscoveryError})
	_, err = tx.ExecContext(ctx, `INSERT INTO container_log_targets(instance_id,agent_id,containers_json,seen_at) VALUES(?,?,?,UTC_TIMESTAMP()) ON DUPLICATE KEY UPDATE containers_json=VALUES(containers_json),seen_at=VALUES(seen_at)`, instance, p.AgentID, string(names))
	if err != nil {
		return nil, err
	}
	if p.Result != nil {
		b, _ := json.Marshal(p.Result)
		result, e := tx.ExecContext(ctx, `UPDATE container_log_tasks SET status=?,result_json=?,claimed_at=IF(?='running',UTC_TIMESTAMP(),claimed_at) WHERE id=? AND instance_id=? AND agent_id=? AND status='running'`, p.Result.Status, string(b), p.Result.Status, p.TaskID, instance, p.AgentID)
		if e != nil {
			return nil, e
		}
		if n, _ := result.RowsAffected(); n > 0 {
			if _, err = tx.ExecContext(ctx, `UPDATE operation_audits SET status=? WHERE id=? AND operation_type='logs.query'`, p.Result.Status, p.TaskID); err != nil {
				return nil, err
			}
		}
	}
	var running int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM container_log_tasks WHERE instance_id=? AND agent_id=? AND status='running'`, instance, p.AgentID).Scan(&running); err != nil {
		return nil, err
	}
	if running > 0 {
		if p.Result != nil && p.Result.Status == "running" {
			task, e := scanContainerLog(tx.QueryRowContext(ctx, `SELECT `+containerLogColumns+` FROM container_log_tasks WHERE id=? AND instance_id=? AND agent_id=? AND status='running'`, p.TaskID, instance, p.AgentID))
			if e == nil {
				if err := tx.Commit(); err != nil {
					return nil, err
				}
				return &task, nil
			}
			if !errors.Is(e, sql.ErrNoRows) {
				return nil, e
			}
		}
		return nil, tx.Commit()
	}
	t, err := scanContainerLog(tx.QueryRowContext(ctx, `SELECT `+containerLogColumns+` FROM container_log_tasks WHERE instance_id=? AND agent_id=? AND status='pending' ORDER BY created_at LIMIT 1 FOR UPDATE`, instance, p.AgentID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	t.Result = cl.Result{Status: "running", Lines: []string{}}
	b, _ := json.Marshal(t.Result)
	_, err = tx.ExecContext(ctx, `UPDATE container_log_tasks SET status='running',result_json=?,claimed_at=? WHERE id=?`, string(b), time.Now().UTC(), t.ID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &t, nil
}

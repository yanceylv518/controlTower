ALTER TABLE container_log_tasks
 ADD COLUMN history_batch VARCHAR(128) GENERATED ALWAYS AS (COALESCE(JSON_UNQUOTE(JSON_EXTRACT(query_json,'$.batch_id')),'')) STORED,
 ADD INDEX idx_clog_history_batch(history_batch,actor_id,created_at);

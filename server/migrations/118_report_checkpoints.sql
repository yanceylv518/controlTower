CREATE TABLE IF NOT EXISTS settlement_report_checkpoints (
 task_id VARCHAR(64) NOT NULL,
 bill_day DATE NOT NULL,
 processed BIGINT NOT NULL DEFAULT 0,
 payload LONGBLOB NOT NULL,
 updated_at DATETIME(6) NOT NULL,
 PRIMARY KEY(task_id,bill_day),
 FOREIGN KEY(task_id,bill_day) REFERENCES settlement_report_task_days(task_id,bill_day) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

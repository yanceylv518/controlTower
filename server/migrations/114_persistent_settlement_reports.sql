CREATE TABLE IF NOT EXISTS settlement_report_days (
 instance_id VARCHAR(191) NOT NULL, bill_day DATE NOT NULL, payload LONGBLOB NOT NULL, task_id VARCHAR(64) NOT NULL, updated_at DATETIME(6) NOT NULL,
 PRIMARY KEY(instance_id,bill_day)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE IF NOT EXISTS settlement_report_tasks (
 id VARCHAR(64) NOT NULL PRIMARY KEY, instance_id VARCHAR(191) NOT NULL, range_from DATE NOT NULL, range_to DATE NOT NULL,
 status VARCHAR(24) NOT NULL, overwrite_existing BOOLEAN NOT NULL DEFAULT FALSE, automatic BOOLEAN NOT NULL DEFAULT FALSE,
 created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL,
 KEY report_site_tasks(instance_id,created_at), KEY report_pending(status,created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE IF NOT EXISTS settlement_report_task_days (
 task_id VARCHAR(64) NOT NULL, bill_day DATE NOT NULL, status VARCHAR(24) NOT NULL, processed BIGINT NOT NULL DEFAULT 0,
 error_message VARCHAR(512) NOT NULL DEFAULT '', updated_at DATETIME(6) NOT NULL,
 PRIMARY KEY(task_id,bill_day), FOREIGN KEY(task_id) REFERENCES settlement_report_tasks(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

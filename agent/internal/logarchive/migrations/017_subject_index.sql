CREATE TABLE IF NOT EXISTS archive_subject_index (
  subject_type VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  subject_id BIGINT NOT NULL,
  parent_user_id BIGINT NOT NULL,
  name_snapshot VARCHAR(255) NULL,
  first_log_date DATE NOT NULL,
  last_log_date DATE NOT NULL,
  day_version_id BINARY(16) NOT NULL,
  PRIMARY KEY (subject_type, subject_id, parent_user_id, day_version_id),
  KEY idx_archive_subject_version (day_version_id, subject_type, subject_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin

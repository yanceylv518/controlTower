ALTER TABLE billing_generation_ranges
 ADD COLUMN cancelled TINYINT(1) NOT NULL DEFAULT 0,
 ADD COLUMN overwrite_existing TINYINT(1) NOT NULL DEFAULT 0,
 ADD COLUMN generation_started_at DATETIME(6) NULL;

-- Version marker makes timezone conversion safe after an interrupted migration.
ALTER TABLE billing_discount_rules ADD COLUMN time_version TINYINT NOT NULL DEFAULT 0;
ALTER TABLE billing_discount_rules MODIFY effective_from DATETIME(6) NOT NULL, MODIFY effective_to DATETIME(6) NULL;
UPDATE billing_discount_rules SET effective_from=DATE_SUB(effective_from,INTERVAL 8 HOUR),effective_to=DATE_SUB(effective_to,INTERVAL 8 HOUR),time_version=1 WHERE time_version=0;
ALTER TABLE billing_discount_rules ALTER time_version SET DEFAULT 1;
ALTER TABLE billing_statement_discount_snapshots ADD COLUMN time_version TINYINT NOT NULL DEFAULT 0;
ALTER TABLE billing_statement_discount_snapshots MODIFY effective_from DATETIME(6) NOT NULL, MODIFY effective_to DATETIME(6) NULL;
UPDATE billing_statement_discount_snapshots SET effective_from=DATE_SUB(effective_from,INTERVAL 8 HOUR),effective_to=DATE_SUB(effective_to,INTERVAL 8 HOUR),time_version=1 WHERE time_version=0;
ALTER TABLE billing_statement_discount_snapshots ALTER time_version SET DEFAULT 1;

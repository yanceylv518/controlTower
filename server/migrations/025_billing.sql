CREATE TABLE IF NOT EXISTS billing_daily (
  instance_id VARCHAR(64) NOT NULL,
  user_id BIGINT NOT NULL,
  username VARCHAR(128) NOT NULL DEFAULT '',
  model_name VARCHAR(255) NOT NULL,
  group_name VARCHAR(64) NOT NULL DEFAULT '',
  tier_from BIGINT NOT NULL DEFAULT 0,
  day DATE NOT NULL,
  request_count BIGINT NOT NULL DEFAULT 0,
  prompt_tokens BIGINT NOT NULL DEFAULT 0,
  completion_tokens BIGINT NOT NULL DEFAULT 0,
  cache_tokens BIGINT NOT NULL DEFAULT 0,
  quota BIGINT NOT NULL DEFAULT 0,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (instance_id, user_id, model_name, group_name, tier_from, day),
  KEY idx_billing_daily_day (instance_id, day)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS billing_prices (
  instance_id VARCHAR(64) NOT NULL,
  model_name VARCHAR(255) NOT NULL,
  effective_from DATE NOT NULL,
  tier_from BIGINT NOT NULL DEFAULT 0,
  input_price DECIMAL(12,6) NOT NULL DEFAULT 0,
  output_price DECIMAL(12,6) NOT NULL DEFAULT 0,
  cache_price DECIMAL(12,6) NOT NULL DEFAULT 0,
  updated_at DATETIME(6) NOT NULL,
  updated_by VARCHAR(128) NOT NULL DEFAULT '',
  PRIMARY KEY (instance_id, model_name, effective_from, tier_from)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS billing_group_ratios (
  instance_id VARCHAR(64) NOT NULL,
  group_name VARCHAR(64) NOT NULL,
  ratio DECIMAL(8,4) NOT NULL DEFAULT 1,
  updated_at DATETIME(6) NOT NULL,
  updated_by VARCHAR(128) NOT NULL DEFAULT '',
  PRIMARY KEY (instance_id, group_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS billing_ratio_snapshot (
  instance_id VARCHAR(64) NOT NULL,
  day DATE NOT NULL,
  ratios_json MEDIUMTEXT NOT NULL,
  PRIMARY KEY (instance_id, day)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS billing_balance_snapshot (
  instance_id VARCHAR(64) NOT NULL,
  user_id BIGINT NOT NULL,
  day DATE NOT NULL,
  balance BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (instance_id, user_id, day)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

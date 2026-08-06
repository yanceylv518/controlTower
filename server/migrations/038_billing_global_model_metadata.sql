CREATE TABLE IF NOT EXISTS billing_global_model_metadata (
  model_name VARCHAR(255) NOT NULL,
  max_context_tokens BIGINT NOT NULL DEFAULT 0,
  updated_at DATETIME(6) NOT NULL,
  updated_by VARCHAR(128) NOT NULL DEFAULT '',
  PRIMARY KEY (model_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT IGNORE INTO billing_global_model_metadata(model_name,max_context_tokens,updated_at,updated_by)
SELECT model_name,max_context_tokens,updated_at,updated_by
FROM (
  SELECT model_name,max_context_tokens,updated_at,updated_by,
         ROW_NUMBER() OVER (PARTITION BY model_name ORDER BY updated_at DESC,instance_id) AS rn
  FROM billing_model_metadata
  WHERE max_context_tokens > 0
) ranked
WHERE rn=1;

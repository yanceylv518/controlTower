# Control Tower Schema

Control Tower owns its database. It must not write to the new-api database.

## Storage Rules

- `log_events` stores sanitized log summaries only.
- Full request bodies are not stored.
- Full response bodies are not stored.
- Full `logs.other` payloads are not stored.
- Notification webhook values are stored in the Control Tower database, but must not be printed in logs or returned to frontend clients as plaintext.
- Every management action must write an operation audit record.

## Migration

Initial schema draft:

- `server/migrations/001_init.sql`

The SQL intentionally avoids `SERIAL`, `AUTO_INCREMENT`, and `JSONB` so PostgreSQL/MySQL compatibility stays possible. Application code should generate string IDs for tables that need standalone IDs.


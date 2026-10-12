package mysqlstore

import (
	"context"
	"database/sql"

	"controltower/server/internal/billing"
)

// Progress must resolve names independently of the browser's selection cache.
// Archived upstreams remain addressable so historical tasks keep their identity.
func (s Store) fillBillingGenerationName(ctx context.Context, site, kind string, p *billing.GenerationProgress) error {
	if kind != "upstream_statement" || p.SubjectName != "" {
		return nil
	}
	err := s.db.QueryRowContext(ctx, `SELECT name FROM billing_upstreams WHERE instance_id=? AND id=?`, site, p.SubjectID).Scan(&p.SubjectName)
	if err == sql.ErrNoRows {
		return nil
	}
	return err
}

package logarchive

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"

	af "controltower/internal/archivecontract"
)

// FoundationIdentity discovers an existing binding without inventing a new
// identity on read errors. Preparation verifies its source and schema hashes.
func (w *Worker) FoundationIdentity(ctx context.Context) (*af.Identity, error) {
	exists, err := w.HasFoundation(ctx)
	if err != nil || !exists {
		return nil, err
	}
	var i af.Identity
	var dataset, generation []byte
	err = w.target.QueryRowContext(ctx, `SELECT site_id,dataset_id,source_generation_id FROM archive_dataset_meta WHERE singleton_id=1`).Scan(&i.SiteID, &dataset, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, ErrFoundationSchema
	}
	i.DatasetID = hex.EncodeToString(dataset)
	i.SourceGenerationID = hex.EncodeToString(generation)
	if i.Validate() != nil {
		return nil, ErrFoundationIdentity
	}
	return &i, nil
}

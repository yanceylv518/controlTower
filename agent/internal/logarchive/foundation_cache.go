package logarchive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	af "controltower/internal/archivecontract"
)

func (w *Worker) cachedFingerprints(ctx context.Context) (foundationFingerprint, error) {
	var fp foundationFingerprint
	var uuid, database string
	if err := w.source.QueryRowContext(ctx, "SELECT @@server_uuid, DATABASE()").Scan(&uuid, &database); err != nil || uuid == "" || database == "" {
		return fp, errors.New("archive source identity check failed")
	}
	raw, _ := json.Marshal([]string{uuid, database})
	fp.source = sha256.Sum256(raw)
	hash, err := hex.DecodeString(w.foundationCache.info.SchemaFingerprint)
	if err != nil || len(hash) != 32 {
		return fp, ErrFoundationSchema
	}
	copy(fp.schema[:], hash)
	return fp, nil
}

// Full column/index inspection is expensive and is serialized by the worker.
// The bound fingerprints and migration receipt set remain cheap cache keys.
// The TTL also detects out-of-band source/target DDL without a version bump.
type foundationCache struct {
	mu      sync.Mutex
	info    FoundationInfo
	expires time.Time
}

func (w *Worker) invalidateFoundationCache() {
	w.foundationCache.mu.Lock()
	w.foundationCache.expires = time.Time{}
	w.foundationCache.mu.Unlock()
}

func (w *Worker) inspectFoundationCached(ctx context.Context, identity af.Identity) (FoundationInfo, error) {
	c := &w.foundationCache
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.expires) && c.info.Identity.Equal(identity) {
		migrations, err := loadFoundationMigrations()
		if err == nil {
			applied, e := foundationMigrationHistory(ctx, w.target, migrations)
			if e == nil && len(applied) == len(migrations) {
				fp, e := w.cachedFingerprints(ctx)
				if e == nil {
					info, e := readFoundation(ctx, w.target, identity, fp)
					if e == nil && info.SchemaFingerprint == c.info.SchemaFingerprint && info.SourceFingerprint == c.info.SourceFingerprint {
						return info, nil
					}
				}
			}
		}
	}
	c.expires = time.Time{}
	info, err := w.inspectFoundationUncached(ctx, identity)
	if err == nil {
		c.info = info
		c.expires = time.Now().Add(30 * time.Second)
	}
	return info, err
}

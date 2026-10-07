package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"
)

// ForGeneration scopes claim/recovery queries; all other storage capabilities
// remain available to the runner (including its optional snapshot interfaces).
func (s Store) ForGeneration(site, job string) Store {
	s.generationSite, s.generationJob = site, job
	return s
}

func (s Store) GenerationSites(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instance_id FROM billing_jobs WHERE status IN ('pending','running','publishing') UNION SELECT instance_id FROM settlement_report_tasks WHERE status IN ('pending','running') ORDER BY instance_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sites := []string{}
	for rows.Next() {
		var site string
		if err = rows.Scan(&site); err != nil {
			return nil, err
		}
		sites = append(sites, site)
	}
	return sites, rows.Err()
}

func (s Store) NextGeneration(ctx context.Context, site string) (billing.QueuedGeneration, error) {
	var task billing.QueuedGeneration
	// A running whole task survives new arrivals and restart. At idle, billing
	// takes priority over pending reports. The caller owns the site lock.
	err := s.db.QueryRowContext(ctx, `SELECT kind,id,status FROM (
SELECT 'billing' kind,id,status,IF(status IN ('running','publishing'),0,1) priority,created_at FROM billing_jobs WHERE instance_id=? AND status IN ('pending','running','publishing')
UNION ALL SELECT 'report' kind,id,status,IF(status='running',0,2) priority,created_at FROM settlement_report_tasks WHERE instance_id=? AND status IN ('pending','running')
) queue ORDER BY priority,created_at,id LIMIT 1`, site, site).Scan(&task.Kind, &task.ID, &task.Status)
	return task, err
}

// Only exposes the type of work blocking this site, never another site's tasks.
func (s Store) GenerationWaitingFor(ctx context.Context, site, kind string) (string, error) {
	task, err := s.NextGeneration(ctx, site)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if kind == "billing" && task.Kind == "report" && task.Status == "running" {
		return "report", nil
	}
	if kind == "report" && task.Kind == "billing" {
		return "billing", nil
	}
	return "", nil
}

func generationSiteLockKey(site string) string {
	return fmt.Sprintf("ct:generation:%x", sha256.Sum256([]byte(site)))[:64]
}

func (s Store) LockGenerationSite(ctx context.Context, site string) (context.Context, func(), error) {
	if site == "" {
		return nil, nil, fmt.Errorf("generation site required")
	}
	key := generationSiteLockKey(site)
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	var locked int
	if err = conn.QueryRowContext(ctx, `SELECT GET_LOCK(?,0)`, key).Scan(&locked); err != nil || locked != 1 {
		if err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		conn.Close()
		return nil, nil, billing.ErrGenerationInProgress
	}
	lease, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-lease.Done():
				return
			case <-ticker.C:
				check, stop := context.WithTimeout(lease, 2*time.Second)
				var owns int
				err := conn.QueryRowContext(check, `SELECT COALESCE(IS_USED_LOCK(?)=CONNECTION_ID(),0)`, key).Scan(&owns)
				stop()
				if err != nil || owns != 1 {
					cancel()
					return
				}
			}
		}
	}()
	return lease, func() {
		cancel()
		<-done
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := conn.ExecContext(cleanup, `DO RELEASE_LOCK(?)`, key); err != nil {
			// Never put a connection that may still own the named lock back
			// into the pool after a failed release.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		conn.Close()
	}, nil
}

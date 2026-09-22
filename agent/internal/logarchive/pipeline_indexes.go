package logarchive

import (
	"context"
	af "controltower/internal/archivecontract"
	"errors"
	"github.com/go-sql-driver/mysql"
	"time"
)

type pipelineIndexBuild struct {
	table  string
	done   chan error
	cancel context.CancelFunc
}

// At most one online index build per migration pass. Provision the template
// first so newly created monthly tables inherit the date access path.
func (w *Worker) preparePipelineIndex(ctx context.Context, g af.WriterGrant) (bool, error) {
	m, err := readWriterMeta(ctx, w.target, g.Identity, false)
	if err != nil {
		return false, err
	}
	if err = requireWriter(m, g); err != nil {
		return false, err
	}
	if build := w.indexBuild; build != nil {
		reportOperation(ctx, "prepare_date_index", "archive", build.table)
		select {
		case e := <-build.done:
			build.cancel()
			w.indexBuild = nil
			// Another fenced executor may have completed the same additive DDL.
			var dbErr *mysql.MySQLError
			if e != nil {
				if !(errors.As(e, &dbErr) && dbErr.Number == 1061) {
					return false, e
				}
				var count int
				if checkErr := w.target.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME='created_at' AND SEQ_IN_INDEX=1 AND SUB_PART IS NULL AND INDEX_TYPE='BTREE'`, build.table).Scan(&count); checkErr != nil {
					return false, checkErr
				}
				if count == 0 {
					return false, e
				}
			}
			if cache := w.rawInventory.Months[build.table]; cache != nil {
				cache.Checked = time.Time{}
			}
		default:
			return false, nil
		}
	}
	rows, err := w.target.QueryContext(ctx, `SELECT t.TABLE_NAME FROM information_schema.TABLES t WHERE t.TABLE_SCHEMA=DATABASE() AND (t.TABLE_NAME='logs' OR t.TABLE_NAME REGEXP '^logs_[0-9]{6}$') AND NOT EXISTS (SELECT 1 FROM information_schema.STATISTICS s WHERE s.TABLE_SCHEMA=t.TABLE_SCHEMA AND s.TABLE_NAME=t.TABLE_NAME AND s.COLUMN_NAME='created_at' AND s.SEQ_IN_INDEX=1 AND s.SUB_PART IS NULL AND s.INDEX_TYPE='BTREE') ORDER BY t.TABLE_NAME LIMIT 1`)
	if err != nil {
		return false, err
	}
	var table string
	if rows.Next() {
		err = rows.Scan(&table)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return false, err
	}
	if table == "" {
		return true, nil
	}
	if table != "logs" && !writerMonthlyName.MatchString(table) {
		return false, ErrFoundationSchema
	}
	reportOperation(ctx, "prepare_date_index", "archive", table)
	// This additive online DDL has no row side effects and can finish after a
	// pause. Do not restart it on every 30-second data-batch timeout. A new DDL
	// requires a fresh grant above; log writes always retain transactional fencing.
	buildCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	build := &pipelineIndexBuild{table: table, done: make(chan error, 1), cancel: cancel}
	w.indexBuild = build
	db := w.maintenance
	if db == nil {
		db = w.target
	}
	go func() {
		_, e := db.ExecContext(buildCtx, "ALTER TABLE "+quote(table)+" ADD INDEX idx_archive_created_id (created_at,id), ALGORITHM=INPLACE, LOCK=NONE")
		build.done <- e
	}()
	return false, nil
}

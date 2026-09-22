package logarchive

import (
	"context"
	af "controltower/internal/archivecontract"
	ap "controltower/internal/archivepipeline"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type pipelineCheckpoint struct {
	CollectionScan    *af.BackfillTask        `json:"collection_scan,omitempty"`
	CollectionDate    string                  `json:"collection_date,omitempty"`
	CollectionDone    bool                    `json:"collection_done"`
	Progress          map[ap.Task]ap.Progress `json:"progress,omitempty"`
	State             ap.State                `json:"state"`
	Epoch             uint64                  `json:"epoch,string"`
	AfterID           int64                   `json:"after_id,string"`
	AfterCreated      int64                   `json:"after_created,string"`
	Errors            map[ap.Task]string      `json:"errors"`
	VerificationToken uint64                  `json:"verification_token,string"`
}

func loadPipeline(ctx context.Context, tx *sql.Tx) (pipelineCheckpoint, error) {
	var p pipelineCheckpoint
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT state_json FROM archive_pipeline WHERE singleton_id=1 FOR UPDATE`).Scan(&raw)
	if err != nil {
		return p, err
	}
	if json.Unmarshal(raw, &p) != nil || p.State.Validate() != nil || p.AfterID < 0 || p.AfterCreated < 0 {
		return p, ErrWriterCheckpoint
	}
	if p.Progress == nil {
		p.Progress = map[ap.Task]ap.Progress{}
	}
	if p.Errors == nil {
		p.Errors = map[ap.Task]string{}
	}
	return p, nil
}
func savePipeline(ctx context.Context, tx *sql.Tx, p pipelineCheckpoint) error {
	if err := p.State.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO archive_pipeline(singleton_id,state_json,writer_epoch,updated_at) VALUES(1,?,?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE state_json=VALUES(state_json),writer_epoch=VALUES(writer_epoch),updated_at=VALUES(updated_at)`, string(raw), p.Epoch)
	return err
}
func (w *Worker) pipelineTransaction(ctx context.Context, g af.WriterGrant, fn func(*sql.Tx, *pipelineCheckpoint) error) error {
	tx, err := w.target.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	m, err := readWriterMeta(ctx, tx, g.Identity, true)
	if err != nil {
		return err
	}
	if err = requireWriter(m, g); err != nil {
		return err
	}
	p, err := loadPipeline(ctx, tx)
	if err != nil {
		return err
	}
	p.Epoch = g.WriterEpoch
	if err = fn(tx, &p); err != nil {
		return err
	}
	if err = savePipeline(ctx, tx, p); err != nil {
		return err
	}
	if err = writerGuardBeforeCommit(ctx, tx, g); err != nil {
		return err
	}
	return tx.Commit()
}

// Called inside every raw/statistical writer transaction, before publication of
// its receipt. No raw change can leave a current successful date result behind.
func syncPipelineMutation(ctx context.Context, tx *sql.Tx, dates []string, mutated map[string]bool, verification bool, rawRows int, afterID int64) error {
	p, err := loadPipeline(ctx, tx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	organized := map[string]bool{}
	for date, d := range p.State.Days {
		organized[date] = d.OrganizedRevision == d.Revision
	}
	if err = p.State.ObserveCommittedRows(dates, mutated); err != nil {
		return err
	}
	for _, date := range dates {
		var revision uint64
		if err = tx.QueryRowContext(ctx, `SELECT mutation_revision FROM archive_days WHERE log_date=?`, date).Scan(&revision); err != nil {
			return err
		}
		d := p.State.Days[date]
		if revision == 0 {
			revision = 1
		}
		d.Revision = revision
		if mutated[date] && verification && organized[date] {
			d.OrganizedRevision = revision
		}
		if work, ok := p.State.Active[ap.Verification]; ok && work.Date == date && verification {
			work.Revision = revision
			p.State.Active[ap.Verification] = work
		}
	}
	if !verification && rawRows > 0 {
		progress := p.Progress[ap.Collection]
		progress.Rows += uint64(rawRows)
		progress.AfterID = afterID
		progress.UpdatedAt = time.Now().UTC()
		p.Progress[ap.Collection] = progress
	}
	return savePipeline(ctx, tx, p)
}

func (w *Worker) initializePipeline(ctx context.Context, g af.WriterGrant, settings ap.Settings) error {
	tx, err := w.target.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	m, err := readWriterMeta(ctx, tx, g.Identity, true)
	if err != nil {
		return err
	}
	if err = requireWriter(m, g); err != nil {
		return err
	}
	p, err := loadPipeline(ctx, tx)
	if errors.Is(err, sql.ErrNoRows) {
		old, e := loadWorkflow(ctx, tx)
		if e != nil {
			return e
		}
		if old.Phase == "backfill" || old.Phase == "verify" || old.Phase == "seal" {
			return &scanError{code: "pipeline_wait_existing_date"}
		}
		p = pipelineCheckpoint{State: ap.New(settings), Epoch: g.WriterEpoch, Errors: map[ap.Task]string{}}
		p.State.SchemaReady = true
		// The new migration also provisions date indexes. The old phase is kept:
		// imports already in progress must never restart legacy cleanup.
		p.State.MigrationDone = false
		if e = p.State.BeginRound(time.Now(), archiveLocation); e != nil {
			return e
		}
		// An explicit new independent stream starts at zero, never at MAX(id).
		if _, e = tx.ExecContext(ctx, `INSERT INTO archive_checkpoints(stream_key,stream_type,after_id,cursor_version,updated_at) VALUES('incremental','incremental',0,1,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE stream_key=VALUES(stream_key)`); e != nil {
			return e
		}
	} else if err != nil {
		return err
	}
	if p.Progress == nil {
		p.Progress = map[ap.Task]ap.Progress{}
	}
	if !p.State.Settings.SameRange(settings) {
		p.CollectionScan = nil
		p.CollectionDate = ""
		p.CollectionDone = false
	}
	if p.State.Settings.RetryToken != settings.RetryToken {
		for _, day := range p.State.Days {
			if day.Result != nil && day.Result.Code != "" {
				day.Result = nil
			}
		}
		p.Errors = map[ap.Task]string{}
		for task, progress := range p.Progress {
			progress.Diagnostic = nil
			p.Progress[task] = progress
		}
	}
	p.State.Settings = settings
	p.Epoch = g.WriterEpoch
	// Existing monthly rows supply the boundary signal even before statistics
	// exist. Only successful nonzero exact counts establish an observed date.
	var observed []string
	for _, month := range w.rawInventory.Months {
		for date, count := range month.Days {
			if count.Rows != nil && *count.Rows != "0" && count.ErrorCode == "" {
				observed = append(observed, date)
			}
		}
	}
	if err = p.State.ObserveCommittedRows(observed, nil); err != nil {
		return err
	}
	for _, date := range observed {
		if _, err = tx.ExecContext(ctx, `INSERT IGNORE INTO archive_days(log_date,mutation_revision,state,updated_at) VALUES(?,1,'dirty',UTC_TIMESTAMP(6))`, date); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT DATE_FORMAT(log_date,'%Y-%m-%d'),mutation_revision FROM archive_days ORDER BY log_date`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var date string
		var rev uint64
		if err = rows.Scan(&date, &rev); err != nil {
			rows.Close()
			return err
		}
		if rev == 0 {
			rev = 1
		}
		d := p.State.Days[date]
		if d == nil {
			d = &ap.Day{}
			p.State.Days[date] = d
		}
		if d.Revision != rev {
			d.OrganizedRevision = 0
			d.Result = nil
		}
		d.Revision = rev
		d.Collected = date < p.State.Frontier
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if p.State.NextDate() == "" && len(p.State.Active) == 0 && p.State.Cutoff < time.Now().In(archiveLocation).AddDate(0, 0, -1).Format("2006-01-02") {
		if err = p.State.BeginRound(time.Now(), archiveLocation); err != nil {
			return err
		}
	}
	if err = savePipeline(ctx, tx, p); err != nil {
		return err
	}
	if err = writerGuardBeforeCommit(ctx, tx, g); err != nil {
		return err
	}
	return tx.Commit()
}

// Read-only refresh is available while all tasks are paused and after restart.
func (w *Worker) PipelineProgress(ctx context.Context) (*ap.Status, error) {
	var raw []byte
	err := w.target.QueryRowContext(ctx, `SELECT state_json FROM archive_pipeline WHERE singleton_id=1`).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p pipelineCheckpoint
	if json.Unmarshal(raw, &p) != nil || p.State.Validate() != nil {
		return nil, ErrWriterCheckpoint
	}
	return &ap.Status{MigrationDone: p.State.MigrationDone, Cutoff: p.State.Cutoff, Settings: p.State.Settings, Active: p.State.Active, Errors: p.Errors, Progress: p.Progress, CollectionDate: p.CollectionDate, CollectionDone: p.CollectionDone}, nil
}

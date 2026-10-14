package billing

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (g UserDailyFileGenerator) generateZeroOutputFile(ctx context.Context, job Job) error {
	if g.Spool == nil {
		return fmt.Errorf("zero-output spool unavailable")
	}
	stepsStore, ok := g.Store.(interface {
		ListBillingJobSteps(context.Context, string) ([]JobStep, error)
	})
	if !ok {
		return fmt.Errorf("zero-output step store unavailable")
	}
	steps, err := stepsStore.ListBillingJobSteps(ctx, job.ID)
	if err != nil {
		return err
	}
	var expected int64
	for _, step := range steps {
		if step.Status != "complete" {
			return fmt.Errorf("zero-output step incomplete")
		}
		expected += step.ProcessedRows
	}
	if expected == 0 {
		return fmt.Errorf("zero-output task has no processed rows")
	}
	// Claim queries carry execution fields; restore the captured subject identity.
	if store, ok := g.Store.(interface {
		BillingJob(context.Context, string) (Job, error)
	}); ok {
		full, err := store.BillingJob(ctx, job.ID)
		if err != nil {
			return err
		}
		job = full
	}
	root := g.Root
	if root == "" {
		root = DefaultBillingFileRoot
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	group := UserDailyFile{JobID: job.ID, InstanceID: job.InstanceID, BillDay: dateOnly(job.From), UserID: job.UserID}
	relative := strings.TrimSuffix(dailyFileRelativePath(job, group), ".xlsx") + "-zero-output.xlsx"
	target := filepath.Join(root, relative)
	if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	pages, err := g.Spool.OpenPages(ctx, job)
	if err != nil {
		return err
	}
	if len(pages) == 0 {
		return fmt.Errorf("zero-output detail pages missing")
	}
	f, err := os.CreateTemp(filepath.Dir(target), ".zero-output-*.xlsx")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	var written int64
	hash := sha256.New()
	err = WriteZeroOutputWorkbook(ctx, io.MultiWriter(f, hash), job, func(visit func(RequestDetail) error) error {
		for _, page := range pages {
			if err := page.Read(func(row RequestDetail) error { written++; return visit(row) }); err != nil {
				return err
			}
		}
		if written != expected {
			return fmt.Errorf("zero-output detail count mismatch: got %d, expected %d", written, expected)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), target); err != nil {
		return err
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	digest := fmt.Sprintf("%x", hash.Sum(nil))
	group.RelativePath, group.FileSize, group.SHA256, group.CreatedAt = filepath.ToSlash(relative), info.Size(), digest, time.Now().UTC()
	return g.Store.PutBillingUserDailyFile(ctx, group)
}

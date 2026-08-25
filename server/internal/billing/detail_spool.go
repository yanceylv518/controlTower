package billing

import (
	"compress/gzip"
	"context"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

const DefaultBillingSpoolRoot = "data/billing-staging"

type DetailPageSpool interface {
	WritePage(context.Context, Job, JobStep, LogCursor, []RequestDetail) error
	OpenPages(context.Context, Job) ([]DetailPage, error)
	RemoveJob(context.Context, Job) error
}

type DetailPage struct{ Path string }

func (p DetailPage) Read(visit func(RequestDetail) error) error {
	f, err := os.Open(p.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer z.Close()
	decoder := gob.NewDecoder(z)
	for {
		var row RequestDetail
		if err = decoder.Decode(&row); err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err = visit(row); err != nil {
			return err
		}
	}
}

type FileDetailSpool struct{ Root string }

func (s FileDetailSpool) root() (string, error) {
	root := s.Root
	if root == "" {
		root = DefaultBillingSpoolRoot
	}
	return filepath.Abs(root)
}

func (s FileDetailSpool) jobDir(job Job) (string, error) {
	root, err := s.root()
	if err != nil {
		return "", err
	}
	if len(job.ID) != 32 {
		return "", fmt.Errorf("invalid billing job id")
	}
	if _, err = hex.DecodeString(job.ID); err != nil {
		return "", fmt.Errorf("invalid billing job id")
	}
	return filepath.Join(root, job.ID), nil
}

func (s FileDetailSpool) WritePage(ctx context.Context, job Job, step JobStep, cursor LogCursor, rows []RequestDetail) error {
	if err := ctx.Err(); err != nil || len(rows) == 0 {
		return err
	}
	dir, err := s.jobDir(job)
	if err != nil {
		return err
	}
	dir = filepath.Join(dir, fmt.Sprintf("%06d", step.StepNo))
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	target := filepath.Join(dir, fmt.Sprintf("%020d-%020d.gob.gz", cursor.CreatedUnix, cursor.ID))
	tmp, err := os.CreateTemp(dir, ".page-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	z, err := gzip.NewWriterLevel(tmp, gzip.BestSpeed)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	encoder := gob.NewEncoder(z)
	for _, row := range rows {
		if err = encoder.Encode(row); err != nil {
			_ = z.Close()
			_ = tmp.Close()
			return err
		}
	}
	if err = z.Close(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	_ = os.Remove(target)
	if err = os.Rename(tmpName, target); err != nil {
		return err
	}
	ok = true
	return nil
}

func (s FileDetailSpool) OpenPages(ctx context.Context, job Job) ([]DetailPage, error) {
	dir, err := s.jobDir(job)
	if err != nil {
		return nil, err
	}
	paths := []string{}
	err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() && filepath.Ext(path) == ".gz" {
			paths = append(paths, path)
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	pages := make([]DetailPage, len(paths))
	for i, path := range paths {
		pages[i] = DetailPage{Path: path}
	}
	return pages, nil
}

func (s FileDetailSpool) RemoveJob(ctx context.Context, job Job) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := s.jobDir(job)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

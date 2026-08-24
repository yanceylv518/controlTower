package billing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type UserDailyFileCleanupStore interface {
	ListExpiredInactiveBillingFiles(context.Context, time.Time, int) ([]UserDailyFile, error)
	DeleteBillingUserDailyFile(context.Context, UserDailyFile) error
}

type UserDailyFileCleaner struct {
	Store UserDailyFileCleanupStore
	Root  string
}

func (c UserDailyFileCleaner) Cleanup(ctx context.Context, cutoff time.Time) (int, error) {
	root := c.Root
	if root == "" {
		root = DefaultBillingFileRoot
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return 0, err
	}
	removed := 0
	for {
		items, queryErr := c.Store.ListExpiredInactiveBillingFiles(ctx, cutoff, 200)
		if queryErr != nil {
			return removed, queryErr
		}
		if len(items) == 0 {
			return removed, nil
		}
		for _, item := range items {
			path := filepath.Join(root, filepath.FromSlash(item.RelativePath))
			relative, relErr := filepath.Rel(root, path)
			if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
				return removed, fmt.Errorf("invalid billing cleanup path")
			}
			if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
				return removed, removeErr
			}
			if deleteErr := c.Store.DeleteBillingUserDailyFile(ctx, item); deleteErr != nil {
				return removed, deleteErr
			}
			removed++
		}
	}
}

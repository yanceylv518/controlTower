package dashboard

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"controltower/server/internal/billing"
)

func statementDailyMemberFilename(job billing.Job, file billing.UserDailyFile) string {
	name := statementDailyFilename(job, file.BillDay)
	if job.JobType == "upstream_statement" && file.ChannelID > 0 {
		return strings.TrimSuffix(name, ".xlsx") + fmt.Sprintf("-渠道-%d.xlsx", file.ChannelID)
	}
	if job.JobType == "upstream_statement" {
		return strings.TrimSuffix(name, ".xlsx") + fmt.Sprintf("-用户-%d.xlsx", file.UserID)
	}
	return name
}

func statementDailyDownloads(job billing.Job, files []billing.UserDailyFile) []map[string]any {
	groups := map[string][]billing.UserDailyFile{}
	for _, file := range files {
		day := file.BillDay.In(billing.BusinessLocation).Format("2006-01-02")
		groups[day] = append(groups[day], file)
	}
	days := make([]string, 0, len(groups))
	for day := range groups {
		days = append(days, day)
	}
	sort.Strings(days)
	result := []map[string]any{}
	for _, day := range days {
		group := groups[day]
		name := statementDailyFilename(job, group[0].BillDay)
		if len(group) > 1 {
			name = strings.TrimSuffix(name, ".xlsx") + ".zip"
		}
		result = append(result, map[string]any{"day": day, "filename": name})
	}
	return result
}

// Build on disk before sending headers: missing members must never look like a
// successful partial download, and large statements must not accumulate in RAM.
func (h BillingStatementResultHandler) writeDailyArchive(w http.ResponseWriter, r *http.Request, job billing.Job, day time.Time, files []billing.UserDailyFile) {
	archive, err := os.CreateTemp("", "billing-daily-*.zip")
	if err != nil {
		writeDashboardError(w, 500, "billing_file_unavailable")
		return
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	z := zip.NewWriter(archive)
	root := h.Root
	if root == "" {
		root = billing.DefaultBillingFileRoot
	}
	root, err = filepath.Abs(root)
	if err != nil {
		writeDashboardError(w, 500, "billing_file_unavailable")
		return
	}
	for _, item := range files {
		path := filepath.Join(root, filepath.FromSlash(item.RelativePath))
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			writeDashboardError(w, 500, "billing_file_path_invalid")
			return
		}
		in, err := os.Open(path)
		if err != nil {
			writeDashboardError(w, 404, "billing_file_missing")
			return
		}
		info, err := in.Stat()
		if err != nil || !info.Mode().IsRegular() {
			in.Close()
			writeDashboardError(w, 500, "billing_file_unavailable")
			return
		}
		entry, err := z.Create(statementDailyMemberFilename(job, item))
		if err == nil {
			if job.UsageVersion >= billing.SettlementUsageVersion {
				var info os.FileInfo
				info, err = in.Stat()
				if err == nil {
					copy := job
					copy.From = day
					copy.To = day.AddDate(0, 0, 1)
					copy.BillPeriod = "daily"
					err = billing.RestyleSettlementDailyFile(r.Context(), entry, in, info.Size(), copy)
				}
			} else {
				_, err = io.Copy(entry, in)
			}
		}
		in.Close()
		if err != nil {
			writeDashboardError(w, 500, "billing_file_unavailable")
			return
		}
		if r.Context().Err() != nil {
			return
		}
	}
	if err = z.Close(); err != nil {
		writeDashboardError(w, 500, "billing_file_unavailable")
		return
	}
	info, err := archive.Stat()
	if err != nil {
		writeDashboardError(w, 500, "billing_file_unavailable")
		return
	}
	name := strings.TrimSuffix(statementDailyFilename(job, day), ".xlsx") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="billing-daily-%s.zip"; filename*=UTF-8''%s`, day.Format("2006-01-02"), url.PathEscape(name)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, info.ModTime(), archive)
}

// New upstream workbooks are keyed by channel. Older snapshots retain their
// original user files until explicitly regenerated; no source reads on download.
func (h BillingStatementResultHandler) statementDownloadFiles(ctx context.Context, job billing.Job) ([]billing.UserDailyFile, error) {
	if job.JobType == "upstream_statement" && job.UsageVersion >= billing.SettlementUsageVersion {
		if store, ok := h.Store.(interface {
			ListBillingStatementChannelFiles(context.Context, string) ([]billing.ChannelDailyFile, error)
		}); ok {
			files, err := store.ListBillingStatementChannelFiles(ctx, job.ID)
			if err != nil {
				return nil, err
			}
			out := []billing.UserDailyFile{}
			for _, f := range files {
				if strings.HasSuffix(f.RelativePath, ".xlsx") {
					out = append(out, billing.UserDailyFile{JobID: f.JobID, InstanceID: f.InstanceID, BillDay: f.BillDay, ChannelID: f.ChannelID, RelativePath: f.RelativePath, FileSize: f.FileSize, SHA256: f.SHA256, CreatedAt: f.CreatedAt})
				}
			}
			if len(out) > 0 {
				return out, nil
			}
		}
	}
	return h.Store.ListBillingStatementUserFiles(ctx, job.ID)
}

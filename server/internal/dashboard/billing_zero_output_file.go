package dashboard

import (
	"controltower/server/internal/billing"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Downloads reuse the published file and do not query source logs or rebuild Excel.
func (h BillingStatementResultHandler) writeZeroOutputFile(w http.ResponseWriter, r *http.Request, job billing.Job) {
	files, err := h.Store.ListBillingStatementUserFiles(r.Context(), job.ID)
	if err != nil || len(files) != 1 {
		writeDashboardError(w, 404, "billing_zero_output_file_missing")
		return
	}
	root := h.Root
	if root == "" {
		root = billing.DefaultBillingFileRoot
	}
	root, err = filepath.Abs(root)
	if err != nil {
		writeDashboardError(w, 500, "billing_statement_files_failed")
		return
	}
	path := filepath.Join(root, filepath.FromSlash(files[0].RelativePath))
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		writeDashboardError(w, 404, "billing_zero_output_file_missing")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeDashboardError(w, 404, "billing_zero_output_file_missing")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != files[0].FileSize {
		writeDashboardError(w, 404, "billing_zero_output_file_missing")
		return
	}
	name := strings.TrimSuffix(statementArchiveFilename(job), ".zip") + "-空输出账单.xlsx"
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="zero-output.xlsx"; filename*=UTF-8''%s`, url.PathEscape(name)))
	http.ServeContent(w, r, name, info.ModTime(), f)
}

package dashboard

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Export caches stay ZIP internally. Serve a lone workbook directly, including
// old cached exports, based on the actual filtered output rather than row estimates.
func serveBillingDetailDownload(w http.ResponseWriter, r *http.Request, path, base string) {
	f, err := os.Open(path)
	if err != nil {
		writeDashboardError(w, 404, "billing_file_missing")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeDashboardError(w, 500, "billing_file_unavailable")
		return
	}
	z, err := zip.NewReader(f, info.Size())
	if err != nil {
		writeDashboardError(w, 500, "billing_file_unavailable")
		return
	}
	var files []*zip.File
	for _, v := range z.File {
		if !v.FileInfo().IsDir() {
			if !strings.HasSuffix(strings.ToLower(v.Name), ".xlsx") {
				writeDashboardError(w, 500, "billing_file_unavailable")
				return
			}
			files = append(files, v)
		}
	}
	if len(files) == 0 {
		writeDashboardError(w, 500, "billing_file_unavailable")
		return
	}
	content := f
	ext, mime := ".zip", "application/zip"
	if len(files) == 1 {
		source, e := files[0].Open()
		if e != nil {
			writeDashboardError(w, 500, "billing_file_unavailable")
			return
		}
		defer source.Close()
		tmp, e := os.CreateTemp("", "billing-detail-download-*.xlsx")
		if e != nil {
			writeDashboardError(w, 500, "billing_file_unavailable")
			return
		}
		defer os.Remove(tmp.Name())
		defer tmp.Close()
		if _, e = io.Copy(tmp, source); e != nil {
			writeDashboardError(w, 500, "billing_file_unavailable")
			return
		}
		if _, e = tmp.Seek(0, io.SeekStart); e != nil {
			writeDashboardError(w, 500, "billing_file_unavailable")
			return
		}
		content = tmp
		ext = ".xlsx"
		mime = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	if r.Context().Err() != nil {
		return
	}
	name := base + ext
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="billing-details%s"; filename*=UTF-8''%s`, ext, url.PathEscape(name)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, info.ModTime(), content)
}

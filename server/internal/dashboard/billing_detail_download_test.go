package dashboard

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"controltower/server/internal/billing"
)

func TestDetailDownloadSingleWorkbookOrMultipleParts(t *testing.T) {
	var book bytes.Buffer
	if err := billing.WriteUserDailyWorkbook(&book, billing.Job{}, billing.UserDailyFile{}, nil); err != nil {
		t.Fatal(err)
	}
	for _, parts := range []int{1, 2} {
		t.Run(fmt.Sprint(parts), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cached.zip")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			z := zip.NewWriter(f)
			for i := 0; i < parts; i++ {
				entry, e := z.Create(fmt.Sprintf("明细-%03d.xlsx", i+1))
				if e != nil {
					t.Fatal(e)
				}
				entry.Write(book.Bytes())
			}
			if err = z.Close(); err != nil {
				t.Fatal(err)
			}
			f.Close()
			w := httptest.NewRecorder()
			serveBillingDetailDownload(w, httptest.NewRequest("GET", "/", nil), path, "2026-09-30-日账单明细")
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			result, e := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
			if e != nil {
				t.Fatal(e)
			}
			if parts == 1 {
				if !bytes.Equal(w.Body.Bytes(), book.Bytes()) || !strings.Contains(w.Header().Get("Content-Type"), "spreadsheetml") || !strings.Contains(w.Header().Get("Content-Disposition"), ".xlsx") {
					t.Fatal("single export not served as original workbook", w.Header())
				}
			} else {
				if len(result.File) != parts || w.Header().Get("Content-Type") != "application/zip" || !strings.Contains(w.Header().Get("Content-Disposition"), ".zip") {
					t.Fatal(w.Header(), len(result.File))
				}
				for _, file := range result.File {
					r, _ := file.Open()
					data, e := io.ReadAll(r)
					r.Close()
					if e != nil || !bytes.Equal(data, book.Bytes()) {
						t.Fatal("part changed", e)
					}
				}
			}
		})
	}
}

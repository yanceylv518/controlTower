package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBillingDownloadPreparationReceipt(t *testing.T) {
	for _, code := range []int{200, 206, 403, 404, 500} {
		r := httptest.NewRequest("GET", "/?download_token="+strings.Repeat("a", 32), nil)
		rec := httptest.NewRecorder()
		w := billingDownloadReceipt(rec, r)
		if rec.Header().Get("Set-Cookie") != "" {
			t.Fatal("premature ready")
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte("body"))
		cookies := rec.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatal(cookies)
		}
		want := "error"
		if code < 300 {
			want = "ready"
		}
		if cookies[0].Value != want || cookies[0].HttpOnly || cookies[0].Path != "/" {
			t.Fatal(cookies[0])
		}
	}
	rec := httptest.NewRecorder()
	w := billingDownloadReceipt(rec, httptest.NewRequest("GET", "/?download_token=invalid", nil))
	w.WriteHeader(http.StatusOK)
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("invalid token accepted")
	}
}

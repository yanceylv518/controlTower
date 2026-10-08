package dashboard

import "net/http"

// Signal when the response starts, without buffering large exports in the page.
// This is a preparation receipt, not confirmation that a file reached disk.
func billingDownloadReceipt(w http.ResponseWriter, r *http.Request) http.ResponseWriter {
	token := r.URL.Query().Get("download_token")
	if len(token) != 32 {
		return w
	}
	for _, c := range token {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return w
		}
	}
	return &billingDownloadResponse{ResponseWriter: w, token: token}
}

type billingDownloadResponse struct {
	http.ResponseWriter
	token string
	sent  bool
}

func (w *billingDownloadResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *billingDownloadResponse) Flush() {
	if !w.sent {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *billingDownloadResponse) WriteHeader(status int) {
	if !w.sent && status >= 200 {
		w.sent = true
		state := "error"
		if status >= 200 && status < 300 {
			state = "ready"
		}
		http.SetCookie(w.ResponseWriter, &http.Cookie{Name: "ct_download_" + w.token, Value: state, Path: "/", MaxAge: 600, SameSite: http.SameSiteStrictMode})
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *billingDownloadResponse) Write(p []byte) (int, error) {
	if !w.sent {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

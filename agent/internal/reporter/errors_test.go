package reporter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestSafeErrorSummary(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&url.Error{Op: "Post", URL: "https://user:secret@example.test/?token=secret", Err: context.DeadlineExceeded}, "timeout"},
		{&mysql.MySQLError{Number: 1045, Message: "password=secret"}, "mysql_code=1045"},
		{errors.New("token=secret\nforged log"), "error_type=*errors.errorString"},
		{fmt.Errorf("secret: %w", &HTTPStatusError{Operation: "secret", StatusCode: 413}), "http_status=413"},
	} {
		if got := SafeErrorSummary(tc.err); got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
}

func TestReportHTTPFailureHasSafeStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(413)
		_, _ = w.Write([]byte("private response"))
	}))
	defer server.Close()
	err := NewClient(server.URL, "private token", time.Second).Report(context.Background(), AgentReportRequest{})
	if got := SafeErrorSummary(err); got != "http_status=413" {
		t.Fatalf("got %q", got)
	}
}

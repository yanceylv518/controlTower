package channelcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStatusOnlyUpdateRejectsDefiniteHTTPFailures(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 405, 409, 422, 408, 429, 500, 503} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			posts, puts := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"success":true,"data":{"id":1,"status":1,"weight":10,"priority":2,"group":"default","key":"mock-only"}}`))
					return
				}
				if r.Method == http.MethodPut {
					puts++
				}
				posts++
				var body map[string]int
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 1 || body["status"] != 2 {
					t.Errorf("status payload: %+v %v", body, err)
				}
				w.WriteHeader(code)
			}))
			defer server.Close()
			client := New(server.URL, "mock-status-token", 7, server.Client())
			target := 2
			_, err := client.Update(context.Background(), UpdateRequest{ChannelID: 1, Status: &target})
			wantRejected := code < 500 && code != 408 && code != 429
			if err == nil || errors.Is(err, ErrStatusRejected) != wantRejected || puts != 0 || posts != 1 {
				t.Fatalf("err=%v rejected=%v posts=%d puts=%d", err, wantRejected, posts, puts)
			}
			var response *HTTPError
			if !errors.As(err, &response) || response.StatusCode != code {
				t.Fatalf("HTTP error cause lost: %v", err)
			}
		})
	}
}

func TestStatusOnlyUpdateBusinessRejectionAndSuccess(t *testing.T) {
	for _, success := range []bool{false, true} {
		t.Run(map[bool]string{false: "rejected", true: "succeeded"}[success], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"success":true,"data":{"id":1,"status":1,"weight":10,"priority":2,"group":"default"}}`))
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"success": success, "message": "mock rejection"})
			}))
			defer server.Close()
			client := New(server.URL, "mock-status-token", 7, server.Client())
			target := 2
			result, err := client.Update(context.Background(), UpdateRequest{ChannelID: 1, Status: &target})
			if success {
				if err != nil || result.Status == nil || *result.Status != 2 || *result.Weight != 10 || *result.Priority != 2 || result.Group != "default" {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			} else if !errors.Is(err, ErrStatusRejected) {
				t.Fatalf("business rejection: %v", err)
			}
		})
	}
}

package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/billing"
	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
)

type userSupplementStore struct {
	auditDiscountStore
	writeCalls int
	writeError error
}

func (s *userSupplementStore) PutBillingDiscountRule(_ context.Context, item billing.DiscountRule) (billing.DiscountRule, error) {
	s.writeCalls++
	if s.writeError != nil {
		return item, s.writeError
	}
	if item.ID == 0 {
		item.ID = 17
		s.items = append(s.items, item)
		return item, nil
	}
	return s.auditDiscountStore.PutBillingDiscountRule(context.Background(), item)
}

func TestUserSupplementEffectiveRanges(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"open", `{"id":1,"instance_id":"test","discount_type":"user_model","subject_id":2,"model_name":"model","discount":"0.8","effective_from":"2020-01-01T00:00:00Z"}`},
		{"explicit-null", `{"id":1,"instance_id":"test","discount_type":"user_model","subject_id":2,"model_name":"model","discount":"0.8","effective_from":"2020-01-01T00:00:00Z","effective_to":null}`},
		{"historical", `{"id":1,"instance_id":"test","discount_type":"user_model","subject_id":2,"model_name":"model","discount":"0.8","effective_from":"2020-01-01T00:00:00Z","effective_to":"2020-02-01T00:00:00Z"}`},
		{"future", `{"id":1,"instance_id":"test","discount_type":"user_model","subject_id":2,"model_name":"model","discount":"0.8","effective_from":"2026-10-01T00:00:00+08:00","effective_to":"2099-01-01T00:00:00+08:00"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, method := range []string{http.MethodPost, http.MethodPut} {
				store := &userSupplementStore{auditDiscountStore: auditDiscountStore{items: []billing.DiscountRule{{ID: 1}}}}
				h := BillingDiscountHandler{Store: store}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(method, "/", strings.NewReader(tc.body)))
				if w.Code != http.StatusOK || store.writeCalls != 1 || len(store.audits) != 1 {
					t.Fatal(method, w.Code, w.Body.String(), store.writeCalls, store.audits)
				}
				var saved billing.DiscountRule
				if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
					t.Fatal(err)
				}
				if saved.ID <= 0 || saved.SubjectID != 2 || saved.Discount != "0.8" || saved.ModelName != "model" {
					t.Fatalf("saved rule: %+v", saved)
				}
				if (tc.name == "open" || tc.name == "explicit-null") && saved.EffectiveTo != nil {
					t.Fatalf("open-ended rule acquired an end: %+v", saved)
				}
			}
		})
	}
}

func TestUserSupplementValidationAndOverlap(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{"missing-site", "instance_id", ""}, {"missing-user", "subject_id", 0},
		{"missing-model", "model_name", " "}, {"negative-discount", "discount", "-0.1"},
		{"over-full-price", "discount", "1.1"}, {"invalid-discount", "discount", "invalid"},
		{"missing-start", "effective_from", "0001-01-01T00:00:00Z"},
		{"invalid-start", "effective_from", "invalid"},
		{"equal-end", "effective_to", "2026-10-01T00:00:00+08:00"},
		{"earlier-end", "effective_to", "2026-09-30T00:00:00+08:00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"instance_id": "test", "discount_type": "user_model", "subject_id": 2,
				"model_name": "model", "discount": "0.8", "effective_from": "2026-10-01T00:00:00+08:00"}
			body[tc.field] = tc.value
			payload, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			store := &userSupplementStore{}
			w := httptest.NewRecorder()
			BillingDiscountHandler{Store: store}.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(payload))))
			if w.Code != http.StatusBadRequest || store.writeCalls != 0 || len(store.audits) != 0 {
				t.Fatal(w.Code, w.Body.String(), store.writeCalls, store.audits)
			}
		})
	}
	store := &userSupplementStore{writeError: billing.ErrDiscountOverlap}
	w := httptest.NewRecorder()
	body := `{"instance_id":"test","discount_type":"user_model","subject_id":2,"model_name":"model","discount":"0.8","effective_from":"2026-10-01T00:00:00+08:00"}`
	BillingDiscountHandler{Store: store}.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "billing_discount_overlap") || len(store.audits) != 0 {
		t.Fatal(w.Code, w.Body.String(), store.audits)
	}
	store.writeError = errors.New("database unavailable")
	w = httptest.NewRecorder()
	BillingDiscountHandler{Store: store}.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "billing_discount_save_failed") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestUserSupplementRequiresAuthenticatedAdmin(t *testing.T) {
	store := &userSupplementStore{}
	accounts := ingest.NewMemoryStore()
	if err := accounts.CreateUser(storage.User{ID: 1, Username: "operator", Role: "operator", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.CreateSession(storage.Session{ID: "discount-session", UserID: 1, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	h := ctauth.RequireSessionOrToken(ctauth.NewManager(accounts, time.Hour), "", BillingDiscountHandler{Store: store})
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
		for _, authenticated := range []bool{false, true} {
			r := httptest.NewRequest(method, "/api/dashboard/billing/discounts?instance_id=test&type=user_model&id=1", nil)
			r.Header.Set("X-Requested-With", "XMLHttpRequest")
			want := http.StatusUnauthorized
			if authenticated {
				r.AddCookie(&http.Cookie{Name: "ct_session", Value: "discount-session"})
				want = http.StatusForbidden
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != want || store.writeCalls != 0 {
				t.Fatal(method, authenticated, w.Code, w.Body.String())
			}
		}
	}
}

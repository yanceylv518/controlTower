package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
)

func TestChannelCommandHandlerValidationActorAndDTO(t *testing.T) {
	s := ingest.NewMemoryStore()
	now := time.Now().UTC()
	_ = s.CreateInstance(storage.Instance{ID: "inst", Enabled: true, CreatedAt: now, UpdatedAt: now})
	h := CommandHandler{Store: s, Instances: s, Directory: channelGroupDirectoryStub{channels: []tuning.Channel{{ID: 7, Name: "primary", GroupName: "default,vip"}}}}
	call := func(body string, actor string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/dashboard/channels/7/commands", bytes.NewBufferString(body))
		r.SetPathValue("channelID", "7")
		r = r.WithContext(context.WithValue(r.Context(), testActorKey{}, actor))
		w := httptest.NewRecorder()
		if actor != "" {
			wrapped := ctauth.RequireSessionOrToken(nil, "token", http.HandlerFunc(h.Create))
			r.Header.Set("Authorization", "Bearer token")
			wrapped.ServeHTTP(w, r)
		} else {
			h.Create(w, r)
		}
		return w
	}
	if w := call(`{"instance_id":"inst","status":2}`, ""); w.Code != 400 {
		t.Fatalf("confirm=%d", w.Code)
	}
	if w := call(`{"instance_id":"missing","confirm":true,"status":2}`, ""); w.Code != 404 {
		t.Fatalf("missing=%d", w.Code)
	}
	if w := call(`{"instance_id":"inst","confirm":true}`, ""); w.Code != 400 {
		t.Fatalf("empty=%d", w.Code)
	}
	w := call(`{"instance_id":"inst","confirm":true,"status":2}`, "token")
	if w.Code != 201 {
		t.Fatalf("create=%d %s", w.Code, w.Body.String())
	}
	var dto map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &dto)
	if dto["created_by"] != "token" || dto["instance_id"] != "inst" || dto["channel_id"] == nil {
		t.Fatalf("dto=%v", dto)
	}
	w = call(`{"instance_id":"inst","confirm":true,"group":" default, vip, default "}`, "token")
	if w.Code != http.StatusCreated || !bytes.Contains(w.Body.Bytes(), []byte(`"group":"default,vip"`)) {
		t.Fatalf("group command=%d %s", w.Code, w.Body.String())
	}
	if w = call(`{"instance_id":"inst","confirm":true,"group":"custom"}`, "token"); w.Code != http.StatusCreated || !bytes.Contains(w.Body.Bytes(), []byte(`"group":"custom"`)) {
		t.Fatalf("unknown group command=%d %s", w.Code, w.Body.String())
	}
}

type testActorKey struct{}

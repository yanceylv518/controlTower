package dashboard

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
)

type channelGroupDirectoryStub struct {
	channels []tuning.Channel
	err      error
}

func (s channelGroupDirectoryStub) LatestChannels(string) ([]tuning.Channel, error) {
	return s.channels, s.err
}

type channelGroupUpdaterStub struct {
	site, actor, group string
	channelID          int64
	calls              int
	result             storage.ChannelCommand
	err                error
}

type channelGroupTuningStoreStub struct {
	tuningStub
	channels []tuning.Channel
	err      error
}

func (s channelGroupTuningStoreStub) LatestChannels(string) ([]tuning.Channel, error) {
	return s.channels, s.err
}

func (s *channelGroupUpdaterStub) UpdateChannelGroup(_ context.Context, site string, channelID int64, group, actor string, _ time.Time) (storage.ChannelCommand, error) {
	s.calls++
	s.site, s.channelID, s.group, s.actor = site, channelID, group, actor
	return s.result, s.err
}

func TestChannelGroupHandlerValidatesBoundariesAndConfirmation(t *testing.T) {
	updater := &channelGroupUpdaterStub{result: storage.ChannelCommand{ID: "cmd-1", InstanceID: "inst-a", ChannelID: 7, Status: "pending", CreatedAt: time.Date(2026, 9, 14, 1, 2, 3, 0, time.UTC)}}
	h := ChannelGroupHandler{Updater: updater, Directory: channelGroupDirectoryStub{channels: []tuning.Channel{{ID: 7, Name: "primary", GroupName: "default,vip,fast"}}}}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.SetPathValue("channelID", "7")
		w := httptest.NewRecorder()
		h.Update(w, r)
		return w
	}
	if w := call(http.MethodPost, "/api/dashboard/tuning/channels/7/group?site_id=site-a", `{"confirm":true,"group":"vip"}`); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method=%d body=%s", w.Code, w.Body.String())
	}
	if w := call(http.MethodPut, "/api/dashboard/tuning/channels/7/group", `{"confirm":true,"group":"vip"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("missing site=%d body=%s", w.Code, w.Body.String())
	}
	if w := call(http.MethodPut, "/api/dashboard/tuning/channels/7/group?site_id=site-a", `{"group":"vip"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("missing confirmation=%d body=%s", w.Code, w.Body.String())
	}
	if w := call(http.MethodPut, "/api/dashboard/tuning/channels/7/group?site_id=site-a", `{"confirm":true}`); w.Code != http.StatusBadRequest {
		t.Fatalf("missing group=%d body=%s", w.Code, w.Body.String())
	}
	if w := call(http.MethodPut, "/api/dashboard/tuning/channels/7/group?site_id=site-a", `{"confirm":true,"group":"vip,,fast"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("empty group item=%d body=%s", w.Code, w.Body.String())
	}
	if w := call(http.MethodPut, "/api/dashboard/tuning/channels/7/group?site_id=site-a", `{"confirm":true,"group":"  "}`); w.Code != http.StatusAccepted {
		t.Fatalf("clearing groups=%d body=%s", w.Code, w.Body.String())
	}
	if updater.site != "site-a" || updater.channelID != 7 || updater.group != "" {
		t.Fatalf("unexpected updater request: %#v", updater)
	}
	callCount := updater.calls
	if w := call(http.MethodPut, "/api/dashboard/tuning/channels/7/group?site_id=site-a", `{"confirm":true,"group":"custom"}`); w.Code != http.StatusAccepted {
		t.Fatalf("unknown group=%d body=%s", w.Code, w.Body.String())
	}
	if updater.calls != callCount+1 || updater.group != "custom" {
		t.Fatalf("custom group did not reach updater: %#v", updater)
	}
}

func TestChannelGroupHandlerRejectsUnknownChannelAndMapsStatuses(t *testing.T) {
	updater := &channelGroupUpdaterStub{result: storage.ChannelCommand{ID: "cmd-2", InstanceID: "inst-a", ChannelID: 7, Status: "succeeded"}}
	h := ChannelGroupHandler{Updater: updater, Directory: channelGroupDirectoryStub{channels: []tuning.Channel{{ID: 9, Name: "other"}}}}
	r := httptest.NewRequest(http.MethodPut, "/api/dashboard/tuning/channels/7/group?site_id=site-a", bytes.NewBufferString(`{"confirm":true,"group":"vip"}`))
	r.SetPathValue("channelID", "7")
	w := httptest.NewRecorder()
	h.Update(w, r)
	if w.Code != http.StatusNotFound || updater.site != "" {
		t.Fatalf("unknown channel accepted: %d %s %#v", w.Code, w.Body.String(), updater)
	}

	h.Directory = channelGroupDirectoryStub{channels: []tuning.Channel{{ID: 7, Name: "primary", GroupName: "default,vip"}}}
	r = httptest.NewRequest(http.MethodPut, "/api/dashboard/tuning/channels/7/group?site_id=site-a", bytes.NewBufferString(`{"confirm":true,"group":"vip"}`))
	r.SetPathValue("channelID", "7")
	w = httptest.NewRecorder()
	h.Update(w, r)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"status":"succeeded"`)) {
		t.Fatalf("succeeded status=%d body=%s", w.Code, w.Body.String())
	}

	updater.err = tuning.ErrChannelNotFound
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPut, "/api/dashboard/tuning/channels/7/group?site_id=site-a", bytes.NewBufferString(`{"confirm":true,"group":"vip"}`))
	r.SetPathValue("channelID", "7")
	h.Update(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("updater not-found=%d body=%s", w.Code, w.Body.String())
	}
	updater.err = errors.New("controller unavailable")
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPut, "/api/dashboard/tuning/channels/7/group?site_id=site-a", bytes.NewBufferString(`{"confirm":true,"group":"vip"}`))
	r.SetPathValue("channelID", "7")
	h.Update(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("updater failure=%d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleTuningChannelsReturnsCompleteChannelMetadata(t *testing.T) {
	h := NewHandler(nil).WithTuningStore(&channelGroupTuningStoreStub{channels: []tuning.Channel{{ID: 7, Name: "primary", Status: "disabled", Weight: 12, Priority: 3, Models: []string{"m-a", "m-b"}, GroupName: "default,vip"}}})
	w := httptest.NewRecorder()
	h.HandleTuningChannels(w, httptest.NewRequest(http.MethodGet, "/api/dashboard/tuning/channels?site_id=site-a", nil))
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"channel_id":7`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"status":"disabled"`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"group_name":"default,vip"`)) {
		t.Fatalf("channel directory response=%d body=%s", w.Code, w.Body.String())
	}
}

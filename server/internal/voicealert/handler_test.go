package voicealert

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

type settingsMemory struct {
	config Config
	list   CustomerList
	err    error
	saved  bool
}

func (s *settingsMemory) Config(context.Context) (Config, error) { return s.config, nil }
func (s *settingsMemory) SaveConfig(_ context.Context, c Config, _ string) error {
	s.config = c
	s.saved = true
	return nil
}
func (s *settingsMemory) Calls(context.Context) ([]CallRecord, error)     { return []CallRecord{}, nil }
func (s *settingsMemory) Customers(context.Context) (CustomerList, error) { return s.list, s.err }

func TestSettingsDirectoryAndSaveWithoutManualTargets(t *testing.T) {
	s := &settingsMemory{config: DefaultConfig(), list: CustomerList{Customers: []Target{{Site: "a", UserID: 7, Label: "alice"}}, UnavailableSites: []string{}}}
	h := Handler{Store: s, Caller: &fakeCaller{}}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("PUT", "/", strings.NewReader(`{"enabled":true,"tts_code":"TTS_test","percent":50,"delta":10000000,"recipients":[{"phone":"13800000000","targets":["a/7"]}]}`)))
	if w.Code != 200 || !s.saved || !strings.Contains(w.Body.String(), `"label":"alice"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if !s.config.UsePercent || s.config.Percent != 50 {
		t.Fatal("legacy request changed threshold semantics", s.config)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("PUT", "/", strings.NewReader(`{"use_percent":false,"percent":20,"delta":10000000,"recipients":[{"phone":"13800000000","targets":["a/7"]}]}`)))
	if w.Code != 200 || s.config.UsePercent || s.config.Percent != 20 || !strings.Contains(w.Body.String(), `"use_percent":false`) {
		t.Fatal(w.Code, w.Body.String())
	}
	s.config.Targets = []Target{{Site: "legacy", UserID: 1, Label: "manual"}}
	s.err = errors.New("source credentials must not leak")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	var response struct {
		Config         Config
		DirectoryError string `json:"directory_error"`
		Customers      []Target
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || response.DirectoryError == "" || len(response.Config.Targets) != 0 || response.Config.Recipients[0].Targets[0] != "a/7" || strings.Contains(w.Body.String(), "credentials must not leak") {
		t.Fatal(w.Body.String())
	}
}

package voicealert

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestTrialObservationBoundaries(t *testing.T) {
	start := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	w := TrialWatch{Model: "gpt-4.1", Enabled: true, UserID: 7, TokenID: 9, Rule: "first", GapMinutes: 30, StartedAt: start, LastID: 10}
	l := TrialLog{Model: "gpt-4.1", ID: 11, UserID: 7, TokenID: 9, Type: 2, CreatedAt: start.Add(time.Second)}
	wrong := l
	wrong.UserID = 8
	if w.Observe(wrong) {
		t.Fatal("cross-user hit")
	}
	wrong = l
	wrong.TokenID = 8
	if w.Observe(wrong) {
		t.Fatal("cross-key hit")
	}
	old := l
	old.CreatedAt = start.Add(-time.Second)
	if w.Observe(old) {
		t.Fatal("historical hit")
	}
	failed := l
	failed.Type = 5
	if w.Observe(failed) {
		t.Fatal("error counted when disabled")
	}
	w.IncludeFailed = true
	if !w.Observe(failed) {
		t.Fatal("first error not detected")
	}
	if w.Observe(l) {
		t.Fatal("replayed same log")
	}
	l.ID++
	l.CreatedAt = l.CreatedAt.Add(time.Hour)
	if w.Observe(l) {
		t.Fatal("first mode repeated")
	}
	w.Rule = "resume"
	l.ID++
	l.CreatedAt = l.CreatedAt.Add(29 * time.Minute)
	if w.Observe(l) {
		t.Fatal("continuous activity repeated")
	}
	l.ID++
	l.CreatedAt = l.CreatedAt.Add(30 * time.Minute)
	if !w.Observe(l) {
		t.Fatal("silence threshold not detected")
	}
	w.Enabled = false
	l.ID++
	l.CreatedAt = l.CreatedAt.Add(time.Hour)
	if w.Observe(l) {
		t.Fatal("paused watch fired")
	}
}

func TestTrialTemplateParameters(t *testing.T) {
	count := 0
	a := &Aliyun{AccessKeyID: "test", AccessKeySecret: "test", HTTP: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		count++
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		var variables map[string]string
		if err := json.Unmarshal([]byte(r.PostForm.Get("TtsParam")), &variables); err != nil {
			t.Fatal(err)
		}
		if variables["site"] != "测试站" || variables["customer"] != "客户甲" || len(variables) != 2 {
			t.Fatal(variables)
		}
		if r.PostForm.Get("CalledShowNumber") != "057100000000" {
			t.Fatal("shared caller lost")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"Code":"OK","CallId":"fake"}`))}, nil
	})}}
	result := a.CallTemplate(context.Background(), Config{TtsCode: "TTS_trial", CalledShowNumber: "057100000000"}, Target{Phone: "13800000000"}, "id", map[string]string{"site": "测试站", "customer": "客户甲"})
	if result.Status != "accepted" || count != 1 {
		t.Fatal(result, count)
	}
}

func TestTrialConfigurationValidation(t *testing.T) {
	w := TrialWatch{Model: "gpt-4.1", Site: "a", UserID: 1, Label: "客户", Rule: "first", GapMinutes: 30, Enabled: true, Phone: true}
	if w.Validate() == nil {
		t.Fatal("phone without recipient accepted")
	}
	w.Phone = false
	w.Message = true
	if err := w.Validate(); err != nil {
		t.Fatal(err)
	}
	w.PersonIDs = []string{"invalid"}
	if w.Validate() == nil {
		t.Fatal("invalid person accepted")
	}
	c := DefaultConfig()
	c.TrialTemplateReady = true
	if c.Validate() == nil {
		t.Fatal("unconfigured template marked ready")
	}
}

func TestTrialModelFiltering(t *testing.T) {
	start := time.Now().UTC()
	for _, token := range []int64{0, 9} {
		w := TrialWatch{Model: "gpt-4.1", Enabled: true, UserID: 7, TokenID: token, Rule: "resume", GapMinutes: 30, StartedAt: start, LastID: 10, IncludeFailed: true}
		l := TrialLog{Model: "gpt-4.1", ID: 11, UserID: 7, TokenID: 9, Type: 2, CreatedAt: start.Add(time.Second)}
		for _, model := range []string{"", "gpt-4.1-mini", "GPT-4.1"} {
			l.Model = model
			if w.Observe(l) || w.Fired || w.LastID != 10 || !w.LastAt.IsZero() {
				t.Fatalf("unselected model changed state: %+v", w)
			}
		}
		l.Model = w.Model
		if !w.Observe(l) {
			t.Fatal("selected model did not trigger")
		}
		l.ID++
		l.Model = "other"
		l.Type = 5
		l.CreatedAt = l.CreatedAt.Add(29 * time.Minute)
		if w.Observe(l) {
			t.Fatal("other failed model triggered")
		}
		l.ID++
		l.Model = w.Model
		l.CreatedAt = l.CreatedAt.Add(time.Minute)
		if !w.Observe(l) {
			t.Fatal("other model postponed silence threshold")
		}
		w.Model = ""
		w.Fired = false
		l.ID++
		if w.Observe(l) {
			t.Fatal("legacy watch without model triggered")
		}
	}
}

func TestTrialModelValidation(t *testing.T) {
	w := TrialWatch{Site: "a", UserID: 1, Label: "客户", Rule: "first", GapMinutes: 30, Enabled: true, Message: true}
	for _, model := range []string{"", "  ", strings.Repeat("模", 201)} {
		w.Model = model
		if w.Validate() == nil {
			t.Fatalf("invalid model accepted: %q", model)
		}
	}
	w.Model = "gpt-4.1"
	if err := w.Validate(); err != nil {
		t.Fatal(err)
	}
	w.Model = ""
	w.Enabled = false
	if err := w.Validate(); err != nil {
		t.Fatalf("legacy watch cannot be paused: %v", err)
	}
}

package erroralert

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"controltower/agent/internal/logcollector"
)

type capture struct {
	mu       sync.Mutex
	errcode  string
	contents []string
}

func TestSlowRuleTriggersRearmsAndSeparatesStreaming(t *testing.T) {
	var mu sync.Mutex
	var messages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text struct {
				Content string `json:"content"`
			} `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		messages = append(messages, body.Text.Content)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"errcode":0}`))
	}))
	defer server.Close()
	n := New(server.URL, "inst-a", 10, 3, nil).WithSlowRule(120, 10, 3, 300)
	events := make([]logcollector.Event, 10)
	for i := range events {
		events[i] = logcollector.Event{ChannelID: 1, CreatedAt: time.Now(), UseTime: 1}
	}
	for i := 0; i < 3; i++ {
		events[i].UseTime = 150
	}
	n.Process(context.Background(), events)
	n.Process(context.Background(), []logcollector.Event{{ChannelID: 1, CreatedAt: time.Now(), UseTime: 150}})
	mu.Lock()
	if len(messages) != 1 || !strings.Contains(messages[0], "渠道慢返回激增") {
		t.Fatalf("messages=%v", messages)
	}
	mu.Unlock()
	fast := make([]logcollector.Event, 8)
	for i := range fast {
		fast[i] = logcollector.Event{ChannelID: 1, CreatedAt: time.Now(), UseTime: 1}
	}
	n.Process(context.Background(), fast)
	n.Process(context.Background(), []logcollector.Event{{ChannelID: 1, CreatedAt: time.Now(), UseTime: 150}, {ChannelID: 1, CreatedAt: time.Now(), UseTime: 150}, {ChannelID: 1, CreatedAt: time.Now(), UseTime: 150}})
	mu.Lock()
	if len(messages) != 2 {
		t.Fatalf("want rearmed second alert, got %d", len(messages))
	}
	mu.Unlock()
	n2 := New(server.URL, "inst-a", 10, 3, nil).WithSlowRule(120, 10, 1, 300)
	n2.Process(context.Background(), []logcollector.Event{{ChannelID: 2, CreatedAt: time.Now(), UseTime: 150, IsStream: true}})
	mu.Lock()
	before := len(messages)
	mu.Unlock()
	if before != 2 {
		t.Fatalf("stream below threshold alerted")
	}
	n2.WithSlowRule(120, 10, 1, 100).Process(context.Background(), []logcollector.Event{{ChannelID: 2, CreatedAt: time.Now(), UseTime: 150, IsStream: true}})
	mu.Lock()
	if len(messages) != 3 {
		t.Fatalf("stream above threshold did not alert")
	}
	mu.Unlock()
}

func TestEpisodeEventLogRecordsTransitions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"errcode":0}`)) }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "alert-events.jsonl")
	now := time.Now()
	n := New(server.URL, "inst-a", 2, 1, nil).WithRemindInterval(time.Hour).WithEventLog(path)
	n.now = func() time.Time { return now }
	n.Process(context.Background(), []logcollector.Event{{ChannelID: 7, LogType: "error", CreatedAt: now}})
	now = now.Add(61 * time.Minute)
	n.Process(context.Background(), []logcollector.Event{{ChannelID: 7, LogType: "error", CreatedAt: now}})
	n.Process(context.Background(), []logcollector.Event{{ChannelID: 7, CreatedAt: now}, {ChannelID: 7, CreatedAt: now}})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{`"kind":"alert"`, `"kind":"remind"`, `"kind":"rearm"`, `"rule":"error"`} {
		if !bytes.Contains(data, []byte(kind)) {
			t.Fatalf("missing %s in %s", kind, data)
		}
	}
}

func TestEventLogRotatesAndFailsSafe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), int(eventLogMaxBytes)+1), 0600); err != nil {
		t.Fatal(err)
	}
	logger := newEventLogger(path, nil)
	logger.logf = func(string, ...any) {}
	logger.append([]EventRecord{{Time: time.Now(), Dimension: "channel:1", Rule: "slow", Kind: "alert", WindowCount: 3, Threshold: 3}})
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotated file missing: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(data, []byte(`"rule":"slow"`)) {
		t.Fatalf("new event log invalid: %v %s", err, data)
	}
	failCount := 0
	bad := newEventLogger(filepath.Join(dir, "missing", "events.jsonl"), func(string, ...any) { failCount++ })
	bad.append([]EventRecord{{Time: time.Now(), Kind: "alert"}})
	bad.append([]EventRecord{{Time: time.Now(), Kind: "alert"}})
	if failCount != 1 || !bad.failed {
		t.Fatalf("failure must log once: count=%d failed=%v", failCount, bad.failed)
	}
}

func (c *capture) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			MsgType string `json:"msgtype"`
			Text    struct {
				Content string `json:"content"`
			} `json:"text"`
		}
		_ = json.Unmarshal(body, &payload)
		c.mu.Lock()
		if payload.MsgType == "text" {
			c.contents = append(c.contents, payload.Text.Content)
		}
		response := c.errcode
		c.mu.Unlock()
		if response == "" {
			response = `{"errcode":0,"errmsg":"ok"}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
}

func (c *capture) matching(substr string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, content := range c.contents {
		if strings.Contains(content, substr) {
			count++
		}
	}
	return count
}

func (c *capture) setErrcode(response string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errcode = response
}

func event(id int64, logType string, channelID int64, userID int64, username string) logcollector.Event {
	return logcollector.Event{
		SourceLogID:  id,
		LogType:      logType,
		ChannelID:    channelID,
		UserID:       userID,
		Username:     username,
		ErrorSummary: map[bool]string{true: "upstream timeout", false: ""}[logType == "error"],
	}
}

func events(logType string, count int, channelID int64, userID int64, username string) []logcollector.Event {
	items := make([]logcollector.Event, 0, count)
	for i := 0; i < count; i++ {
		items = append(items, event(int64(i), logType, channelID, userID, username))
	}
	return items
}

func TestNotifierFiresOncePerEpisode(t *testing.T) {
	c := &capture{}
	server := c.server()
	defer server.Close()
	n := New(server.URL, "inst-a", 10, 3, nil)

	n.Process(context.Background(), events("consume", 7, 18, 0, ""))
	if got := c.matching("渠道错误激增"); got != 0 {
		t.Fatalf("expected no alert below threshold, got %d", got)
	}

	n.Process(context.Background(), events("error", 3, 18, 0, ""))
	if got := c.matching("渠道错误激增"); got != 1 {
		t.Fatalf("expected 1 alert at threshold, got %d (%v)", got, c.contents)
	}

	// Still firing: more errors must not duplicate the message.
	n.Process(context.Background(), events("error", 2, 18, 0, ""))
	if got := c.matching("渠道错误激增"); got != 1 {
		t.Fatalf("expected no duplicate while firing, got %d", got)
	}
}

func TestNotifierRearmsAfterRecovery(t *testing.T) {
	c := &capture{}
	server := c.server()
	defer server.Close()
	n := New(server.URL, "inst-a", 10, 3, nil)

	n.Process(context.Background(), events("error", 3, 18, 0, ""))
	if got := c.matching("渠道错误激增"); got != 1 {
		t.Fatalf("expected first episode alert, got %d", got)
	}

	// 10 successes push all errors out of the window.
	n.Process(context.Background(), events("consume", 10, 18, 0, ""))
	n.Process(context.Background(), events("error", 3, 18, 0, ""))
	if got := c.matching("渠道错误激增"); got != 2 {
		t.Fatalf("expected renotification after recovery, got %d", got)
	}
}

func TestNotifierWindowLimitsErrorCount(t *testing.T) {
	c := &capture{}
	server := c.server()
	defer server.Close()
	n := New(server.URL, "inst-a", 10, 3, nil)

	n.Process(context.Background(), events("error", 3, 18, 0, ""))
	c.mu.Lock()
	c.contents = nil
	c.mu.Unlock()

	// 10 newer successes: the old errors leave the window, so no new alert
	// and the dimension is re-armed rather than firing.
	n.Process(context.Background(), events("consume", 10, 18, 0, ""))
	if got := c.matching("渠道错误激增"); got != 0 {
		t.Fatalf("expected no alert after recovery, got %d", got)
	}
}

func TestNotifierTracksChannelAndUserSeparately(t *testing.T) {
	c := &capture{}
	server := c.server()
	defer server.Close()
	n := New(server.URL, "inst-a", 10, 3, nil)

	n.Process(context.Background(), events("error", 3, 18, 7, "alice"))
	if got := c.matching("渠道错误激增"); got != 1 {
		t.Fatalf("expected channel alert, got %d", got)
	}
	if got := c.matching("客户错误激增"); got != 1 {
		t.Fatalf("expected user alert, got %d", got)
	}
	if got := c.matching("alice(7)"); got != 1 {
		t.Fatalf("expected username in user alert, got %v", c.contents)
	}
	if got := c.matching("最新错误: upstream timeout"); got != 2 {
		t.Fatalf("expected latest error summary in both alerts, got %v", c.contents)
	}
}

func TestNotifierRetriesAfterSendFailure(t *testing.T) {
	c := &capture{}
	server := c.server()
	defer server.Close()
	n := New(server.URL, "inst-a", 10, 3, nil)

	c.setErrcode(`{"errcode":310000,"errmsg":"keywords not in content"}`)
	n.Process(context.Background(), events("error", 3, 18, 0, ""))

	// The next pass retries even without new events for that dimension.
	c.setErrcode("")
	n.Process(context.Background(), nil)
	if got := c.matching("渠道错误激增"); got != 2 {
		t.Fatalf("expected rejected then retried message, got %d (%v)", got, c.contents)
	}

	// And once delivered, no further duplicates.
	n.Process(context.Background(), nil)
	if got := c.matching("渠道错误激增"); got != 2 {
		t.Fatalf("expected no duplicate after successful retry, got %d", got)
	}
}

func TestNotifierIncludesChannelNameWhenKnown(t *testing.T) {
	c := &capture{}
	server := c.server()
	defer server.Close()
	n := New(server.URL, "inst-a", 10, 3, nil)
	n.UpdateChannelNames(map[int64]string{18: "OpenAI-主力"})

	n.Process(context.Background(), events("error", 3, 18, 0, ""))
	if got := c.matching("渠道 18(OpenAI-主力)"); got != 1 {
		t.Fatalf("expected named channel label, got %v", c.contents)
	}
}

func TestNotifierFallsBackToChannelIDWithoutName(t *testing.T) {
	c := &capture{}
	server := c.server()
	defer server.Close()
	n := New(server.URL, "inst-a", 10, 3, nil)
	n.UpdateChannelNames(map[int64]string{21: "其他渠道"})

	n.Process(context.Background(), events("error", 3, 18, 0, ""))
	if got := c.matching("渠道 18 最近"); got != 1 {
		t.Fatalf("expected id-only label for unknown channel, got %v", c.contents)
	}
}

func TestNotifierMessageContainsDingTalkKeyword(t *testing.T) {
	c := &capture{}
	server := c.server()
	defer server.Close()

	n := New(server.URL, "inst-a", 10, 3, nil)
	n.Process(context.Background(), events("error", 3, 18, 0, ""))

	if got := c.matching("\u544a\u8b66"); got != 1 {
		t.Fatalf("expected DingTalk keyword in alert message, got %d", got)
	}
}

func TestNotifierProcessStats(t *testing.T) {
	c := &capture{}
	server := c.server()
	defer server.Close()
	n := New(server.URL, "inst-a", 10, 3, nil)

	stats := n.Process(context.Background(), append(
		events("error", 3, 26, 9, "alice"),
		events("consume", 1, 26, 9, "alice")...,
	))

	if stats.EventCount != 4 || stats.ErrorCount != 3 {
		t.Fatalf("unexpected event stats: %#v", stats)
	}
	if stats.ChannelDimensions != 1 || stats.UserDimensions != 1 {
		t.Fatalf("unexpected dimension stats: %#v", stats)
	}
	if stats.AlertsTriggered != 2 || stats.AlertsSent != 2 || stats.AlertsSendFailures != 0 {
		t.Fatalf("unexpected alert stats: %#v", stats)
	}
}

func timedEvents(startID int64, at time.Time, logType string, count int, channelID int64) []logcollector.Event {
	items := make([]logcollector.Event, 0, count)
	for i := 0; i < count; i++ {
		e := event(startID+int64(i), logType, channelID, 0, "")
		e.CreatedAt = at.Add(time.Duration(i) * time.Second)
		items = append(items, e)
	}
	return items
}

func TestNotifierWindowTimeDecayReArmsSparseDimension(t *testing.T) {
	c := &capture{}
	server := c.server()
	defer server.Close()
	n := New(server.URL, "inst-a", 10, 3, nil).WithWindowMaxAge(time.Hour)
	base := time.Date(2026, 7, 12, 3, 0, 0, 0, time.UTC)
	n.now = func() time.Time { return base }

	// Episode 1: sparse channel, three errors, no successes ever.
	n.Process(context.Background(), timedEvents(1, base, "error", 3, 26))
	if got := c.matching("渠道错误激增"); got != 1 {
		t.Fatalf("expected first episode alert, got %d", got)
	}

	// 2 hours later the stale errors decay out of the window and the
	// dimension re-arms even without any successful requests.
	n.now = func() time.Time { return base.Add(2 * time.Hour) }
	n.Process(context.Background(), nil)

	// Episode 2: a fresh error burst must alert again.
	n.Process(context.Background(), timedEvents(100, base.Add(2*time.Hour), "error", 3, 26))
	if got := c.matching("渠道错误激增"); got != 2 {
		t.Fatalf("expected second episode alert after decay, got %d (%v)", got, c.contents)
	}
}

func TestNotifierRemindsWhileEpisodeKeepsFiring(t *testing.T) {
	c := &capture{}
	server := c.server()
	defer server.Close()
	n := New(server.URL, "inst-a", 10, 3, nil).WithRemindInterval(time.Hour)
	base := time.Date(2026, 7, 12, 3, 0, 0, 0, time.UTC)
	n.now = func() time.Time { return base }

	n.Process(context.Background(), timedEvents(1, base, "error", 3, 26))
	if got := c.matching("渠道错误激增"); got != 1 {
		t.Fatalf("expected initial alert, got %d", got)
	}

	// Half an hour later: episode still firing, no reminder yet.
	n.now = func() time.Time { return base.Add(30 * time.Minute) }
	n.Process(context.Background(), timedEvents(50, base.Add(30*time.Minute), "error", 2, 26))
	if got := c.matching("渠道错误持续"); got != 0 {
		t.Fatalf("expected no reminder before interval, got %d", got)
	}

	// Past the interval: one reminder with cumulative episode errors.
	n.now = func() time.Time { return base.Add(61 * time.Minute) }
	n.Process(context.Background(), nil)
	if got := c.matching("渠道错误持续"); got != 1 {
		t.Fatalf("expected one reminder, got %d (%v)", got, c.contents)
	}
	if got := c.matching("累计 5 条错误"); got != 1 {
		t.Fatalf("expected cumulative episode errors in reminder, got %v", c.contents)
	}

	// Shortly after: no duplicate reminder.
	n.now = func() time.Time { return base.Add(70 * time.Minute) }
	n.Process(context.Background(), nil)
	if got := c.matching("渠道错误持续"); got != 1 {
		t.Fatalf("expected no duplicate reminder, got %d", got)
	}
}

func TestNotifierRetriesFailedReminder(t *testing.T) {
	c := &capture{}
	server := c.server()
	defer server.Close()
	n := New(server.URL, "inst-a", 10, 3, nil).WithRemindInterval(time.Hour)
	base := time.Date(2026, 7, 12, 3, 0, 0, 0, time.UTC)
	n.now = func() time.Time { return base }

	n.Process(context.Background(), timedEvents(1, base, "error", 3, 26))

	c.setErrcode(`{"errcode":310000,"errmsg":"keywords not in content"}`)
	n.now = func() time.Time { return base.Add(61 * time.Minute) }
	n.Process(context.Background(), nil)

	c.setErrcode("")
	n.Process(context.Background(), nil)
	if got := c.matching("渠道错误持续"); got != 2 {
		t.Fatalf("expected rejected then retried reminder, got %d (%v)", got, c.contents)
	}
}

func TestNotifierCountsEventsWithNullCreatedAt(t *testing.T) {
	c := &capture{}
	server := c.server()
	defer server.Close()
	n := New(server.URL, "inst-a", 10, 3, nil).WithWindowMaxAge(time.Hour)
	base := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	n.now = func() time.Time { return base }

	// NULL created_at is coalesced to 0 by the collector, which arrives here
	// as unix epoch. Such errors must still count instead of being pruned by
	// time decay immediately.
	items := make([]logcollector.Event, 0, 3)
	for i := int64(1); i <= 3; i++ {
		e := event(i, "error", 26, 0, "")
		e.CreatedAt = time.Unix(0, 0)
		items = append(items, e)
	}
	n.Process(context.Background(), items)
	if got := c.matching("渠道错误激增"); got != 1 {
		t.Fatalf("expected epoch-timestamp errors to trigger alert, got %d (%v)", got, c.contents)
	}
}

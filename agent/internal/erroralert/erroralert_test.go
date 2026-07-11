package erroralert

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"controltower/agent/internal/logcollector"
)

type capture struct {
	mu       sync.Mutex
	errcode  string
	contents []string
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

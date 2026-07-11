package erroralert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"controltower/agent/internal/logcollector"
)

const (
	DefaultWindow    = 10
	DefaultThreshold = 3
)

// Notifier keeps a sliding window of recent request outcomes per channel and
// per user, collected directly from the source logs table, and pushes a
// DingTalk group message when a dimension reaches the error threshold within
// its window. State lives in memory only: a restart starts with empty windows.
type Notifier struct {
	webhookURL string
	instanceID string
	window     int
	threshold  int
	client     *http.Client
	now        func() time.Time
	logf       func(format string, args ...any)

	mu           sync.Mutex
	states       map[string]*dimensionState
	channelNames map[int64]string
}

type dimensionState struct {
	title            string
	label            string
	outcomes         []bool // true = error; newest last, capped at window
	lastErrorSummary string
	alerted          bool
}

func New(webhookURL string, instanceID string, window int, threshold int, logf func(format string, args ...any)) *Notifier {
	if window <= 0 {
		window = DefaultWindow
	}
	if threshold <= 0 {
		threshold = DefaultThreshold
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Notifier{
		webhookURL: webhookURL,
		instanceID: instanceID,
		window:     window,
		threshold:  threshold,
		client:     &http.Client{Timeout: 5 * time.Second},
		now:        time.Now,
		logf:       logf,
		states:     make(map[string]*dimensionState),
	}
}

// Process feeds newly collected events (ordered oldest first, exactly as the
// log collector returns them) into the windows, then sends one message per
// dimension that is at or above the threshold and has not alerted yet in the
// current episode. A dimension re-arms once its window drops back below the
// threshold. Failed sends re-arm immediately so the next pass retries.
func (n *Notifier) Process(ctx context.Context, events []logcollector.Event) {
	if n == nil {
		return
	}
	n.mu.Lock()
	for _, event := range events {
		if event.ChannelID > 0 {
			key := "channel:" + strconv.FormatInt(event.ChannelID, 10)
			n.observeLocked(key, "渠道错误激增", n.channelLabelLocked(event.ChannelID), event)
		}
		if event.UserID > 0 {
			label := fmt.Sprintf("客户 %d", event.UserID)
			if event.Username != "" {
				label = fmt.Sprintf("客户 %s(%d)", event.Username, event.UserID)
			}
			key := "user:" + strconv.FormatInt(event.UserID, 10)
			n.observeLocked(key, "客户错误激增", label, event)
		}
	}
	pending := n.evaluateLocked()
	n.mu.Unlock()

	for _, message := range pending {
		if err := n.send(ctx, message.content); err != nil {
			n.logf("control tower dingtalk alert failed: %v", err)
			n.mu.Lock()
			if state, ok := n.states[message.key]; ok {
				state.alerted = false
			}
			n.mu.Unlock()
		}
	}
}

// UpdateChannelNames replaces the channel id to name mapping used in alert
// labels. Existing window labels refresh as new events for the dimension
// arrive.
func (n *Notifier) UpdateChannelNames(names map[int64]string) {
	if n == nil || names == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.channelNames = names
}

func (n *Notifier) channelLabelLocked(channelID int64) string {
	if name := n.channelNames[channelID]; name != "" {
		return fmt.Sprintf("渠道 %d(%s)", channelID, name)
	}
	return fmt.Sprintf("渠道 %d", channelID)
}

func (n *Notifier) observeLocked(key string, title string, label string, event logcollector.Event) {
	state := n.states[key]
	if state == nil {
		state = &dimensionState{title: title, label: label}
		n.states[key] = state
	}
	state.label = label
	isError := event.LogType == "error"
	state.outcomes = append(state.outcomes, isError)
	if len(state.outcomes) > n.window {
		state.outcomes = state.outcomes[len(state.outcomes)-n.window:]
	}
	if isError && event.ErrorSummary != "" {
		state.lastErrorSummary = truncate(event.ErrorSummary, 120)
	}
}

type pendingMessage struct {
	key     string
	content string
}

func (n *Notifier) evaluateLocked() []pendingMessage {
	var pending []pendingMessage
	for key, state := range n.states {
		errors := 0
		for _, isError := range state.outcomes {
			if isError {
				errors++
			}
		}
		if errors < n.threshold {
			state.alerted = false
			continue
		}
		if state.alerted {
			continue
		}
		state.alerted = true
		content := fmt.Sprintf("【Control Tower 告警】%s\n实例: %s\n%s 最近 %d 条请求中 %d 条失败",
			state.title, n.instanceID, state.label, len(state.outcomes), errors)
		if state.lastErrorSummary != "" {
			content += "\n最新错误: " + state.lastErrorSummary
		}
		content += "\n时间: " + n.now().Local().Format("2006-01-02 15:04:05")
		pending = append(pending, pendingMessage{key: key, content: content})
	}
	return pending
}

func (n *Notifier) send(ctx context.Context, content string) error {
	payload := map[string]any{"msgtype": "text", "text": map[string]string{"content": content}}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.webhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("dingtalk webhook returned HTTP %d", resp.StatusCode)
	}
	// DingTalk robots answer HTTP 200 even on rejection; the real outcome is
	// in the errcode field of the response body.
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fmt.Errorf("read dingtalk response: %w", err)
	}
	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil
	}
	if result.ErrCode != 0 {
		return fmt.Errorf("dingtalk errcode %d: %s", result.ErrCode, result.ErrMsg)
	}
	return nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

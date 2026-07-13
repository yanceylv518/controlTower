package erroralert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
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
	client         *http.Client
	now            func() time.Time
	logf           func(format string, args ...any)
	windowMaxAge   time.Duration // 0 = no time decay
	remindInterval time.Duration // 0 = no reminders

	mu           sync.Mutex
	states       map[string]*dimensionState
	channelNames map[int64]string
}

type dimensionState struct {
	title            string
	label            string
	outcomes         []outcome // newest last, capped at window
	lastErrorSummary string
	alerted          bool
	episodeStartAt   time.Time
	episodeErrors    int
	lastRemindAt     time.Time
}

type outcome struct {
	isError bool
	at      time.Time
}

// ProcessStats describes one alert evaluation pass without exposing log
// contents or other request-sensitive fields.
type ProcessStats struct {
	EventCount          int
	ErrorCount          int
	ChannelDimensions   int
	UserDimensions      int
	AlertsTriggered     int
	AlertsSent          int
	AlertsSendFailures  int
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

// WithWindowMaxAge drops window entries older than maxAge during evaluation,
// so stale errors on low-traffic dimensions eventually leave the window and
// the episode re-arms. Zero disables time decay.
func (n *Notifier) WithWindowMaxAge(maxAge time.Duration) *Notifier {
	n.windowMaxAge = maxAge
	return n
}

// WithRemindInterval re-sends a reminder message while an episode keeps
// firing, so a dimension that never recovers is not silent forever after its
// first alert. Zero disables reminders.
func (n *Notifier) WithRemindInterval(interval time.Duration) *Notifier {
	n.remindInterval = interval
	return n
}

// Process feeds newly collected events (ordered oldest first, exactly as the
// log collector returns them) into the windows, then sends one message per
// dimension that is at or above the threshold and has not alerted yet in the
// current episode. A dimension re-arms once its window drops back below the
// threshold. Failed sends re-arm immediately so the next pass retries.
func (n *Notifier) Process(ctx context.Context, events []logcollector.Event) ProcessStats {
	var stats ProcessStats
	if n == nil {
		return stats
	}
	stats.EventCount = len(events)
	channelDimensions := make(map[int64]struct{})
	userDimensions := make(map[int64]struct{})
	n.mu.Lock()
	for _, event := range events {
		if event.LogType == "error" {
			stats.ErrorCount++
		}
		if event.ChannelID > 0 {
			channelDimensions[event.ChannelID] = struct{}{}
			key := "channel:" + strconv.FormatInt(event.ChannelID, 10)
			n.observeLocked(key, "渠道错误激增", n.channelLabelLocked(event.ChannelID), event)
		}
		if event.UserID > 0 {
			userDimensions[event.UserID] = struct{}{}
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
	stats.ChannelDimensions = len(channelDimensions)
	stats.UserDimensions = len(userDimensions)
	stats.AlertsTriggered = len(pending)

	for _, message := range pending {
		if err := n.send(ctx, message.content); err != nil {
			stats.AlertsSendFailures++
			n.logf("control tower dingtalk alert failed: %v", err)
			n.mu.Lock()
			if state, ok := n.states[message.key]; ok {
				switch message.kind {
				case "remind":
					state.lastRemindAt = message.prevRemindAt
				default:
					state.alerted = false
				}
			}
			n.mu.Unlock()
			continue
		}
		stats.AlertsSent++
	}
	return stats
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
	at := event.CreatedAt
	// Unix 0 covers NULL created_at coalesced to 0 by the collector; without
	// this, such an event would enter the window with a 1970 timestamp and be
	// silently dropped by time decay before it could ever count.
	if at.IsZero() || at.Unix() <= 0 {
		at = n.now()
	}
	state.outcomes = append(state.outcomes, outcome{isError: isError, at: at})
	if len(state.outcomes) > n.window {
		state.outcomes = state.outcomes[len(state.outcomes)-n.window:]
	}
	if isError {
		if state.alerted {
			state.episodeErrors++
		}
		if event.ErrorSummary != "" {
			state.lastErrorSummary = truncate(event.ErrorSummary, 120)
		}
	}
}

type pendingMessage struct {
	key          string
	content      string
	kind         string // "alert" or "remind"
	prevRemindAt time.Time
}

func (n *Notifier) evaluateLocked() []pendingMessage {
	now := n.now()
	var pending []pendingMessage
	for key, state := range n.states {
		if n.windowMaxAge > 0 {
			cutoff := now.Add(-n.windowMaxAge)
			kept := state.outcomes[:0]
			for _, item := range state.outcomes {
				if item.at.After(cutoff) {
					kept = append(kept, item)
				}
			}
			state.outcomes = kept
		}
		errors := 0
		for _, item := range state.outcomes {
			if item.isError {
				errors++
			}
		}
		if errors < n.threshold {
			state.alerted = false
			state.episodeErrors = 0
			state.episodeStartAt = time.Time{}
			state.lastRemindAt = time.Time{}
			if len(state.outcomes) == 0 {
				delete(n.states, key)
			}
			continue
		}
		if !state.alerted {
			state.alerted = true
			state.episodeStartAt = now
			state.episodeErrors = errors
			state.lastRemindAt = now
			n.logf("control tower alert trigger: dimension=%s kind=alert window=%d errors=%d", key, len(state.outcomes), errors)
			pending = append(pending, pendingMessage{key: key, kind: "alert", content: n.alertContent(state, errors, now)})
			continue
		}
		if n.remindInterval > 0 && now.Sub(state.lastRemindAt) >= n.remindInterval {
			prev := state.lastRemindAt
			state.lastRemindAt = now
			n.logf("control tower alert trigger: dimension=%s kind=remind window=%d errors=%d episode_errors=%d", key, len(state.outcomes), errors, state.episodeErrors)
			pending = append(pending, pendingMessage{key: key, kind: "remind", prevRemindAt: prev, content: n.remindContent(state, errors, now)})
		}
	}
	return pending
}

func (n *Notifier) alertContent(state *dimensionState, errors int, now time.Time) string {
	content := fmt.Sprintf("[\u544a\u8b66] 【Control Tower 告警】%s\n实例: %s\n%s 最近 %d 条请求中 %d 条失败",
		state.title, n.instanceID, state.label, len(state.outcomes), errors)
	if state.lastErrorSummary != "" {
		content += "\n最新错误: " + state.lastErrorSummary
	}
	content += "\n时间: " + now.Local().Format("2006-01-02 15:04:05")
	return content
}

func (n *Notifier) remindContent(state *dimensionState, errors int, now time.Time) string {
	title := strings.TrimSuffix(state.title, "激增") + "持续"
	content := fmt.Sprintf("[\u544a\u8b66] 【Control Tower 告警】%s\n实例: %s\n%s 自 %s 起持续异常，累计 %d 条错误，最近 %d 条请求中 %d 条失败",
		title, n.instanceID, state.label, state.episodeStartAt.Local().Format("01-02 15:04"), state.episodeErrors, len(state.outcomes), errors)
	if state.lastErrorSummary != "" {
		content += "\n最新错误: " + state.lastErrorSummary
	}
	content += "\n时间: " + now.Local().Format("2006-01-02 15:04:05")
	return content
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

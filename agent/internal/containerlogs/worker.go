package containerlogs

import (
	"bytes"
	"compress/gzip"
	"context"
	cl "controltower/internal/containerlog"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

// Run serializes queries independently of metric collection. Unacknowledged
// results are retried; a lost claim response expires on Server, never re-executes.
func Run(ctx context.Context, server, token, agent, socket string) {
	local := &http.Client{Timeout: 40 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	remote := &http.Client{Timeout: 15 * time.Second}
	var pending *cl.Result
	var taskID string
	var active *automaticQuery
	delay := 5 * time.Second
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		inventory := cl.Inventory{}
		if err := request(ctx, local, "GET", "http://unix/containers", "", nil, &inventory); err != nil {
			inventory.Error = "本机日志读取服务不可用，请检查服务运行状态"
		}
		poll := cl.Poll{AgentID: agent, Sources: inventory.Sources, DiscoveryError: inventory.Error, TaskID: taskID, Result: pending}
		var response struct {
			Task *cl.Task `json:"task"`
		}
		if err := request(ctx, remote, "POST", strings.TrimRight(server, "/")+"/api/agent/container-logs/poll", token, poll, &response); err != nil {
			log.Printf("container logs: poll failed: %v", err)
			delay = 5 * time.Second
			continue
		}
		pending = nil
		taskID = ""
		// Server echoes a running task only to acknowledge its owner's progress.
		// A missing/different task means the lease ended; never keep scanning it.
		if response.Task == nil {
			active = nil
			delay = 5 * time.Second
			continue
		}
		if active == nil || active.task.ID != response.Task.ID {
			active = newAutomaticQuery(*response.Task)
		}
		result := active.step(ctx, func(stepCtx context.Context, q cl.Query) cl.Result { return executeQuery(stepCtx, local, q) })
		pending = &result
		taskID = active.task.ID
		delay = time.Second
		if result.Status != "running" {
			cursor := active.releaseCursor
			if cursor == "" {
				cursor = active.query.Cursor
			}
			if cursor != "" {
				var ack map[string]any
				_ = request(ctx, local, "POST", "http://unix/release", "", map[string]string{"cursor": cursor}, &ack)
			}
			active = nil
		}
	}
}

// executeQuery decodes the reader's reply into a fresh Result. Decoding into a
// pre-filled failure value would keep its error text on success, because the
// reader omits empty fields.
func executeQuery(ctx context.Context, local *http.Client, q cl.Query) cl.Result {
	if q.Validate(time.Now().UTC()) != nil {
		return cl.Result{Status: "failed", Lines: []string{}, Error: "本机日志读取服务不可用"}
	}
	var result cl.Result
	if err := request(ctx, local, "POST", "http://unix/query", "", q, &result); err != nil {
		return cl.Result{Status: "failed", Lines: []string{}, Error: "本机日志读取失败或超时"}
	}
	if result.Lines == nil {
		result.Lines = []string{}
	}
	if result.Status == "" {
		result.Status = "failed"
		result.Error = "本机日志读取服务返回不完整"
	}
	return result
}

func request(ctx context.Context, c *http.Client, method, url, token string, input, output any) error {
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return err
		}
		if token != "" {
			var compressed bytes.Buffer
			zip := gzip.NewWriter(&compressed)
			if _, err = zip.Write(b); err != nil {
				return err
			}
			if err = zip.Close(); err != nil {
				return err
			}
			b = compressed.Bytes()
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		if input != nil {
			req.Header.Set("Content-Encoding", "gzip")
		}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("connection unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4*1024*1024)).Decode(output)
}

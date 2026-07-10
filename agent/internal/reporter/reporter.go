package reporter

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func NewClient(baseURL string, token string, timeout time.Duration) Client {
	return Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c Client) Heartbeat(ctx context.Context, heartbeat AgentHeartbeatRequest) (AgentHeartbeatResponse, error) {
	var response AgentHeartbeatResponse
	err := c.postJSONResponse(ctx, "/api/agent/heartbeat", heartbeat, "control tower heartbeat", &response)
	return response, err
}

func (c Client) Report(ctx context.Context, report AgentReportRequest) error {
	return c.postJSON(ctx, "/api/agent/report", report, "control tower report")
}

func (c Client) postJSON(ctx context.Context, path string, value any, label string) error {
	return c.postJSONResponse(ctx, path, value, label, nil)
}

func (c Client) postJSONResponse(ctx context.Context, path string, value any, label string, target any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	body, err := gzipBytes(data)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s failed with status %d", label, resp.StatusCode)
	}
	if target != nil {
		if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
			return fmt.Errorf("%s decode response: %w", label, err)
		}
	}
	return nil
}

func gzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(data); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

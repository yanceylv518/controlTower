package healthcheck

import (
	"context"
	"net/http"
	"strings"
	"time"

	"controltower/agent/internal/reporter"
)

type Checker struct {
	client *http.Client
	now    func() time.Time
}

func New(timeout time.Duration) Checker {
	return Checker{
		client: &http.Client{Timeout: timeout},
		now:    time.Now,
	}
}

func (c Checker) Check(ctx context.Context, target string) reporter.HealthCheckPayload {
	checkedAt := c.now().UTC()
	payload := reporter.HealthCheckPayload{
		CheckedAt: checkedAt,
		Target:    target,
		Status:    "down",
	}
	if strings.TrimSpace(target) == "" {
		payload.ErrorSummary = "missing health check target"
		return payload
	}

	started := c.now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		payload.ErrorSummary = summarizeError(err)
		return payload
	}
	resp, err := c.client.Do(req)
	payload.LatencyMS = int64(c.now().Sub(started) / time.Millisecond)
	if err != nil {
		payload.ErrorSummary = summarizeError(err)
		return payload
	}
	defer resp.Body.Close()

	payload.HTTPStatusCode = resp.StatusCode
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		payload.Status = "up"
	}
	return payload
}

func summarizeError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	message = strings.ReplaceAll(message, "\r", " ")
	message = strings.ReplaceAll(message, "\n", " ")
	if len(message) > 200 {
		message = message[:200]
	}
	return message
}

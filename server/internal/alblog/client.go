// Package alblog reads ALB access-log metadata from Alibaba Cloud SLS.
// Wire contract: GetLogsV2 and the documented SLS V1 request signature.
// No raw request/response bodies or log records are returned to the browser.
package alblog

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"controltower/server/internal/secrets"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	SecretKey string
	HTTP      *http.Client
	Now       func() time.Time
}

func (c Client) Probe(ctx context.Context, cfg Config) Result {
	now := time.Now().UTC()
	if c.Now != nil {
		now = c.Now().UTC()
	}
	result := Result{Status: "failed", TestedAt: now.Format(time.RFC3339), From: now.Add(-15 * time.Minute).Unix(), To: now.Unix()}
	fail := func(code string) Result {
		result.Status = "failed"
		result.RequestCount = nil
		result.LatestLogTime = nil
		result.Code = code
		return result
	}
	invalid := func(stage string) Result {
		// Fixed stage labels only: no credentials, response body, SQL or log records.
		log.Printf("ALB access log probe: invalid SLS response (stage=%s)", stage)
		return fail("alb_invalid_response")
	}
	if cfg.Normalize() != nil {
		return fail("alb_invalid_config")
	}
	secret, err := secrets.Decrypt(c.SecretKey, cfg.SecretCipher)
	if err != nil || secret == "" {
		return fail("alb_secret_unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// Fixed, bounded aggregate; the validated ALB ID cannot inject SQL.
	query := fmt.Sprintf("* | SELECT count(*) AS request_count, max(__time__) AS latest_log_time, count(request_length) AS sized_requests FROM log WHERE app_lb_id = '%s' LIMIT 1", cfg.ALBID)
	body, _ := json.Marshal(map[string]any{"from": result.From, "to": result.To, "query": query, "line": 0, "offset": 0})
	path := "/logstores/" + cfg.Logstore + "/logs"
	hc := http.Client{Timeout: 15 * time.Second}
	if c.HTTP != nil {
		hc = *c.HTTP
	}
	// Never forward a signed credential to a redirect target.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+cfg.Project+"."+cfg.Endpoint+path, bytes.NewReader(body))
		if err != nil {
			return fail("alb_invalid_config")
		}
		req.Header.Set("Accept-Encoding", "gzip")
		sign(req, body, cfg.AccessKeyID, secret, now)
		resp, err := hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return fail("alb_timeout")
			}
			return fail("alb_network_failed")
		}
		data, readErr := readResponse(resp)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			switch resp.StatusCode {
			case 401, 403:
				return fail("alb_auth_failed")
			case 404:
				return fail("alb_source_not_found")
			case 400:
				return fail("alb_query_failed")
			case 429:
				return fail("alb_rate_limited")
			default:
				return fail("alb_service_failed")
			}
		}
		if readErr != nil {
			return invalid("decompress_or_read")
		}
		var reply struct {
			Meta struct {
				Progress string `json:"progress"`
			} `json:"meta"`
			Data []map[string]json.RawMessage `json:"data"`
		}
		if json.Unmarshal(data, &reply) != nil {
			return invalid("json")
		}
		if reply.Meta.Progress == "Incomplete" {
			if attempt == 2 {
				result.Status = "incomplete"
				result.Code = "alb_query_incomplete"
				return result
			}
			timer := time.NewTimer(200 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return fail("alb_timeout")
			case <-timer.C:
			}
			continue
		}
		if reply.Meta.Progress != "Complete" || len(reply.Data) != 1 {
			return invalid("progress_or_rows")
		}
		count, ok := number(reply.Data[0]["request_count"])
		if !ok || count < 0 {
			return invalid("request_count")
		}
		result.RequestCount = &count
		result.Status = "success"
		if count == 0 {
			result.Status = "no_data"
			return result
		}
		latest, ok := number(reply.Data[0]["latest_log_time"])
		if !ok || latest < result.From || latest >= result.To {
			result.RequestCount = nil
			return invalid("latest_log_time")
		}
		result.LatestLogTime = &latest
		return result
	}
	return fail("alb_query_incomplete")
}

func number(raw json.RawMessage) (int64, bool) {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		s = string(raw)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

func readResponse(resp *http.Response) ([]byte, error) {
	return readResponseLimit(resp, 256*1024)
}

func readResponseLimit(resp *http.Response, limit int64) ([]byte, error) {
	// Bound both the wire body and the decoded body. Never allocate using a
	// server-provided raw-size header.
	data, err := readBounded(resp.Body, limit)
	if err != nil {
		return nil, err
	}
	encoding := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	serviceEncoding := strings.ToLower(strings.TrimSpace(resp.Header.Get("x-log-compresstype")))
	if encoding == "" || encoding == "identity" {
		encoding = serviceEncoding
	}
	if encoding == "" || encoding == "identity" {
		return data, nil
	}
	var decoder io.ReadCloser
	switch encoding {
	case "gzip", "deflate":
		// SLS's x-log-compresstype=gzip can carry a zlib stream. This matches
		// aliyun-log-python-sdk/aliyun/log/compress.py:decompress_response.
		// Retain support for standard HTTP gzip and ordinary uncompressed JSON.
		if encoding == "gzip" && len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
			decoder, err = gzip.NewReader(bytes.NewReader(data))
		} else {
			decoder, err = zlib.NewReader(bytes.NewReader(data))
		}
	default:
		return nil, fmt.Errorf("unsupported response compression")
	}
	if err != nil {
		return nil, err
	}
	defer decoder.Close()
	return readBounded(decoder, limit)
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response too large")
	}
	return data, err
}

// V1 signs the body digest, content type, RFC1123 date and sorted x-log headers.
func sign(req *http.Request, body []byte, id, secret string, now time.Time) {
	digest := fmt.Sprintf("%X", md5.Sum(body))
	date := now.UTC().Format(http.TimeFormat)
	size := strconv.Itoa(len(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-MD5", digest)
	req.Header.Set("Date", date)
	req.Header.Set("x-log-apiversion", "0.6.0")
	req.Header.Set("x-log-bodyrawsize", size)
	req.Header.Set("x-log-signaturemethod", "hmac-sha1")
	canonical := strings.Join([]string{req.Method, digest, "application/json", date, "x-log-apiversion:0.6.0", "x-log-bodyrawsize:" + size, "x-log-signaturemethod:hmac-sha1", req.URL.EscapedPath()}, "\n")
	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write([]byte(canonical))
	req.Header.Set("Authorization", "LOG "+id+":"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
}

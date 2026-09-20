// Package voicealert implements customer TPM monitoring and durable voice delivery.
package voicealert

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// RPC specification: https://help.aliyun.com/zh/vms/the-http-protocol-and-signature
// SingleCallByTts: Dyvmsapi/2017-05-25. OutId is correlation, NOT idempotency.
type Aliyun struct {
	AccessKeyID, AccessKeySecret, SecurityToken string
	HTTP                                        *http.Client
}
type Result struct{ Status, Code, CallID, RequestID string }

func AliyunFromEnv() *Aliyun {
	return &Aliyun{AccessKeyID: os.Getenv("CT_VOICE_ACCESS_KEY_ID"), AccessKeySecret: os.Getenv("CT_VOICE_ACCESS_KEY_SECRET"), SecurityToken: os.Getenv("CT_VOICE_SECURITY_TOKEN")}
}
func (a *Aliyun) Ready() bool { return a != nil && a.AccessKeyID != "" && a.AccessKeySecret != "" }
func encode(s string) string  { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }
func signature(method string, p url.Values, secret string) string {
	canonical := strings.ReplaceAll(p.Encode(), "+", "%20")
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	_, _ = mac.Write([]byte(method + "&%2F&" + encode(canonical)))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
func randomID(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// No automatic retries: a timeout may follow a successfully placed call.
// Public mode omits CalledShowNumber. Never log signed requests or raw errors.
func (a *Aliyun) Call(ctx context.Context, c Config, t Target, id, direction string) Result {
	return a.CallTemplate(ctx, c, t, id, map[string]string{"customer": t.Label, "direction": direction})
}

func (a *Aliyun) CallTemplate(ctx context.Context, c Config, t Target, id string, variables map[string]string) Result {
	if !a.Ready() {
		return Result{Status: "rejected", Code: "credentials_missing"}
	}
	nonce, err := randomID(16)
	if err != nil {
		return Result{Status: "rejected", Code: "nonce_failed"}
	}
	params, _ := json.Marshal(variables)
	p := url.Values{"Action": {"SingleCallByTts"}, "Version": {"2017-05-25"}, "RegionId": {"cn-hangzhou"}, "Format": {"JSON"}, "AccessKeyId": {a.AccessKeyID}, "SignatureMethod": {"HMAC-SHA1"}, "SignatureVersion": {"1.0"}, "SignatureNonce": {nonce}, "Timestamp": {time.Now().UTC().Format("2006-01-02T15:04:05Z")}, "CalledNumber": {t.Phone}, "TtsCode": {c.TtsCode}, "TtsParam": {string(params)}, "OutId": {id}, "PlayTimes": {"2"}}
	if c.CalledShowNumber != "" {
		p.Set("CalledShowNumber", c.CalledShowNumber)
	}
	if a.SecurityToken != "" {
		p.Set("SecurityToken", a.SecurityToken)
	}
	p.Set("Signature", signature("POST", p, a.AccessKeySecret))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://dyvmsapi.aliyuncs.com/", strings.NewReader(p.Encode()))
	if err != nil {
		return Result{Status: "rejected", Code: "request_failed"}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if a.HTTP != nil {
		client.Transport = a.HTTP.Transport
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{Status: "unknown", Code: "transport_unknown"}
	}
	defer resp.Body.Close()
	var body struct{ Code, CallId, RequestId string }
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return Result{Status: "unknown", Code: "response_unknown"}
	}
	result := Result{Status: "unknown", Code: body.Code, CallID: body.CallId, RequestID: body.RequestId}
	if len(result.Code) > 128 {
		result.Code = "invalid_response_code"
	}
	if len(result.CallID) > 128 {
		result.CallID = ""
	}
	if len(result.RequestID) > 128 {
		result.RequestID = ""
	}
	if resp.StatusCode == 200 && body.Code == "OK" && result.CallID != "" {
		result.Status = "accepted"
	} else if body.Code != "" && body.Code != "OK" {
		result.Status = "rejected"
	}
	return result
}

package voicealert

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRPCEncoding(t *testing.T) {
	if got := encode("a b+*~中文"); got != "a%20b%2B%2A~%E4%B8%AD%E6%96%87" {
		t.Fatal(got)
	}
	// Fixed vector generated independently using Python hmac/hashlib and the
	// documented canonical POST string; protects key suffix and double escaping.
	p := url.Values{"Action": {"SingleCallByTts"}, "TtsParam": {"{\"customer\":\"A B\"}"}}
	if got := signature("POST", p, "testSecret"); got != "sxbwqyknL0Uu7DmwmInkhDhJGI0=" {
		t.Fatalf("signature=%s", got)
	}
}
func TestSingleCallContract(t *testing.T) {
	for _, show := range []string{"", "057112345678"} {
		count := 0
		a := &Aliyun{AccessKeyID: "test-id", AccessKeySecret: "test-secret", SecurityToken: "sts", HTTP: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			count++
			if r.Method != "POST" || r.URL.String() != "https://dyvmsapi.aliyuncs.com/" {
				t.Fatal("wrong endpoint/method")
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			p := r.PostForm
			for key, want := range map[string]string{"Action": "SingleCallByTts", "Version": "2017-05-25", "RegionId": "cn-hangzhou", "Format": "JSON", "CalledNumber": "13800000000", "TtsCode": "TTS_test", "TtsParam": "{\"customer\":\"user-one\",\"direction\":\"上涨\"}", "OutId": "12345678901234", "SecurityToken": "sts", "PlayTimes": "2"} {
				if p.Get(key) != want {
					t.Fatalf("%s=%q", key, p.Get(key))
				}
			}
			if show == "" {
				if _, ok := p["CalledShowNumber"]; ok {
					t.Fatal("public mode must omit caller")
				}
			} else if p.Get("CalledShowNumber") != show {
				t.Fatal("dedicated caller missing")
			}
			sig := p.Get("Signature")
			p.Del("Signature")
			if sig != signature("POST", p, "test-secret") {
				t.Fatal("bad signature")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"Code":"OK","CallId":"call-1","RequestId":"request-1"}`)), Header: http.Header{}}, nil
		})}}
		got := a.Call(context.Background(), Config{TtsCode: "TTS_test", CalledShowNumber: show}, Target{Label: "user-one", Phone: "13800000000"}, "12345678901234", "上涨")
		if count != 1 || got.Status != "accepted" || got.CallID != "call-1" {
			t.Fatal(got, count)
		}
	}
}
func TestUnknownAndRejectedNeverRetry(t *testing.T) {
	for _, tc := range []struct {
		body, status string
		network      bool
	}{{`{"Code":"isv.BUSINESS_LIMIT_CONTROL"}`, "rejected", false}, {`{"Code":"OK"}`, "unknown", false}, {`<html>`, "unknown", false}, {"", "unknown", true}} {
		count := 0
		a := &Aliyun{AccessKeyID: "id", AccessKeySecret: "secret", HTTP: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			count++
			if tc.network {
				return nil, errors.New("secret transport error")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}}
		got := a.Call(context.Background(), Config{}, Target{}, "test", "下降")
		if count != 1 || got.Status != tc.status || strings.Contains(got.Code, "secret") {
			t.Fatal(got, count)
		}
	}
}

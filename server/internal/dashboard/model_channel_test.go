package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"controltower/server/internal/aggregator"
)

type modelChannelNameSource struct {
	nameSourceFake
}

func (s *modelChannelNameSource) ChannelNames(instanceID string) (map[int64]string, error) {
	s.calls[instanceID]++
	return map[int64]string{5: instanceID + " 主渠道"}, nil
}

func TestModelChannelNamesKeepModelAndInstanceBoundaries(t *testing.T) {
	source := &modelChannelNameSource{nameSourceFake{calls: map[string]int{}}}
	h := NewHandler(nil).WithNameSource(source)
	for _, tc := range []struct{ key, want string }{
		{"a:model:gpt-4o:channel:5", "a 主渠道"},
		{"b:model:gpt-4o:channel:5", "b 主渠道"},
		{"site:a:model:provider:model:inner:channel:6:gpt:channel:5", "site:a 主渠道"},
		{"a:model:gpt-4o:channel:99", "渠道 99"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			if got := h.displayDimensionName("instance_model_channel", tc.key); got != tc.want {
				t.Fatalf("display name = %q, want %q", got, tc.want)
			}
			if got := h.displayDimensionKey("instance_model_channel", tc.key); got != tc.want {
				t.Fatalf("display key = %q, want %q", got, tc.want)
			}
		})
	}
	for _, instanceID := range []string{"a", "b", "site:a"} {
		if source.calls[instanceID] != 1 {
			t.Fatalf("channel names cache calls = %v", source.calls)
		}
	}
	if got := (Handler{}).displayDimensionName("instance_model_channel", "a:model:provider:gpt:channel:5"); got != "渠道 5" {
		t.Fatalf("fallback without name source = %q", got)
	}
}

func TestModelChannelNamesRejectMalformedDimensions(t *testing.T) {
	source := &modelChannelNameSource{nameSourceFake{calls: map[string]int{}}}
	h := NewHandler(nil).WithNameSource(source)
	for _, key := range []string{
		"a:model:gpt-4o", "a:model::channel:5", ":model:gpt-4o:channel:5",
		"a:channel:5", "a:model:gpt-4o:channel:", "a:model:gpt-4o:channel:bad",
		"a:model:gpt-4o:channel:0", "a:model:gpt-4o:channel:-5", "a:model:gpt-4o:channel:+5",
		"a:model:gpt-4o:channel:05", "a:model:gpt-4o:channel:5:extra", "a:model:gpt-4o:channel:9223372036854775808",
	} {
		t.Run(key, func(t *testing.T) {
			if got := h.displayDimensionName("instance_model_channel", key); got != key {
				t.Fatalf("display name = %q, want original %q", got, key)
			}
			if got := h.displayDimensionKey("instance_model_channel", key); got != key {
				t.Fatalf("display key = %q, want original %q", got, key)
			}
		})
	}
	if len(source.calls) != 0 {
		t.Fatalf("malformed dimensions queried names: %v", source.calls)
	}
}

func TestModelChannelMetricsAndHistoryReturnChannelNames(t *testing.T) {
	key := "inst:model:provider:channel:inner:gpt:channel:5"
	source := &metricSourceStub{metrics: []aggregator.Metric{
		{InstanceID: "inst", BucketTime: time.Now().UTC(), DimensionType: "instance_model_channel", DimensionKey: key, RequestCount: 7, TPM: 1234},
	}}
	h := NewHandler(nil).WithMetricSource(source).WithNameSource(&nameSourceFake{calls: map[string]int{}})
	for _, tc := range []struct {
		path    string
		handler http.HandlerFunc
	}{
		{"metrics?dimension_type=instance_model_channel&latest=true", h.HandleMetrics},
		{"metric-history?dimension_type=instance_model_channel&dimension_key_prefix=inst:model:provider:channel:inner:gpt:channel:&hours=1", h.HandleMetricHistory},
		{"metric-history?dimension_type=instance_model_channel&dimension_key_prefix=inst:model:provider:channel:inner:gpt:channel:&hours=1&aggregate=true", h.HandleMetricHistory},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			tc.handler(rr, httptest.NewRequest(http.MethodGet, "/api/dashboard/"+tc.path, nil))
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
			}
			var response MetricListResponse
			if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Items) != 1 {
				t.Fatalf("items = %v", response.Items)
			}
			item := response.Items[0]
			if item.DisplayName != "主渠道" || item.DisplayKey != "主渠道" || item.DimensionKey != key || item.RequestCount != 7 || item.TPM != 1234 {
				t.Fatalf("unexpected metric: %+v", item)
			}
		})
	}
}

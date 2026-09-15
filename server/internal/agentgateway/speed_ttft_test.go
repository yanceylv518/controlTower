package agentgateway

import (
	"encoding/json"
	"testing"
)

func TestMalformedOptionalSpeedEvidencePreservesReport(t *testing.T) {
	for _, value := range []string{`"bad"`, `{"buckets":"bad"}`, `{"buckets":["bad"]}`, `{"retry_count":"bad"}`, `[]`} {
		var report AgentReportRequest
		err := json.Unmarshal([]byte(`{"aggregated_metrics":[{"request_count":3,"ttft_count":3,"speed_ttft":`+value+`}]}`), &report)
		if err != nil || len(report.AggregatedMetrics) != 1 || report.AggregatedMetrics[0].RequestCount != 3 {
			t.Fatalf("optional evidence %s blocked report: %v", value, err)
		}
		if report.AggregatedMetrics[0].SpeedTTFT.Valid(3) {
			t.Fatal("malformed evidence accepted")
		}
	}
}

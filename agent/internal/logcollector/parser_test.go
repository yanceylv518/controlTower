package logcollector

import (
	"strings"
	"testing"
	"time"
)

func TestConvertRowBuildsConsumeEventWithCacheTokens(t *testing.T) {
	createdAt := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	event, ok, err := ConvertRow(Row{
		ID:                1001,
		CreatedAt:         createdAt,
		Type:              2,
		UserID:            7,
		Username:          "alice",
		ChannelID:         18,
		ModelName:         "gpt-4o",
		TokenID:           9,
		TokenName:         "prod-token",
		PromptTokens:      30,
		CompletionTokens:  70,
		Quota:             500,
		UseTime:           3.2,
		IsStream:          true,
		Group:             "default",
		RequestID:         "req-1",
		UpstreamRequestID: "up-1",
		Other:             `{"cache_tokens":128}`,
	})
	if err != nil {
		t.Fatalf("convert row: %v", err)
	}
	if !ok {
		t.Fatalf("expected supported log row")
	}
	if event.LogType != "consume" {
		t.Fatalf("unexpected log type: %s", event.LogType)
	}
	if event.TotalTokens != 100 {
		t.Fatalf("unexpected total tokens: %d", event.TotalTokens)
	}
	if event.CacheTokens == nil || *event.CacheTokens != 128 {
		t.Fatalf("unexpected cache tokens: %#v", event.CacheTokens)
	}
	if !event.CacheFieldPresent {
		t.Fatalf("cache field should be present")
	}
}

func TestConvertRowParsesValidFirstResponseMillisecondsIndependently(t *testing.T) {
	event, ok, err := ConvertRow(Row{ID: 2001, CreatedAt: time.Now().UTC(), Type: 2, Other: `{"frt":1234,"cache_tokens":"invalid"}`})
	if err != nil || !ok {
		t.Fatalf("convert row: ok=%v err=%v", ok, err)
	}
	if event.FirstResponseMs == nil || *event.FirstResponseMs != 1234 {
		t.Fatalf("unexpected frt: %#v", event.FirstResponseMs)
	}
	if event.CacheTokens != nil {
		t.Fatalf("cache parsing must remain independent: %#v", event.CacheTokens)
	}
}

func TestConvertRowRejectsInvalidFirstResponseMilliseconds(t *testing.T) {
	for _, other := range []string{`{"frt":0}`, `{"frt":3600001}`, `{"frt":"bad"}`} {
		event, ok, err := ConvertRow(Row{ID: 2002, CreatedAt: time.Now().UTC(), Type: 2, Other: other})
		if err != nil || !ok {
			t.Fatalf("metadata %s blocked event: ok=%v err=%v", other, ok, err)
		}
		if event.FirstResponseMs != nil {
			t.Fatalf("invalid frt accepted for %s", other)
		}
	}
}

func TestConvertRowRedactsAndTruncatesErrorSummary(t *testing.T) {
	longSecret := "Authorization: Bearer sk-prod-secret-value " + strings.Repeat("x", 400)
	event, ok, err := ConvertRow(Row{
		ID:        1002,
		CreatedAt: time.Date(2026, 7, 2, 12, 1, 0, 0, time.UTC),
		Type:      5,
		Content:   longSecret,
	})
	if err != nil {
		t.Fatalf("convert row: %v", err)
	}
	if !ok {
		t.Fatalf("expected supported error row")
	}
	if event.LogType != "error" {
		t.Fatalf("unexpected log type: %s", event.LogType)
	}
	if strings.Contains(event.ErrorSummary, "sk-prod-secret-value") {
		t.Fatalf("secret was not redacted: %s", event.ErrorSummary)
	}
	if len(event.ErrorSummary) > 300 {
		t.Fatalf("summary too long: %d", len(event.ErrorSummary))
	}
}

func TestConvertRowMissingCacheTokensStaysNil(t *testing.T) {
	event, ok, err := ConvertRow(Row{
		ID:        1003,
		CreatedAt: time.Date(2026, 7, 2, 12, 2, 0, 0, time.UTC),
		Type:      2,
		Other:     `{}`,
	})
	if err != nil {
		t.Fatalf("convert row: %v", err)
	}
	if !ok {
		t.Fatalf("expected supported row")
	}
	if event.CacheTokens != nil {
		t.Fatalf("cache tokens should be nil when field is missing")
	}
	if event.CacheFieldPresent {
		t.Fatalf("cache field should not be present")
	}
}

func TestConvertRowMalformedOptionalMetadataDoesNotBlockEvent(t *testing.T) {
	for _, other := range []string{"not-json", `{"cache_tokens":"invalid"}`} {
		event, ok, err := ConvertRow(Row{
			ID:        1004,
			CreatedAt: time.Date(2026, 7, 2, 12, 3, 0, 0, time.UTC),
			Type:      2,
			Other:     other,
		})
		if err != nil {
			t.Fatalf("optional metadata %q blocked event: %v", other, err)
		}
		if !ok || event.SourceLogID != 1004 {
			t.Fatalf("event was not preserved for metadata %q: %#v", other, event)
		}
		if event.CacheTokens != nil || event.CacheFieldPresent {
			t.Fatalf("invalid cache metadata should be unavailable: %#v", event)
		}
	}
}
func TestConvertRowIgnoresUnsupportedLogTypes(t *testing.T) {
	_, ok, err := ConvertRow(Row{
		ID:        1004,
		CreatedAt: time.Date(2026, 7, 2, 12, 3, 0, 0, time.UTC),
		Type:      1,
	})
	if err != nil {
		t.Fatalf("convert row: %v", err)
	}
	if ok {
		t.Fatalf("unsupported log type should be ignored")
	}
}

package storage

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestParseAuditSearch(t *testing.T) {
	for _, test := range []struct {
		mode, query string
		want        []string
		invalid     bool
	}{
		{"smart", "  GPT-4\t渠道\n7  ", []string{"gpt-4", "渠道", "7"}, false},
		{"smart", `"香港 备用" GPT-4`, []string{"香港 备用", "gpt-4"}, false},
		{"smart", "  ", nil, false},
		{"", " one two ", []string{"one two"}, false},
		{"target", " CaseSensitive ", []string{"CaseSensitive"}, false},
		{"smart", `"unclosed`, nil, true},
		{"smart", `""`, nil, true},
		{"smart", "a b c d e f g h i", nil, true},
		{"made-up", "", nil, true},
		{"smart", strings.Repeat("a", 257), nil, true},
	} {
		got, err := ParseAuditSearch(test.mode, test.query)
		if (err != nil) != test.invalid || (!test.invalid && !reflect.DeepEqual(got, test.want)) {
			t.Fatalf("%q %q: terms=%v err=%v", test.mode, test.query, got, err)
		}
	}
}

func TestUnifiedAuditSearchRecognizesIdentifiersAndErrorBoundaries(t *testing.T) {
	for _, term := range []string{"127.0.0.1", "2001:db8::1", "0123456789abcdef0123456789abcdef", "123e4567-e89b-12d3-a456-426614174000"} {
		if !AuditSearchExactTerm(term) || AuditSearchValueMatches(term+"extra", term) {
			t.Errorf("identifier=%q", term)
		}
	}
	for _, test := range []struct {
		term, message string
		want          bool
	}{
		{"429", "upstream status_code=429 busy", true},
		{"429", "upstream status_code=4290 busy", false},
		{"429", "upstream status_code=1429 busy", false},
		{"status_code=429", "upstream status_code = 429 busy", true},
		{"status_code=429", "upstream status_code=4290 busy", false},
		{"status_code=429", "old_status_code=429", false},
	} {
		if got := regexp.MustCompile(AuditSearchErrorPattern(test.term)).MatchString(test.message); got != test.want {
			t.Errorf("%q %q: got=%v", test.term, test.message, got)
		}
	}
	if AuditSearchValueMatches("status_code=4290", "status_code=429") || !AuditSearchValueMatches("status_code = 429", "status_code=429") {
		t.Fatal("snapshot status codes must keep numeric boundaries")
	}
}

func TestAuditNumericSearchDoesNotMatchPartialValues(t *testing.T) {
	for _, term := range []string{"7", "-7", "0.75", "1e3"} {
		if !AuditSearchExactTerm(term) || !AuditSearchValueMatches(term, term) || AuditSearchValueMatches("value-"+term, term) {
			t.Errorf("numeric term=%q", term)
		}
	}
	for _, term := range []string{"gpt-4", "01", "7abc", "%_!"} {
		if AuditSearchExactTerm(term) {
			t.Errorf("text classified as numeric: %q", term)
		}
	}
}

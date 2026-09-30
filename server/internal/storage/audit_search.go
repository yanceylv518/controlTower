package storage

import (
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"regexp"
	"strings"
	"unicode"
)

var ErrInvalidAuditSearch = errors.New("invalid audit search")

const MaxAuditSearchTerms = 8

var auditIdentifierTerm = regexp.MustCompile(`^(?:[a-f0-9]{32}|[a-f0-9]{64}|[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12})$`)
var auditStatusCodeTerm = regexp.MustCompile(`^status_code=([1-5][0-9]{2})$`)

// ParseAuditSearch 保留旧接口的整句模糊搜索，新模式支持空白分词和双引号短语。
func ParseAuditSearch(mode, query string) ([]string, error) {
	switch mode {
	case "", "text", "smart", "target", "error", "ip":
	default:
		return nil, ErrInvalidAuditSearch
	}
	query = strings.TrimSpace(query)
	if len(query) > 256 {
		return nil, ErrInvalidAuditSearch
	}
	if query == "" {
		return nil, nil
	}
	if mode != "smart" && mode != "error" {
		return []string{query}, nil
	}
	var terms []string
	var term strings.Builder
	quoted := false
	flush := func() {
		if term.Len() > 0 {
			terms = append(terms, strings.ToLower(term.String()))
			term.Reset()
		}
	}
	for _, char := range query {
		switch {
		case char == '"':
			quoted = !quoted
		case unicode.IsSpace(char) && !quoted:
			flush()
		default:
			term.WriteRune(char)
		}
	}
	flush()
	if quoted || len(terms) == 0 || len(terms) > MaxAuditSearchTerms {
		return nil, ErrInvalidAuditSearch
	}
	return terms, nil
}

// AuditSearchExactTerm 数字、有效 IP 和标准请求ID匹配完整值，避免子串误命中。
func AuditSearchExactTerm(term string) bool {
	if _, err := netip.ParseAddr(term); err == nil || auditIdentifierTerm.MatchString(term) {
		return true
	}
	decoder := json.NewDecoder(strings.NewReader(term))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return false
	}
	if _, ok := value.(json.Number); !ok {
		return false
	}
	return decoder.Decode(&value) == io.EOF
}

// AuditSearchErrorPattern 错误摘要中的数字保留词边界，不要求整段错误文本等于数字。
func AuditSearchErrorPattern(term string) string {
	if match := auditStatusCodeTerm.FindStringSubmatch(term); match != nil {
		return `(^|[^0-9A-Za-z_.])status_code[[:space:]]*=[[:space:]]*` + match[1] + `([^0-9A-Za-z_.]|$)`
	}
	if AuditSearchExactTerm(term) {
		return `(^|[^0-9A-Za-z_.])` + regexp.QuoteMeta(term) + `([^0-9A-Za-z_.]|$)`
	}
	return ""
}

func AuditSearchValueMatches(value, term string) bool {
	value = strings.ToLower(value)
	if auditStatusCodeTerm.MatchString(term) {
		return regexp.MustCompile(AuditSearchErrorPattern(term)).MatchString(value)
	}
	if AuditSearchExactTerm(term) {
		return value == term
	}
	return strings.Contains(value, term)
}

func OperationAuditMatchesSearch(audit OperationAudit, mode string, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	switch mode {
	case "target":
		return audit.TargetID == terms[0]
	case "ip":
		return audit.ClientIP == terms[0]
	case "", "text":
		return strings.Contains(strings.ToLower(audit.OperationType+" "+audit.TargetType+" "+audit.TargetID+" "+audit.ActorID+" "+audit.ErrorSummary+" "+audit.RequestID+" "+audit.CorrelationID), strings.ToLower(terms[0]))
	}
	var values []string
	if mode == "smart" {
		values = append(values, audit.ActorID)
		for _, snapshot := range []string{audit.BeforeSummary, audit.AfterSummary} {
			decoder := json.NewDecoder(strings.NewReader(snapshot))
			decoder.UseNumber()
			var value any
			if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
				continue
			}
			pending := []any{value}
			for len(pending) > 0 {
				value, pending = pending[len(pending)-1], pending[:len(pending)-1]
				switch value := value.(type) {
				case map[string]any:
					for _, child := range value {
						pending = append(pending, child)
					}
				case []any:
					pending = append(pending, value...)
				case string:
					if value != "[redacted]" {
						values = append(values, value)
					}
				case json.Number:
					values = append(values, value.String())
				case bool:
					if value {
						values = append(values, "true")
					} else {
						values = append(values, "false")
					}
				}
			}
		}
	}
	for _, term := range terms {
		matched := false
		if pattern := AuditSearchErrorPattern(term); mode == "smart" && pattern != "" {
			matched = regexp.MustCompile(pattern).MatchString(strings.ToLower(audit.ErrorSummary))
		} else {
			matched = strings.Contains(strings.ToLower(audit.ErrorSummary), term)
		}
		if mode == "smart" {
			for _, identifier := range []string{audit.TargetID, audit.RequestID, audit.CorrelationID, audit.ClientIP} {
				matched = matched || strings.ToLower(identifier) == term
			}
		}
		if matched {
			continue
		}
		for _, value := range values {
			if (mode == "error" && strings.Contains(strings.ToLower(value), term)) || (mode == "smart" && AuditSearchValueMatches(value, term)) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

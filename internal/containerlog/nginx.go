package containerlog

import (
	"errors"
	"strings"
)

func MatchesDomain(host string, domains []string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return false
	}
	for _, domain := range domains {
		domain = strings.TrimSuffix(strings.ToLower(domain), ".")
		if domain == host {
			return true
		}
		if strings.HasPrefix(domain, "*.") && strings.HasSuffix(host, domain[1:]) {
			return true
		}
		if strings.HasPrefix(domain, ".") && (host == domain[1:] || strings.HasSuffix(host, domain)) {
			return true
		}
	}
	return false
}

func ValidateSourceQuery(s Source, q Query) error {
	kind := s.Kind
	if kind == "" {
		kind = "app"
	}
	qKind := q.Kind
	if qKind == "" {
		qKind = "app"
	}
	if kind != qKind {
		return errors.New("日志类型与来源不一致")
	}
	if kind == "app" {
		if q.Host != "" || q.Path != "" || q.Level != "" || q.MinDurationMS != 0 {
			return errors.New("应用日志不支持 Nginx 筛选")
		}
		return nil
	}
	if !MatchesDomain(q.Host, s.Domains) {
		return errors.New("日志来源与当前站点域名不匹配")
	}
	has := func(field string) bool {
		for _, f := range s.Fields {
			if f == field {
				return true
			}
		}
		return false
	}
	if (q.RequestID != "" && !has("request_id")) || (q.ErrorCode != "" && !has("status")) || (q.Path != "" && !has("path")) || (q.MinDurationMS > 0 && !has("duration")) || (q.Level != "" && !has("level")) {
		return errors.New("此日志格式不支持所选筛选条件")
	}
	if q.ErrorCode != "" && (len(q.ErrorCode) != 3 || q.ErrorCode[0] < '1' || q.ErrorCode[0] > '5' || q.ErrorCode[1] < '0' || q.ErrorCode[1] > '9' || q.ErrorCode[2] < '0' || q.ErrorCode[2] > '9') {
		return errors.New("HTTP 状态码格式无效")
	}
	return nil
}

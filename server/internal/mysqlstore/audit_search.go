package mysqlstore

import (
	"strings"

	"controltower/server/internal/storage"
)

func auditSearchWhere(mode string, terms []string) ([]string, []any) {
	var where []string
	var args []any
	if len(terms) == 0 {
		return where, args
	}
	if mode == "target" || mode == "ip" {
		column := "a.target_id"
		if mode == "ip" {
			column = "a.client_ip"
		}
		return []string{"CAST(" + column + " AS BINARY) = CAST(? AS BINARY)"}, []any{terms[0]}
	}
	if mode == "" || mode == "text" {
		pattern := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(terms[0]) + "%"
		var matches []string
		for _, column := range []string{"operation_type", "target_type", "target_id", "actor_id", "error_summary", "request_id", "correlation_id"} {
			matches = append(matches, "a."+column+" LIKE ? ESCAPE '!'")
			args = append(args, pattern)
		}
		return []string{"(" + strings.Join(matches, " OR ") + ")"}, args
	}
	// 只枚举 JSON 的实际标量值；不匹配键名、对象序列化或脱敏占位符。
	// 两条递归路径分别覆盖对象属性和数组成员，外层数组兼容根节点标量及历史非 JSON 内容。
	snapshots := "CONCAT('[',IF(JSON_VALID(a.before_summary),a.before_summary,'{}'),',',IF(JSON_VALID(a.after_summary),a.after_summary,'{}'),']')"
	for _, term := range terms {
		match := func(column string, exact bool) string {
			value := "LOWER(COALESCE(" + column + ",'')) COLLATE utf8mb4_bin"
			if !exact && mode == "smart" && strings.HasPrefix(term, "status_code=") {
				if pattern := storage.AuditSearchErrorPattern(term); pattern != "" {
					args = append(args, pattern)
					return "REGEXP_LIKE(" + value + ", ?, 'c')"
				}
			}
			args = append(args, term)
			if exact {
				return value + " = CAST(? AS CHAR CHARACTER SET utf8mb4) COLLATE utf8mb4_bin"
			}
			return "LOCATE(CAST(? AS CHAR CHARACTER SET utf8mb4) COLLATE utf8mb4_bin," + value + ")>0"
		}
		var matches []string
		if pattern := storage.AuditSearchErrorPattern(term); mode == "smart" && pattern != "" {
			matches = append(matches, "REGEXP_LIKE(LOWER(COALESCE(a.error_summary,'')), ?, 'c')")
			args = append(args, pattern)
		} else {
			matches = append(matches, match("a.error_summary", false))
		}
		if mode == "smart" {
			exact := storage.AuditSearchExactTerm(term)
			matches = append(matches, match("a.actor_id", exact))
			for _, column := range []string{"target_id", "request_id", "correlation_id", "client_ip"} {
				matches = append(matches, match("a."+column, true))
			}
			for _, path := range []string{"$**.*", "$**[*]"} {
				matches = append(matches, "EXISTS (SELECT 1 FROM JSON_TABLE("+snapshots+",'"+path+"' COLUMNS(v JSON PATH '$')) audit_values WHERE JSON_TYPE(v) IN ('STRING','INTEGER','DOUBLE','DECIMAL','BOOLEAN') AND CAST(JSON_UNQUOTE(v) AS BINARY) <> CAST('[redacted]' AS BINARY) AND "+match("JSON_UNQUOTE(v)", exact)+")")
			}
		}
		where = append(where, "("+strings.Join(matches, " OR ")+")")
	}
	return where, args
}

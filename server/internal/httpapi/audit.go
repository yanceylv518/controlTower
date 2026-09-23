package httpapi

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"controltower/server/internal/auditmeta"
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/storage"
)

const auditRequestBodyLimit = 32 * 1024
const auditResponseCaptureLimit = 8 * 1024
const auditMutationResponseLimit = 1 << 20

var safeAuditErrorCode = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,96}$`)

type operationAuditWriter interface {
	InsertOperationAudit(storage.OperationAudit) error
}

type operationAuditHTTPStatusWriter interface {
	UpdateOperationAuditHTTPStatus(requestID string, status int) error
}

type auditResponseWriter struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	response bytes.Buffer
	overflow bool
}

func (w *auditResponseWriter) Header() http.Header { return w.header }

func (w *auditResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
}

func (w *auditResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if remain := auditResponseCaptureLimit - w.body.Len(); remain > 0 {
		if len(data) < remain {
			remain = len(data)
		}
		_, _ = w.body.Write(data[:remain])
	}
	if !w.overflow {
		if len(data) > auditMutationResponseLimit-w.response.Len() {
			w.overflow = true
		} else {
			_, _ = w.response.Write(data)
		}
	}
	return len(data), nil
}

func (w *auditResponseWriter) commit(target http.ResponseWriter, requestID string) {
	if w.overflow {
		target.Header().Set("Content-Type", "application/json")
		target.Header().Del("Content-Length")
		target.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(target).Encode(map[string]any{
			"error":            "operation_response_too_large",
			"request_id":       requestID,
			"operation_status": w.status,
			"message":          "操作结果已记录，请根据请求 ID 查看操作审计中的原始状态。",
		})
		return
	}
	for key, values := range w.header {
		target.Header()[key] = append([]string(nil), values...)
	}
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	target.WriteHeader(status)
	_, _ = target.Write(w.response.Bytes())
}

func auditMutations(next http.Handler, store operationAuditWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAuditMutation(r) {
			next.ServeHTTP(w, r)
			return
		}
		if store == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "audit_unavailable"})
			return
		}

		requestID := newAuditRequestID()
		requestSummary, requestFields := auditRequestSummary(r)
		metadata := auditmeta.RequestMetadata{RequestID: requestID, ActorID: ctauth.Actor(r), ActorType: "service_token", AuthMethod: "bearer"}
		if user, ok := ctauth.CurrentUser(r); ok {
			metadata.ActorID = user.Username
			metadata.ActorType = "human"
			metadata.ActorRole = user.Role
			metadata.AuthMethod = "session"
		}
		r = auditmeta.WithRequestMetadata(r, metadata)
		r.Header.Set("X-CT-Audit-Request-ID", requestID)
		w.Header().Set("X-Request-ID", requestID)
		capture := &auditResponseWriter{header: w.Header().Clone()}
		next.ServeHTTP(capture, r)

		status := capture.status
		if status == 0 {
			status = http.StatusOK
		}
		semanticAuditFailure := isAuditPersistenceFailure(status, capture.body.Bytes())
		if auditmeta.SemanticAuditRecorded(r) {
			metadata, found := auditmeta.Metadata(r)
			updater, supportsStatusUpdate := store.(operationAuditHTTPStatusWriter)
			if !found || metadata.RequestID == "" || !supportsStatusUpdate {
				log.Printf("operation audit response status update unavailable request_id=%s status=%d", metadata.RequestID, status)
				writeUnknownOperationResult(w, requestID)
				return
			}
			if err := updater.UpdateOperationAuditHTTPStatus(metadata.RequestID, status); err != nil {
				log.Printf("operation audit response status update failed request_id=%s status=%d error=%v", metadata.RequestID, status, err)
				writeUnknownOperationResult(w, requestID)
				return
			}
		}
		if status < http.StatusBadRequest && (auditmeta.SemanticAuditRecorded(r) || auditmeta.AuditHandledWithoutRecord(r)) {
			capture.commit(w, requestID)
			return
		}

		now := time.Now().UTC()
		actor := ctauth.Actor(r)
		metadata, _ = auditmeta.Metadata(r)
		if actor == "" {
			actor = metadata.ActorID
		}
		instanceID, targetType, targetID := auditTarget(r, requestFields)
		correlationID := responseCorrelationID(capture.body.Bytes())
		if correlationID == "" {
			correlationID = requestID
		}
		audit := storage.OperationAudit{
			ID: "http-" + requestID, InstanceID: instanceID, OperationType: auditOperation(r),
			TargetType: targetType, TargetID: targetID, ActorID: actor, SourceComponent: auditSource(r),
			ActorType: metadata.ActorType, ActorRole: metadata.ActorRole,
			TriggerType: "manual", RequestID: requestID, CorrelationID: correlationID,
			ClientIP: auditClientIP(r), AuthMethod: auditAuthMethod(r), HTTPMethod: r.Method,
			Route: auditRoute(r), HTTPStatus: status, AfterSummary: requestSummary,
			Status: "succeeded", CreatedAt: now, UpdatedAt: now,
		}
		if status >= http.StatusBadRequest {
			audit.Status = "failed"
			audit.ErrorSummary = auditErrorSummary(status, capture.body.Bytes())
		}
		audit = storage.NormalizeOperationAudit(audit)
		if err := store.InsertOperationAudit(audit); err != nil {
			log.Printf("operation audit write failed request_id=%s route=%s status=%d error=%v", requestID, audit.Route, status, err)
			writeUnknownOperationResult(w, requestID)
			return
		}
		if semanticAuditFailure {
			writeUnknownOperationResult(w, requestID)
			return
		}
		capture.commit(w, requestID)
	})
}

func writeUnknownOperationResult(w http.ResponseWriter, requestID string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Del("Content-Length")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":      "operation_result_unknown",
		"request_id": requestID,
		"message":    "操作可能已生效，请先根据请求 ID 查看操作审计，确认前不要重试。",
	})
}

func isAuditPersistenceFailure(status int, body []byte) bool {
	if status < http.StatusInternalServerError {
		return false
	}
	var result struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &result) != nil {
		return false
	}
	return result.Error == "audit_failed" || strings.HasSuffix(result.Error, "_audit_failed")
}

func isAuditMutation(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/dashboard/") && r.URL.Path != "/api/dashboard" &&
		r.URL.Path != "/api/auth/login" && r.URL.Path != "/api/auth/logout" && r.URL.Path != "/api/auth/password" && !strings.HasPrefix(r.URL.Path, "/api/auth/users") {
		return false
	}
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return false
	}
	switch r.URL.Path {
	case "/api/dashboard/model-square", "/api/dashboard/billing/models", "/api/dashboard/tuning/preflight", "/api/dashboard/container-log-tasks":
		return false
	default:
		return true
	}
}

func auditRequestSummary(r *http.Request) (string, map[string]any) {
	fields := map[string]any{}
	var summary any
	if r.Body != nil {
		body, err := io.ReadAll(io.LimitReader(r.Body, auditRequestBodyLimit+1))
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), r.Body))
		if err == nil && len(body) > 0 && len(body) <= auditRequestBodyLimit {
			var value any
			if json.Unmarshal(body, &value) == nil {
				summary = redactAuditValue("", value)
				if object, ok := summary.(map[string]any); ok {
					fields = object
				}
			} else {
				summary = map[string]any{"body": "[omitted: non-json]"}
			}
		} else if len(body) > auditRequestBodyLimit {
			summary = map[string]any{"body": "[omitted: too large]"}
		}
	}
	query := map[string]string{}
	for _, key := range []string{"instance_id", "site_id", "id", "channel_id", "dataset_id", "task_id"} {
		if value := r.URL.Query().Get(key); value != "" {
			query[key] = value
		}
	}
	encoded, err := json.Marshal(map[string]any{"request": summary, "query": query})
	if err != nil {
		return `{"request":"[omitted]"}`, fields
	}
	return string(encoded), fields
}

func redactAuditValue(key string, value any) any {
	if isSensitiveAuditKey(key) {
		return "[redacted]"
	}
	switch current := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(current))
		for childKey, childValue := range current {
			out[childKey] = redactAuditValue(childKey, childValue)
		}
		return out
	case []any:
		out := make([]any, len(current))
		for index, childValue := range current {
			out[index] = redactAuditValue("", childValue)
		}
		return out
	default:
		return value
	}
}

func isSensitiveAuditKey(key string) bool {
	key = strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
	for _, fragment := range []string{"password", "passwd", "token", "secret", "dsn", "authorization", "cookie", "privatekey", "credential", "apikey"} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}

func auditTarget(r *http.Request, fields map[string]any) (string, string, string) {
	if r.URL.Path == "/api/auth/login" {
		name := []rune(firstAuditValue(fields, "username"))
		if len(name) > 128 {
			name = name[:128]
		}
		return "", "ct_user", string(name)
	}
	if r.URL.Path == "/api/auth/logout" || r.URL.Path == "/api/auth/password" {
		meta, _ := auditmeta.Metadata(r)
		return "", "ct_user", meta.ActorID
	}
	instanceID := firstAuditValue(fields, "instance_id", "site_id")
	if instanceID == "" {
		instanceID = firstAuditValue(mapStringAny(r.URL.Query()), "instance_id", "site_id")
	}
	pattern := strings.TrimSpace(strings.TrimPrefix(auditRoute(r), r.Method+" "))
	patternParts, pathParts := strings.Split(strings.Trim(pattern, "/"), "/"), strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	for index := len(patternParts) - 1; index >= 0; index-- {
		if index < len(pathParts) && strings.HasPrefix(patternParts[index], "{") && strings.HasSuffix(patternParts[index], "}") {
			name := strings.Trim(patternParts[index], "{}")
			targetType := strings.ToLower(name)
			if targetType == "id" {
				targetType = strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/dashboard/"), "/")
				if slash := strings.IndexByte(targetType, '/'); slash >= 0 {
					targetType = targetType[:slash]
				}
			} else {
				targetType = strings.TrimSuffix(targetType, "id")
			}
			return instanceID, strings.ReplaceAll(targetType, "-", "_"), pathParts[index]
		}
	}
	targetID := firstAuditValue(fields, "dataset_id", "channel_id", "user_id", "id", "instance_id", "site_id")
	if targetID == "" {
		targetID = firstAuditValue(mapStringAny(r.URL.Query()), "dataset_id", "channel_id", "id", "instance_id", "site_id")
	}
	resource := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/api/dashboard/"), "/api/auth/")
	if slash := strings.IndexByte(resource, '/'); slash >= 0 {
		resource = resource[:slash]
	}
	if resource == "" {
		resource = "global"
	}
	return instanceID, strings.ReplaceAll(resource, "-", "_"), targetID
}

func mapStringAny(values map[string][]string) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		if len(value) > 0 {
			out[key] = value[0]
		}
	}
	return out
}

func firstAuditValue(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			if text, ok := value.(string); ok && text != "" {
				return text
			}
			if number, ok := value.(json.Number); ok {
				return number.String()
			}
			if number, ok := value.(float64); ok {
				return fmt.Sprintf("%.0f", number)
			}
		}
	}
	return ""
}

func auditOperation(r *http.Request) string {
	switch r.URL.Path {
	case "/api/auth/login":
		return "auth.login"
	case "/api/auth/logout":
		return "auth.logout"
	case "/api/auth/password":
		return "auth.password_change"
	}
	resource := "mutation"
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) >= 3 {
		resource = parts[2]
		if resource == "dashboard" || resource == "auth" {
			resource = parts[2]
		}
		if len(parts) > 3 && (resource == "billing" || resource == "tuning" || resource == "archive-datasets") {
			resource += "." + parts[3]
		}
	}
	resource = strings.NewReplacer("-", "_", "/", ".").Replace(resource)
	operation := "http." + auditSource(r) + "." + resource + "." + strings.ToLower(r.Method)
	if len(operation) > 64 {
		operation = operation[:64]
	}
	return operation
}

func auditSource(r *http.Request) string {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) > 1 && parts[0] == "api" && parts[1] == "auth" {
		return "identity"
	}
	if len(parts) > 2 && parts[0] == "api" && parts[1] == "dashboard" {
		switch parts[2] {
		case "billing", "tuning", "archive-datasets", "log-archives", "alerts", "notification-channels", "notification-deliveries", "container-log-tasks", "channels", "channel-group-presets":
			return strings.ReplaceAll(strings.TrimSuffix(parts[2], "-datasets"), "-", "_")
		default:
			return "system"
		}
	}
	return "control_tower"
}

func auditRoute(r *http.Request) string {
	if r.Pattern != "" {
		return r.Pattern
	}
	return r.URL.Path
}

func auditClientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func auditAuthMethod(r *http.Request) string {
	if metadata, ok := auditmeta.Metadata(r); ok && metadata.AuthMethod != "" {
		return metadata.AuthMethod
	}
	if _, ok := ctauth.CurrentUser(r); ok {
		return "session"
	}
	return "bearer"
}

func responseCorrelationID(body []byte) string {
	var value map[string]any
	if json.Unmarshal(body, &value) != nil {
		return ""
	}
	return firstAuditValue(value, "command_id", "task_id", "id")
}

func auditErrorSummary(status int, body []byte) string {
	var value map[string]any
	if json.Unmarshal(body, &value) == nil {
		if code, ok := value["error"].(string); ok && safeAuditErrorCode.MatchString(code) {
			return code
		}
	}
	return http.StatusText(status)
}

func newAuditRequestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return fmt.Sprintf("%032x", time.Now().UTC().UnixNano())
}

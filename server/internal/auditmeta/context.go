package auditmeta

import (
	"context"
	"net"
	"net/http"

	"controltower/server/internal/storage"
)

type requestMetadataKey struct{}

type requestState struct {
	metadata                  RequestMetadata
	semanticAudited           bool
	auditHandledWithoutRecord bool
}

type RequestMetadata struct {
	RequestID  string
	ActorID    string
	ActorType  string
	ActorRole  string
	AuthMethod string
}

func WithRequestMetadata(r *http.Request, metadata RequestMetadata) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), requestMetadataKey{}, &requestState{metadata: metadata}))
}

func SetActor(r *http.Request, actorID, actorType, actorRole, authMethod string) {
	if r == nil {
		return
	}
	if state, _ := r.Context().Value(requestMetadataKey{}).(*requestState); state != nil {
		state.metadata.ActorID = actorID
		state.metadata.ActorType = actorType
		state.metadata.ActorRole = actorRole
		state.metadata.AuthMethod = authMethod
	}
}

func Metadata(r *http.Request) (RequestMetadata, bool) {
	if r == nil {
		return RequestMetadata{}, false
	}
	state, _ := r.Context().Value(requestMetadataKey{}).(*requestState)
	if state == nil {
		return RequestMetadata{}, false
	}
	return state.metadata, true
}

func MarkSemanticAudit(r *http.Request) {
	if r == nil {
		return
	}
	if state, _ := r.Context().Value(requestMetadataKey{}).(*requestState); state != nil {
		state.semanticAudited = true
	}
}

func SemanticAuditRecorded(r *http.Request) bool {
	if r == nil {
		return false
	}
	state, _ := r.Context().Value(requestMetadataKey{}).(*requestState)
	return state != nil && state.semanticAudited
}

func MarkAuditHandledWithoutRecord(r *http.Request) {
	if r == nil {
		return
	}
	if state, _ := r.Context().Value(requestMetadataKey{}).(*requestState); state != nil {
		state.auditHandledWithoutRecord = true
	}
}

func AuditHandledWithoutRecord(r *http.Request) bool {
	if r == nil {
		return false
	}
	state, _ := r.Context().Value(requestMetadataKey{}).(*requestState)
	return state != nil && state.auditHandledWithoutRecord
}

func Enrich(r *http.Request, value *storage.OperationAudit) {
	if r == nil || value == nil {
		return
	}
	if state, ok := r.Context().Value(requestMetadataKey{}).(*requestState); ok && state != nil {
		metadata := state.metadata
		if metadata.ActorID != "" {
			value.ActorID = metadata.ActorID
		}
		value.RequestID = metadata.RequestID
		if value.CorrelationID == "" {
			value.CorrelationID = metadata.RequestID
		}
		value.ActorType = metadata.ActorType
		value.ActorRole = metadata.ActorRole
		value.AuthMethod = metadata.AuthMethod
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		value.ClientIP = host
	} else {
		value.ClientIP = r.RemoteAddr
	}
	value.HTTPMethod = r.Method
	value.Route = r.Pattern
	if value.Route == "" {
		value.Route = r.URL.Path
	}
}

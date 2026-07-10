package agentgateway

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const maxAgentCompressedBytes = 2 * 1024 * 1024
const maxAgentDecodedBytes = 8 * 1024 * 1024

var errAgentPayloadTooLarge = errors.New("agent payload too large")

type Sink interface {
	SaveHeartbeat(req AgentHeartbeatRequest) (int64, error)
	SaveReport(req AgentReportRequest) error
}

type Handler struct {
	expectedToken string
	sink          Sink
}

func NewHandler(expectedToken string, sink Sink) Handler {
	return Handler{
		expectedToken: expectedToken,
		sink:          sink,
	}
}

func (h Handler) HandleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !validBearerToken(r, h.expectedToken) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req AgentHeartbeatRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	if req.InstanceID == "" || req.AgentID == "" {
		writeError(w, http.StatusBadRequest, "missing_identity")
		return
	}
	serverLastLogID, err := h.sink.SaveHeartbeat(req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed")
		return
	}
	writeJSON(w, http.StatusOK, AgentHeartbeatResponse{Accepted: true, ServerLastLogID: serverLastLogID})
}

func (h Handler) HandleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if !validBearerToken(r, h.expectedToken) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req AgentReportRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	if req.InstanceID == "" || req.AgentID == "" {
		writeError(w, http.StatusBadRequest, "missing_identity")
		return
	}
	if !validReportSize(req) {
		writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large")
		return
	}
	if err := h.sink.SaveReport(req); err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed")
		return
	}
	writeAccepted(w)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentCompressedBytes)
	defer r.Body.Close()
	reader, closeReader, err := requestBodyReader(r)
	if err != nil {
		return err
	}
	defer closeReader()

	data, err := io.ReadAll(io.LimitReader(reader, maxAgentDecodedBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxAgentDecodedBytes {
		return errAgentPayloadTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}

func validReportSize(req AgentReportRequest) bool {
	return len(req.LogEvents) <= 5000 &&
		len(req.LogSamples) <= 1000 &&
		len(req.AggregatedMetrics) <= 10000 &&
		len(req.ServerMetrics) <= 100 &&
		len(req.DockerStatuses) <= 5000 &&
		len(req.HealthChecks) <= 100 &&
		len(req.ChannelSnapshots) <= 5000
}

func requestBodyReader(r *http.Request) (io.Reader, func(), error) {
	if r.Header.Get("Content-Encoding") != "gzip" {
		return r.Body, func() {}, nil
	}
	reader, err := gzip.NewReader(r.Body)
	if err != nil {
		return nil, func() {}, err
	}
	return reader, func() { _ = reader.Close() }, nil
}

func writeDecodeError(w http.ResponseWriter, err error) {
	var maxBytesError *http.MaxBytesError
	if errors.Is(err, errAgentPayloadTooLarge) || errors.As(err, &maxBytesError) {
		writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid_json")
}

func writeAccepted(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]bool{"accepted": true})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + code + `"}`))
}

package logarchive

import (
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
)

// ReconcileSummary preserves integer totals exactly and distinguishes every
// NULL field from zero/empty. Digest hashes ordered, byte-framed raw row hashes.
type ReconcileSummary struct {
	Rows               uint64          `json:"rows,string"`
	Digest             string          `json:"digest"`
	Types              reconcileCounts `json:"types"`
	Nulls              reconcileCounts `json:"nulls"`
	Quota              string          `json:"quota"`
	PromptTokens       string          `json:"prompt_tokens"`
	CompletionTokens   string          `json:"completion_tokens"`
	ConsumeRows        uint64          `json:"consume_rows,string"`
	ConsumeQuota       string          `json:"consume_quota"`
	UnknownChargedRows uint64          `json:"unknown_charged_rows,string"`
}

// Histogram counts stay integers in Go but use decimal strings on the wire,
// including when the same summary is embedded in a persisted scan or manifest.
type reconcileCounts map[string]uint64

func (c reconcileCounts) MarshalJSON() ([]byte, error) {
	if c == nil {
		return []byte("null"), nil
	}
	encoded := make(map[string]string, len(c))
	for key, value := range c {
		encoded[key] = strconv.FormatUint(value, 10)
	}
	return json.Marshal(encoded)
}

func (c *reconcileCounts) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if fields == nil {
		*c = nil
		return nil
	}
	decoded := make(reconcileCounts, len(fields))
	for key, rawValue := range fields {
		text := string(rawValue)
		if len(rawValue) > 0 && rawValue[0] == '"' {
			if err := json.Unmarshal(rawValue, &text); err != nil {
				return err
			}
		}
		// Accept existing numeric JSON checkpoints without a float64 round trip.
		// Fractions, exponent notation, negative counts and overflow fail closed.
		value, err := strconv.ParseUint(text, 10, 64)
		if err != nil {
			return errors.New("invalid archive summary count")
		}
		decoded[key] = value
	}
	*c = decoded
	return nil
}

type reconcileScan struct {
	AfterCreated int64            `json:"after_created,string"`
	AfterID      int64            `json:"after_id,string"`
	HashState    []byte           `json:"hash_state"`
	Summary      ReconcileSummary `json:"summary"`
}

type reconcileScans struct {
	Version int              `json:"version"`
	Scans   [4]reconcileScan `json:"scans"`
	Phase   int              `json:"phase"`
}

func newReconcileScans() reconcileScans {
	r := reconcileScans{Version: 1}
	for i := range r.Scans {
		h := sha256.New()
		writerHashField(h, []byte("archive-day-raw-v1"))
		r.Scans[i].HashState, _ = h.(encoding.BinaryMarshaler).MarshalBinary()
		r.Scans[i].Summary = ReconcileSummary{Digest: hex.EncodeToString(h.Sum(nil)), Types: map[string]uint64{}, Nulls: map[string]uint64{}, Quota: "0", PromptTokens: "0", CompletionTokens: "0", ConsumeQuota: "0"}
	}
	return r
}

func (s *reconcileScan) add(columns []string, row []any) ([32]byte, error) {
	rawHash, err := writerRawHash(columns, row)
	if err != nil {
		return rawHash, err
	}
	h := sha256.New()
	if err := h.(encoding.BinaryUnmarshaler).UnmarshalBinary(s.HashState); err != nil {
		return rawHash, ErrWriterCheckpoint
	}
	writerHashField(h, rawHash[:])
	values := make(map[string]any, len(columns))
	for i, c := range columns {
		values[c] = row[i]
		if row[i] == nil {
			s.Summary.Nulls[c]++
		}
	}
	id, e := strconv.ParseInt(valueString(values["id"]), 10, 64)
	ts, e2 := strconv.ParseInt(valueString(values["created_at"]), 10, 64)
	if e != nil || e2 != nil || id <= 0 || ts < s.AfterCreated || (ts == s.AfterCreated && id <= s.AfterID) {
		return rawHash, ErrWriterCheckpoint
	}
	typ := valueString(values["type"])
	if values["type"] == nil {
		typ = "NULL"
	}
	s.Summary.Types[typ]++
	sums := []*string{&s.Summary.Quota, &s.Summary.PromptTokens, &s.Summary.CompletionTokens}
	for i, c := range []string{"quota", "prompt_tokens", "completion_tokens"} {
		value, exists := values[c]
		if !exists {
			return rawHash, ErrFoundationSchema
		}
		if value == nil {
			continue
		}
		n, ok := new(big.Int).SetString(valueString(value), 10)
		prior, valid := new(big.Int).SetString(*sums[i], 10)
		if !ok || !valid {
			return rawHash, &scanError{code: "invalid_source_row", sourceID: id}
		}
		*sums[i] = prior.Add(prior, n).String()
		if c == "quota" {
			if typ == "2" {
				prior, _ = new(big.Int).SetString(s.Summary.ConsumeQuota, 10)
				s.Summary.ConsumeQuota = prior.Add(prior, n).String()
			}
			if (typ != "1" && typ != "2" && typ != "3" && typ != "4" && typ != "5" && typ != "6") && n.Sign() != 0 {
				s.Summary.UnknownChargedRows++
			}
		}
	}
	if typ == "2" {
		s.Summary.ConsumeRows++
	}
	s.Summary.Rows++
	s.Summary.Digest = hex.EncodeToString(h.Sum(nil))
	s.HashState, _ = h.(encoding.BinaryMarshaler).MarshalBinary()
	s.AfterID, s.AfterCreated = id, ts
	return rawHash, nil
}

func valueString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func sameReconcileSummary(a, b ReconcileSummary) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func (s reconcileScans) valid() bool {
	if s.Version != 1 || s.Phase < 0 || s.Phase > 4 {
		return false
	}
	for _, p := range s.Scans {
		h := sha256.New()
		if h.(encoding.BinaryUnmarshaler).UnmarshalBinary(p.HashState) != nil || hex.EncodeToString(h.Sum(nil)) != p.Summary.Digest || p.Summary.Types == nil || p.Summary.Nulls == nil {
			return false
		}
		if (p.Summary.Rows == 0 && (p.AfterID != 0 || p.AfterCreated != 0)) || (p.Summary.Rows > 0 && (p.AfterID <= 0 || p.AfterCreated <= 0)) {
			return false
		}
		for _, n := range []string{p.Summary.Quota, p.Summary.PromptTokens, p.Summary.CompletionTokens, p.Summary.ConsumeQuota} {
			if _, ok := new(big.Int).SetString(n, 10); !ok {
				return false
			}
		}
	}
	return true
}

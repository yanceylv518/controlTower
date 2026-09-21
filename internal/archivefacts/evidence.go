// Package archivefacts preserves immutable billing evidence and extracts
// versioned facts without reading live metadata or calculating money.
package archivefacts

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const EvidenceCodecVersion = 1
const ParserVersion = "archive-facts-v1"
const FactSchemaVersion = 1
const maxEvidenceBytes = 64 << 20

var ErrEvidence = errors.New("archive billing evidence is invalid or unsupported")

// Column.Type is the source database's complete column type. The caller supplies
// the fingerprint of the full source schema, not just this evidence whitelist.
type Column struct{ Name, Type string }

type Evidence struct {
	CodecVersion     int
	SourceSchemaHash [32]byte
	Payload          []byte
	Hash             [32]byte
}

// The order and membership are part of codec v1. In particular content, IP,
// request/response bodies and arbitrary future source columns are excluded.
var evidenceFields = [...]string{
	"id", "created_at", "type", "user_id", "token_id", "channel_id",
	"username", "token_name", "model_name", "group", "request_id", "upstream_request_id",
	"quota", "prompt_tokens", "completion_tokens", "other",
}

// EncodeEvidence copies SQL bytes. A present nil slice means SQL NULL; a
// non-nil empty slice means empty bytes. An absent schema column has its own tag.
// A present schema column must have a value entry, preventing accidental loss of
// a SELECT column from being silently represented as SQL NULL.
func EncodeEvidence(columns []Column, values map[string][]byte, schemaHash [32]byte) (Evidence, error) {
	e := Evidence{CodecVersion: EvidenceCodecVersion, SourceSchemaHash: schemaHash}
	if schemaHash == ([32]byte{}) {
		return e, ErrEvidence
	}
	types := make(map[string]string, len(columns))
	for _, c := range columns {
		if c.Name == "" || c.Type == "" || len(c.Name) > 4096 || len(c.Type) > 4096 {
			return e, ErrEvidence
		}
		if _, exists := types[c.Name]; exists {
			return e, ErrEvidence
		}
		types[c.Name] = c.Type
	}
	var b bytes.Buffer
	b.WriteString("CT-BILLING-EVIDENCE\x00")
	for _, name := range evidenceFields {
		writeBytes(&b, []byte(name))
		kind, present := types[name]
		if !present {
			if _, exists := values[name]; exists {
				return e, ErrEvidence
			}
			b.WriteByte(0)
			continue
		}
		b.WriteByte(1)
		writeBytes(&b, []byte(kind))
		value, exists := values[name]
		if !exists || len(value) > maxEvidenceBytes {
			return e, ErrEvidence
		}
		if value == nil {
			b.WriteByte(0)
		} else {
			b.WriteByte(1)
			writeBytes(&b, value)
		}
		if b.Len() > maxEvidenceBytes {
			return e, ErrEvidence
		}
	}
	e.Payload = b.Bytes()
	e.Hash = evidenceHash(e)
	return e, nil
}

func evidenceHash(e Evidence) [32]byte {
	var b bytes.Buffer
	b.WriteString("CT-BILLING-EVIDENCE-HASH\x00")
	_ = binary.Write(&b, binary.BigEndian, uint32(e.CodecVersion))
	b.Write(e.SourceSchemaHash[:])
	writeBytes(&b, e.Payload)
	return sha256.Sum256(b.Bytes())
}

// DecodeEvidence verifies the codec, schema and payload hash before returning
// independent byte slices suitable for re-parsing an immutable day version.
func DecodeEvidence(e Evidence) ([]Column, map[string][]byte, error) {
	if e.CodecVersion != EvidenceCodecVersion || e.SourceSchemaHash == ([32]byte{}) || len(e.Payload) > maxEvidenceBytes || e.Hash != evidenceHash(e) {
		return nil, nil, ErrEvidence
	}
	r := bytes.NewReader(e.Payload)
	magic := make([]byte, len("CT-BILLING-EVIDENCE\x00"))
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != "CT-BILLING-EVIDENCE\x00" {
		return nil, nil, ErrEvidence
	}
	columns := make([]Column, 0, len(evidenceFields))
	values := make(map[string][]byte, len(evidenceFields))
	for _, expected := range evidenceFields {
		name, err := readBytes(r)
		if err != nil || string(name) != expected {
			return nil, nil, ErrEvidence
		}
		present, err := r.ReadByte()
		if err != nil || present > 1 {
			return nil, nil, ErrEvidence
		}
		if present == 0 {
			continue
		}
		kind, err := readBytes(r)
		if err != nil || len(kind) == 0 || len(kind) > 4096 {
			return nil, nil, ErrEvidence
		}
		columns = append(columns, Column{expected, string(kind)})
		known, err := r.ReadByte()
		if err != nil || known > 1 {
			return nil, nil, ErrEvidence
		}
		if known == 0 {
			values[expected] = nil
		} else {
			values[expected], err = readBytes(r)
			if err != nil {
				return nil, nil, ErrEvidence
			}
		}
	}
	if r.Len() != 0 {
		return nil, nil, ErrEvidence
	}
	return columns, values, nil
}

func writeBytes(b *bytes.Buffer, value []byte) {
	_ = binary.Write(b, binary.BigEndian, uint64(len(value)))
	b.Write(value)
}

func readBytes(r *bytes.Reader) ([]byte, error) {
	var n uint64
	if err := binary.Read(r, binary.BigEndian, &n); err != nil || n > uint64(r.Len()) || n > maxEvidenceBytes {
		return nil, fmt.Errorf("%w: byte frame", ErrEvidence)
	}
	value := make([]byte, int(n)) // make preserves non-nil zero-length bytes.
	_, err := io.ReadFull(r, value)
	return value, err
}

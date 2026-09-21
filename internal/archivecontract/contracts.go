// Package archivecontract defines the opt-in versioned archive protocol.
// Legacy archive progress is never a source of versioned coverage.
package archivecontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"time"
)

const ProtocolVersion = 2
const FormatVersion = 2
const CapabilityFoundation = "foundation_v2"
const CapabilityAtomicWriter = "atomic_writer_v2"

var (
	ErrConflict    = errors.New("archive_foundation_conflict")
	ErrIdentity    = errors.New("archive_identity_mismatch")
	ErrUnsupported = errors.New("archive_version_unsupported")
	ErrNotFound    = errors.New("archive_dataset_not_found")
	idPattern      = regexp.MustCompile(`^[0-9a-f]{32}$`)
	hashPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	refPattern     = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
	countPattern   = regexp.MustCompile(`^(0|[1-9][0-9]{0,37})$`)
	quotaPattern   = regexp.MustCompile(`^(0|-?[1-9][0-9]{0,37})$`)
)

type Identity struct {
	SiteID             string `json:"site_id"`
	DatasetID          string `json:"dataset_id"`
	SourceGenerationID string `json:"source_generation_id"`
}

func IDBytes(v string) ([]byte, error) {
	if !idPattern.MatchString(v) || v == "00000000000000000000000000000000" {
		return nil, ErrIdentity
	}
	return hex.DecodeString(v)
}
func (i Identity) Validate() error {
	if len(i.SiteID) == 0 || len(i.SiteID) > 64 {
		return ErrIdentity
	}
	if _, err := IDBytes(i.DatasetID); err != nil {
		return err
	}
	_, err := IDBytes(i.SourceGenerationID)
	return err
}
func (i Identity) Equal(other Identity) bool { return i == other }

type Registration struct {
	Identity
	StorageRef           string `json:"storage_ref"`
	ArchiveFormatVersion int    `json:"archive_format_version"`
	SchemaFingerprint    string `json:"schema_fingerprint"`
	SourceFingerprint    string `json:"source_fingerprint"`
}

func (r Registration) Validate() error {
	if err := r.Identity.Validate(); err != nil {
		return err
	}
	if r.ArchiveFormatVersion != FormatVersion {
		return ErrUnsupported
	}
	if !refPattern.MatchString(r.StorageRef) || !hashPattern.MatchString(r.SchemaFingerprint) || !hashPattern.MatchString(r.SourceFingerprint) {
		return ErrIdentity
	}
	return nil
}

type Dataset struct {
	Registration
	LifecycleState          string    `json:"lifecycle_state"`
	ConfigRevision          uint64    `json:"config_revision,string"`
	ObservedCatalogRevision uint64    `json:"observed_catalog_revision,string"`
	CatalogHash             string    `json:"catalog_hash,omitempty"`
	UnscopedBlockingIssues  uint64    `json:"unscoped_blocking_issues,string"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}
type FoundationStatus struct {
	Identity
	ProtocolVersion int      `json:"protocol_version"`
	FormatVersion   int      `json:"format_version"`
	Capabilities    []string `json:"capabilities"`
	WriterEpoch     uint64   `json:"writer_epoch,string"`
	CatalogRevision uint64   `json:"catalog_revision,string"`
	ReceiptID       string   `json:"receipt_id,omitempty"`
}

func (s FoundationStatus) Validate() error {
	if err := s.Identity.Validate(); err != nil {
		return err
	}
	if s.ProtocolVersion != ProtocolVersion || s.FormatVersion != FormatVersion {
		return ErrUnsupported
	}
	seen := map[string]bool{}
	for _, capability := range s.Capabilities {
		if seen[capability] || (capability != CapabilityFoundation && capability != CapabilityAtomicWriter && capability != CapabilityBackfill && capability != CapabilityReconcile && capability != CapabilitySeal && capability != CapabilityWorkflow) {
			return ErrUnsupported
		}
		seen[capability] = true
	}
	if !seen[CapabilityFoundation] {
		return ErrUnsupported
	}
	if seen[CapabilityBackfill] && !seen[CapabilityAtomicWriter] {
		return ErrUnsupported
	}
	if seen[CapabilityReconcile] && !seen[CapabilityBackfill] || seen[CapabilitySeal] && !seen[CapabilityReconcile] || seen[CapabilityWorkflow] && !seen[CapabilitySeal] {
		return ErrUnsupported
	}
	if !s.SupportsAtomicWriter() && (s.WriterEpoch != 0 || s.ReceiptID != "") {
		return ErrUnsupported
	}
	if s.ReceiptID != "" {
		if _, err := IDBytes(s.ReceiptID); err != nil || s.WriterEpoch == 0 {
			return ErrUnsupported
		}
	}
	return nil
}

func (s FoundationStatus) SupportsAtomicWriter() bool {
	for _, capability := range s.Capabilities {
		if capability == CapabilityAtomicWriter {
			return true
		}
	}
	return false
}

func (s FoundationStatus) SupportsBackfill() bool {
	for _, capability := range s.Capabilities {
		if capability == CapabilityBackfill {
			return true
		}
	}
	return false
}

// WriterGrant is an ephemeral CT authorization. Agents bound its lifetime by
// the local monotonic request-start deadline; targets never trust wall clocks
// or a cached grant to extend that authorization.
type WriterGrant struct {
	Identity
	ProtocolVersion int    `json:"protocol_version"`
	WriterEpoch     uint64 `json:"writer_epoch,string"`
	Session         string `json:"session"`
	ConfigVersion   int64  `json:"config_version"`
	LeaseSeconds    int    `json:"lease_seconds"`
}

func (g WriterGrant) Validate() error {
	if err := g.Identity.Validate(); err != nil {
		return err
	}
	if _, err := IDBytes(g.Session); err != nil {
		return err
	}
	if g.ProtocolVersion != ProtocolVersion || g.WriterEpoch == 0 || g.ConfigVersion < 0 || g.LeaseSeconds < 60 || g.LeaseSeconds > 120 {
		return ErrUnsupported
	}
	return nil
}

type CatalogDay struct {
	Date             string     `json:"date"`
	State            string     `json:"state"`
	CurrentVersionID string     `json:"current_version_id,omitempty"`
	ManifestHash     string     `json:"manifest_hash,omitempty"`
	MutationRevision uint64     `json:"mutation_revision,string"`
	AllRows          *string    `json:"all_rows"`
	ConsumeRows      *string    `json:"consume_rows"`
	ConsumeQuota     *string    `json:"consume_quota"`
	VerifiedAt       *time.Time `json:"verified_at,omitempty"`
	BlockReason      string     `json:"block_reason,omitempty"`
}

func (d CatalogDay) Validate() error {
	if _, err := time.Parse("2006-01-02", d.Date); err != nil {
		return ErrConflict
	}
	switch d.State {
	case "collecting", "needs_fill", "verifying", "dirty", "sealed", "unknown", "pending_verify", "empty_candidate", "blocked":
	default:
		return ErrConflict
	}
	if d.CurrentVersionID != "" {
		if _, err := IDBytes(d.CurrentVersionID); err != nil {
			return err
		}
	}
	if d.ManifestHash != "" && !hashPattern.MatchString(d.ManifestHash) {
		return ErrConflict
	}
	if len(d.BlockReason) > 64 {
		return ErrConflict
	}
	for _, n := range []*string{d.AllRows, d.ConsumeRows} {
		if n != nil && !countPattern.MatchString(*n) {
			return ErrConflict
		}
	}
	if d.ConsumeQuota != nil && !quotaPattern.MatchString(*d.ConsumeQuota) {
		return ErrConflict
	}
	if d.State == "sealed" && (d.CurrentVersionID == "" || d.ManifestHash == "" || d.AllRows == nil || d.ConsumeRows == nil || d.ConsumeQuota == nil || d.VerifiedAt == nil || d.VerifiedAt.IsZero() || d.BlockReason != "") {
		return ErrConflict
	}
	return nil
}

type CatalogSnapshot struct {
	Identity
	CatalogRevision        uint64       `json:"catalog_revision,string"`
	UnscopedBlockingIssues uint64       `json:"unscoped_blocking_issues,string"`
	Days                   []CatalogDay `json:"days"`
}

func (s CatalogSnapshot) Validate() error {
	if err := s.Identity.Validate(); err != nil {
		return err
	}
	if len(s.Days) > 20000 {
		return ErrConflict
	}
	seen := make(map[string]bool, len(s.Days))
	for _, d := range s.Days {
		if err := d.Validate(); err != nil {
			return err
		}
		if seen[d.Date] {
			return ErrConflict
		}
		seen[d.Date] = true
	}
	return nil
}
func (s CatalogSnapshot) Hash() (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	s.Days = append([]CatalogDay{}, s.Days...)
	sort.Slice(s.Days, func(i, j int) bool { return s.Days[i].Date < s.Days[j].Date })
	for i := range s.Days {
		if s.Days[i].VerifiedAt != nil {
			t := s.Days[i].VerifiedAt.UTC()
			s.Days[i].VerifiedAt = &t
		}
	}
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

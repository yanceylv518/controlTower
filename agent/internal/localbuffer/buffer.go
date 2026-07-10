package localbuffer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"controltower/agent/internal/fileatomic"
	"controltower/agent/internal/reporter"
)

var ErrBufferFull = errors.New("local report buffer is full")

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

type Entry struct {
	CreatedAt time.Time                   `json:"created_at"`
	LastLogID int64                       `json:"last_log_id"`
	Report    reporter.AgentReportRequest `json:"report"`
}

type FileStore struct {
	path string
}

func NewFileStore(path string) FileStore {
	return FileStore{path: path}
}

func (s FileStore) Load() ([]Entry, error) {
	data, err := fileatomic.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	data = bytes.TrimPrefix(data, utf8BOM)
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		backup, backupErr := fileatomic.ReadBackup(s.path)
		if backupErr != nil {
			return nil, err
		}
		backup = bytes.TrimPrefix(backup, utf8BOM)
		if backupErr = json.Unmarshal(backup, &entries); backupErr != nil {
			return nil, err
		}
	}
	return entries, nil
}

func (s FileStore) Save(entries []Entry) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return fileatomic.WriteFile(s.path, data, 0o600)
}

func (s FileStore) Append(entry Entry, maxLogEvents int) error {
	entries, err := s.Load()
	if err != nil {
		return err
	}
	if maxLogEvents > 0 && CountLogEvents(entries)+bufferPayloadCount(entry.Report) > maxLogEvents {
		return fmt.Errorf("%w: max log events %d", ErrBufferFull, maxLogEvents)
	}
	entries = append(entries, entry)
	return s.Save(entries)
}

func (s FileStore) DropFirst() (Entry, bool, error) {
	entries, err := s.Load()
	if err != nil {
		return Entry{}, false, err
	}
	if len(entries) == 0 {
		return Entry{}, false, nil
	}
	entry := entries[0]
	if err := s.Save(entries[1:]); err != nil {
		return Entry{}, false, err
	}
	return entry, true, nil
}

func CountLogEvents(entries []Entry) int {
	count := 0
	for _, entry := range entries {
		count += bufferPayloadCount(entry.Report)
	}
	return count
}

func bufferPayloadCount(report reporter.AgentReportRequest) int {
	return len(report.LogEvents) + len(report.LogSamples) + len(report.AggregatedMetrics)
}

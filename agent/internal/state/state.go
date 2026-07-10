package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"controltower/agent/internal/fileatomic"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

type State struct {
	LastLogID                 int64     `json:"last_log_id"`
	LastSuccessReportAt       time.Time `json:"last_success_report_at"`
	ConsecutiveReportFailures int       `json:"consecutive_report_failures"`
}

type FileStore struct {
	path string
}

func NewFileStore(path string) FileStore {
	return FileStore{path: path}
}

func (s FileStore) Load() (State, error) {
	data, err := fileatomic.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}

	data = bytes.TrimPrefix(data, utf8BOM)
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		backup, backupErr := fileatomic.ReadBackup(s.path)
		if backupErr != nil {
			return State{}, err
		}
		backup = bytes.TrimPrefix(backup, utf8BOM)
		if backupErr = json.Unmarshal(backup, &state); backupErr != nil {
			return State{}, err
		}
	}
	return state, nil
}

func (s FileStore) Save(state State) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return fileatomic.WriteFile(s.path, data, 0o600)
}

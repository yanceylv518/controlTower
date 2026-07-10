package fileatomic

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileReplacesContentAndKeepsPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if err := WriteFile(path, []byte("second"), 0o600); err != nil {
		t.Fatalf("write second: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "second" {
		t.Fatalf("content = %q", data)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("backup should be cleaned after successful replace")
	}
}

func TestReadFileReturnsPrimaryBeforeJSONValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path+".bak", []byte("backup"), 0o600); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatalf("write corrupted file: %v", err)
	}
	data, err := ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "{broken" {
		t.Fatalf("read = %q", data)
	}
}

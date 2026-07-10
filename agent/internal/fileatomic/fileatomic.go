package fileatomic

import (
	"errors"
	"os"
	"path/filepath"
)

func ReadFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return data, nil
	}
	backup, backupErr := os.ReadFile(path + ".bak")
	if backupErr == nil {
		return backup, nil
	}
	return nil, err
}

func ReadBackup(path string) ([]byte, error) {
	return os.ReadFile(path + ".bak")
}

func WriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempName)
	}
	if err := temp.Chmod(perm); err != nil {
		cleanup()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempName)
		return err
	}

	backup := path + ".bak"
	_ = os.Remove(backup)
	if _, err := os.Stat(path); err == nil {
		if err := os.Rename(path, backup); err != nil {
			_ = os.Remove(tempName)
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(tempName)
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		_ = os.Rename(backup, path)
		_ = os.Remove(tempName)
		return err
	}
	_ = os.Remove(backup)
	return nil
}

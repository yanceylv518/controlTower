// Package channeltoken persists the new-api admin login token on the agent
// host so restarts reuse the session instead of logging in again.
package channeltoken

import (
	"os"
	"strings"

	"controltower/agent/internal/fileatomic"
)

type FileTokenStore struct{ path string }

func NewFileTokenStore(path string) FileTokenStore { return FileTokenStore{path: path} }

func (s FileTokenStore) Load() (string, error) {
	if s.path == "" {
		return "", nil
	}
	data, err := fileatomic.ReadFile(s.path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (s FileTokenStore) Save(token string) error {
	if s.path == "" {
		return nil
	}
	return fileatomic.WriteFile(s.path, []byte(strings.TrimSpace(token)+"\n"), 0600)
}

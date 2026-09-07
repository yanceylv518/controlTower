package mysqlstore

import (
	"controltower/server/internal/storage"
	"testing"
)

type userScanFunc func(...any) error

func (f userScanFunc) Scan(values ...any) error { return f(values...) }

func TestScanUserPermissionMigrationCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, json    string
		full, invalid bool
	}{
		{"legacy-null-column", "", true, false},
		{"legacy-json-null", "null", true, false},
		{"explicit-empty", "[]", false, false},
		{"restricted", `["monitor.read"]`, false, false},
		{"full", `["*"]`, true, false},
		{"corrupt", `{broken`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var user storage.User
			err := scanUser(userScanFunc(func(dest ...any) error {
				*dest[3].(*string) = "admin"
				*dest[10].(*[]byte) = []byte(tc.json)
				return nil
			}), &user)
			if (err != nil) != tc.invalid {
				t.Fatalf("unexpected decode error: %v", err)
			}
			if !tc.invalid && storage.IsFullAdmin(user) != tc.full {
				t.Fatalf("wrong permissions: %#v", user.Permissions)
			}
		})
	}
}

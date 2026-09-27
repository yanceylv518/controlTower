package archivereader

import (
	"context"
	"controltower/server/internal/secrets"
	"errors"
	"strings"
	"testing"
)

type connectionFake struct {
	c   Connection
	err error
}

func (f connectionFake) ArchiveSiteExists(context.Context, string) (bool, error) { return true, nil }
func (f connectionFake) LoadArchiveConnection(context.Context, string) (Connection, error) {
	return f.c, f.err
}
func (f connectionFake) SaveArchiveConnection(context.Context, string, Connection, string) error {
	return nil
}

func TestSavedConnectionValidationTLSAndFailClosed(t *testing.T) {
	encrypted, _ := secrets.Encrypt("key", "p@ss?/#")
	c := Connection{Host: "::1", Port: 3306, Database: "archive", Username: "reader", TLS: true, EncryptedPassword: encrypted}
	r := Reader{SecretKey: "key"}
	cfg, err := r.connectionConfig(c)
	if err != nil || cfg.Addr != "[::1]:3306" || cfg.Passwd != "p@ss?/#" || cfg.TLSConfig != "true" {
		t.Fatal("invalid config", err)
	}
	for _, host := range []string{"tcp(host)", "host/db", "host\n"} {
		c.Host = host
		if host != "tcp(host)" && c.Validate() == nil {
			t.Fatal(host)
		}
	}
	r.Connections = connectionFake{err: errors.New("store failed")}
	if _, _, err = r.openJob(context.Background(), "site"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("unexpected fallback", err)
	}
	r.Connections = connectionFake{c: Connection{SourceHash: strings.Repeat("a", 64)}}
	if _, _, err = r.openJob(context.Background(), "site"); !errors.Is(err, ErrQuery) {
		t.Fatal("invalid saved config ignored", err)
	}
}

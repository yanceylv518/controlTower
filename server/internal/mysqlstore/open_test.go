package mysqlstore

import "testing"

func TestOpenUsesRegisteredMySQLDriver(t *testing.T) {
	db, err := Open("controltower:password@tcp(127.0.0.1:3306)/control_tower?parseTime=true&loc=UTC")
	if err != nil {
		t.Fatalf("open mysql db handle: %v", err)
	}
	defer db.Close()
}

package dashboard

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
)

func TestPassthroughUsersRejectInvalidAdminFilter(t *testing.T) {
	w := httptest.NewRecorder()
	(&PassthroughHandler{}).Users(w, httptest.NewRequest("GET", "/?site=test&exclude_admin=wrong", nil))
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestPassthroughUsersAdminFilterBeforePagination(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires local CT_MYSQL_TEST_DSN")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// A connection-local table shadows users only for this test connection.
	// No existing CT or source user records are modified.
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`CREATE TEMPORARY TABLE users(id BIGINT PRIMARY KEY,username VARCHAR(40),display_name VARCHAR(40),quota BIGINT,used_quota BIGINT,status INT,created_at BIGINT,last_login_at BIGINT,role INT)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO users VALUES(1,'admin','管理员',0,0,1,0,0,10),(2,'root','超级管理员',0,0,1,0,0,100),(3,'alice','客户甲',0,0,1,0,0,1),(4,'bob','客户乙',0,0,2,0,0,1)`); err != nil {
		t.Fatal(err)
	}
	h := &PassthroughHandler{Config: customerQueryConfig{}, pools: map[string]passthroughPool{"test": {encrypted: "test-pool", db: db}}}
	for _, tc := range []struct {
		query string
		total int
		id    int64
		role  int
	}{
		{"exclude_admin=1&limit=1", 2, 3, 1},
		{"exclude_admin=true&limit=1&offset=1", 2, 4, 1},
		{"exclude_admin=0&limit=1", 4, 1, 10},
		{"limit=1&offset=1", 4, 2, 100},
		{"exclude_admin=1&keyword=alice", 1, 3, 1},
		{"exclude_admin=1&user_ids=1,3&status=1", 1, 3, 1},
		{"exclude_admin=1&user_ids=1,2", 0, 0, 0},
	} {
		w := httptest.NewRecorder()
		h.Users(w, httptest.NewRequest("GET", "/?site=test&"+tc.query, nil))
		var result struct {
			Items []PassthroughUser
			Total int
		}
		if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || result.Total != tc.total {
			t.Fatal(tc.query, w.Code, w.Body.String(), err)
		}
		if tc.total == 0 {
			if len(result.Items) != 0 {
				t.Fatal(result)
			}
			continue
		}
		if len(result.Items) != 1 || result.Items[0].ID != tc.id || result.Items[0].Role != tc.role {
			t.Fatal(tc.query, result)
		}
	}
}

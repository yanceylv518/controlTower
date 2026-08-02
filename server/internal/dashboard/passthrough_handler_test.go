package dashboard

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type unconfiguredReadonlyStore struct{}

func (unconfiguredReadonlyStore) ReadonlyDSNForSite(string) (string, error) { return "", nil }
func (unconfiguredReadonlyStore) UpdateReadonlyDSNForSite(string, string, time.Time) error {
	return nil
}

func TestPassthroughPageLimits(t *testing.T) {
	r := httptest.NewRequest("GET", "/?limit=999&offset=-3", nil)
	limit, offset := queryPage(r, 100)
	if limit != 100 || offset != 0 {
		t.Fatalf("queryPage = %d,%d", limit, offset)
	}
}

func TestPassthroughWindowRejectsMoreThan31Days(t *testing.T) {
	end := time.Now().UTC()
	start := end.Add(-32 * 24 * time.Hour)
	r := httptest.NewRequest("GET", "/?start_time="+start.Format(time.RFC3339)+"&end_time="+end.Format(time.RFC3339), nil)
	if _, _, err := queryWindow(r); err == nil {
		t.Fatal("expected oversized time window to fail")
	}
}

func TestPassthroughSummaryRedactsAndTruncates(t *testing.T) {
	value := "email alice@example.com from 192.168.1.2\n" + strings.Repeat("x", 250)
	redacted := redactSummary(value)
	if strings.Contains(redacted, "alice@example.com") || strings.Contains(redacted, "192.168.1.2") {
		t.Fatalf("sensitive values remain: %q", redacted)
	}
	if len([]rune(redacted)) > 201 {
		t.Fatalf("summary is too long: %d", len([]rune(redacted)))
	}
}

func TestPassthroughAdminScopeAllowsAllUsers(t *testing.T) {
	r := httptest.NewRequest("GET", "/?site=cn", nil)
	site, ids, err := passthroughScope(r)
	if err != nil || site != "cn" || len(ids) != 0 {
		t.Fatalf("scope = %q,%v error = %v", site, ids, err)
	}
}

func TestPassthroughUnconfiguredSiteDegradesGracefully(t *testing.T) {
	h := PassthroughHandler{Config: unconfiguredReadonlyStore{}}
	r := httptest.NewRequest("GET", "/?site=cn&user_ids=12", nil)
	w := httptest.NewRecorder()
	h.Users(w, r)
	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Configured bool `json:"configured"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Configured {
		t.Fatal("unconfigured site reported configured")
	}
}

func TestPassthroughPoolAndTimeoutGuards(t *testing.T) {
	db, err := sql.Open("mysql", "unused:unused@tcp(127.0.0.1:1)/unused")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	configureReadonlyDB(db)
	if got := db.Stats().MaxOpenConnections; got != 2 {
		t.Fatalf("MaxOpenConnections = %d", got)
	}
	if readonlyQueryTimeout != 5*time.Second {
		t.Fatalf("readonlyQueryTimeout = %s", readonlyQueryTimeout)
	}
}

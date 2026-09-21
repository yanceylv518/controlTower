package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"controltower/agent/internal/config"
	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
	"github.com/go-sql-driver/mysql"
)

// Runs the real concurrent poll/preparation/writer loop against isolated MySQL
// schemas. Only the authenticated control service is a fixture; store-side
// leasing, registration and conflict handling have separate MySQL regressions.
func TestAutomaticArchiveLoopMySQL(t *testing.T) {
	dsn := os.Getenv("CT_ARCHIVE_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_ARCHIVE_TEST_DSN for isolated archive loop integration")
	}
	dbConfig, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	dbConfig.DBName = ""
	admin, err := sql.Open("mysql", dbConfig.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	raw := make([]byte, 8)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	base := "ct_auto_loop_" + hex.EncodeToString(raw)
	source, target := base+"_s", base+"_t"
	for _, name := range []string{source, target} {
		if _, err = admin.Exec("CREATE DATABASE " + name); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if strings.HasPrefix(name, "ct_auto_loop_") {
				_, _ = admin.Exec("DROP DATABASE " + name)
			}
		})
	}
	_, err = admin.Exec("CREATE TABLE " + source + ".logs (id BIGINT PRIMARY KEY,created_at BIGINT,type INT,quota BIGINT,other TEXT,prompt_tokens BIGINT,completion_tokens BIGINT,INDEX idx_created(created_at,id)) ENGINE=InnoDB")
	if err != nil {
		t.Fatal(err)
	}
	created := time.Now().Add(-10 * time.Minute)
	_, err = admin.Exec("INSERT INTO "+source+".logs VALUES(1,?,2,50,'{}',5,10)", created.Unix())
	if err != nil {
		t.Fatal(err)
	}
	i := af.Identity{SiteID: "auto-loop", DatasetID: strings.Repeat("a", 32), SourceGenerationID: strings.Repeat("b", 32)}
	updates := make(chan ac.Status, 256)
	var mu sync.Mutex
	var registration *af.Registration
	var epoch uint64
	sessions := map[string]uint64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var st ac.Status
		if r.Header.Get("Authorization") != "Bearer test-instance-token" || json.NewDecoder(r.Body).Decode(&st) != nil || !st.Validate() {
			t.Error("invalid Agent poll")
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		c := ac.Default()
		c.Version = 1
		c.AgentID = "agent"
		c.InstanceID = "instance"
		c.Running = true
		c.IntervalSeconds = 2
		if registration != nil {
			c.FullHistory = true
			c.Version = 2
		}
		out := ac.Response{SiteID: i.SiteID, Config: c, LeaseSeconds: 120}
		if st.Prepared != nil {
			if registration != nil && *registration != *st.Prepared {
				t.Error("restart changed registration")
			}
			copy := *st.Prepared
			registration = &copy
			out.PreparedAccepted = true
			out.Config.FullHistory = true
			out.Config.Version = 2
		} else if st.Foundation != nil {
			if registration == nil || !st.Foundation.Identity.Equal(i) {
				t.Error("writer before registration")
				w.WriteHeader(409)
				return
			}
			if sessions[st.Session] == 0 {
				epoch++
				sessions[st.Session] = epoch
			}
			out.Granted = st.AppliedVersion == out.Config.Version
			if out.Granted {
				out.WriterGrant = &af.WriterGrant{Identity: i, ProtocolVersion: af.ProtocolVersion, WriterEpoch: sessions[st.Session], Session: st.Session, ConfigVersion: out.Config.Version, LeaseSeconds: 120}
			}
		} else if st.PrepareDiscovered {
			out.Prepare = &i
			out.PrepareToken = strings.Repeat("d", 32)
		}
		select {
		case updates <- st:
		default:
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(server.Close)
	dbConfig.DBName = source
	sourceDSN := dbConfig.FormatDSN()
	dbConfig.DBName = target
	cfg := config.Config{AgentID: "agent", InstanceID: "instance", ServerURL: server.URL, AgentToken: "test-instance-token", LogDSN: sourceDSN, LogArchiveDSN: dbConfig.FormatDSN(), LogArchiveEnabled: true, LogArchiveManaged: true, LogArchiveBatchSize: 10, DataDir: t.TempDir()}
	month := created.In(time.FixedZone("Beijing", 28800)).Format("200601")
	waitCopied := func(previous string) ac.Status {
		timer := time.NewTimer(35 * time.Second)
		defer timer.Stop()
		var last ac.Status
		for {
			select {
			case last = <-updates:
				if last.Session == previous {
					continue
				}
				if last.State == "error" {
					t.Fatalf("automatic loop failed: %+v", last)
				}
				if last.Workflow == nil {
					continue
				}
				var count int
				if admin.QueryRow("SELECT COUNT(*) FROM "+target+".logs_"+month+" WHERE id=1").Scan(&count) == nil && count == 1 {
					return last
				}
			case <-timer.C:
				t.Fatalf("automatic loop did not copy: %+v", last)
			}
		}
	}
	stop := startAutomaticManagedArchiveWithPollInterval(context.Background(), cfg, 50*time.Millisecond)
	defer func() { stop() }()
	first := waitCopied("")
	var taskID string
	if err = admin.QueryRow("SELECT JSON_UNQUOTE(JSON_EXTRACT(state_json,'$.task_id')) FROM " + target + ".archive_workflow WHERE singleton_id=1").Scan(&taskID); err != nil || taskID == "" {
		t.Fatalf("missing persistent workflow: %v", err)
	}
	stop()
	stop = startAutomaticManagedArchiveWithPollInterval(context.Background(), cfg, 50*time.Millisecond)
	second := waitCopied(first.Session)
	var resumed string
	if err = admin.QueryRow("SELECT JSON_UNQUOTE(JSON_EXTRACT(state_json,'$.task_id')) FROM " + target + ".archive_workflow WHERE singleton_id=1").Scan(&resumed); err != nil || resumed != taskID || second.Foundation.WriterEpoch <= first.Foundation.WriterEpoch {
		t.Fatalf("restart reset workflow: %q -> %q, %v", taskID, resumed, err)
	}
}

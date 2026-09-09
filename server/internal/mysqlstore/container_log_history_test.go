package mysqlstore

import (
	"context"
	cl "controltower/internal/containerlog"
	"controltower/server/internal/storage"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestContainerLogHistoryPagination(t *testing.T) {
	if os.Getenv("CT_MYSQL_TEST_DSN") == "" {
		t.Skip("requires test MySQL")
	}
	db, err := Open(os.Getenv("CT_MYSQL_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err = ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	site := fmt.Sprintf("history-%d", time.Now().UnixNano())
	now := time.Now().UTC().Truncate(time.Second)
	for _, id := range []string{site + "a", site + "b"} {
		if err = s.CreateInstance(storage.Instance{ID: id, SiteID: site, Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		defer db.Exec("DELETE FROM instances WHERE id=?", id)
	}
	defer db.Exec("DELETE FROM container_log_tasks WHERE instance_id IN (?,?)", site+"a", site+"b")
	for batch := 0; batch < 25; batch++ {
		for source := 0; source < 2; source++ {
			q := cl.Query{BatchID: fmt.Sprintf("batch-%d", batch), Container: "new-api", From: now.Add(-time.Hour), To: now, RequestID: fmt.Sprintf("request-%d", batch)}
			raw, _ := json.Marshal(q)
			instance := site + "a"
			if source == 1 {
				instance = site + "b"
			}
			_, err = db.Exec(`INSERT INTO container_log_tasks(id,instance_id,agent_id,actor_id,actor,actor_name,query_json,status,result_json,created_at) VALUES(?,?,?,?,?,?,?,'succeeded','{"status":"succeeded","lines":["PRIVATE LOG BODY"]}',?)`, fmt.Sprintf("%s-%d-%d", site, batch, source), instance, "agent", 123+batch%2, fmt.Sprintf("operator-%d", batch%2), "name", string(raw), now.Add(-time.Duration(batch)*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	f := cl.HistoryFilter{Site: site, Page: 1, PageSize: 20}
	page, err := s.ListContainerLogHistory(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 25 || len(page.Items) != 20 {
		t.Fatalf("first page: %+v", page)
	}
	for _, g := range page.Items {
		if len(g.Tasks) != 2 || len(g.Tasks[0].Result.Lines) != 0 {
			t.Fatal("batch split or log body leaked")
		}
	}
	firstID := page.Items[0].ID
	f.Page = 2
	page, err = s.ListContainerLogHistory(ctx, f)
	if err != nil || len(page.Items) != 5 || page.Items[0].ID == firstID {
		t.Fatal("second page", page, err)
	}
	f.Page = 1
	f.ActorID = 123
	page, err = s.ListContainerLogHistory(ctx, f)
	if err != nil || page.Total != 13 {
		t.Fatal("owner scope", page, err)
	}
	f.ActorID = 0
	f.Actor = "operator-1"
	page, err = s.ListContainerLogHistory(ctx, f)
	if err != nil || page.Total != 12 {
		t.Fatal("actor filter", page, err)
	}
	f.Actor = ""
	f.RequestID = "request-0"
	page, err = s.ListContainerLogHistory(ctx, f)
	if err != nil || page.Total != 1 || len(page.Items[0].Tasks) != 2 {
		t.Fatal("request filter", page, err)
	}
	f.RequestID = ""
	f.From = now.Add(-time.Minute)
	f.To = now
	page, err = s.ListContainerLogHistory(ctx, f)
	if err != nil || page.Total != 2 {
		t.Fatal("submission time filter", page, err)
	}
	f.From = time.Time{}
	f.To = time.Time{}
	f.Site = "other-site"
	page, err = s.ListContainerLogHistory(ctx, f)
	if err != nil || page.Total != 0 {
		t.Fatal("site isolation", page, err)
	}
	// Legacy records are assigned a stable batch once, even across second boundaries.
	_, err = db.Exec(`UPDATE container_log_tasks SET query_json=JSON_REMOVE(query_json,'$.batch_id') WHERE instance_id IN (?,?)`, site+"a", site+"b")
	if err != nil {
		t.Fatal(err)
	}
	f.Site = site
	page, err = s.ListContainerLogHistory(ctx, f)
	if err != nil || page.Total != 25 || len(page.Items[0].Tasks) != 2 {
		t.Fatal("legacy grouping", page, err)
	}
	again, err := s.ListContainerLogHistory(ctx, f)
	if err != nil || again.Items[0].ID != page.Items[0].ID {
		t.Fatal("unstable legacy grouping", err)
	}
}

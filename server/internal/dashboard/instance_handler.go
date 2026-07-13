package dashboard

import (
	"controltower/server/internal/storage"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"time"
)

type InstanceStore interface {
	ListInstances() ([]storage.Instance, error)
	InstanceByID(string) (storage.Instance, bool, error)
	CreateInstance(storage.Instance) error
	UpdateInstance(string, string, bool, time.Time) error
	CreateInstanceToken(storage.InstanceToken) error
	ExpireInstanceTokens(string, time.Time, time.Time) error
}
type InstanceHandler struct {
	Store   InstanceStore
	Runtime RuntimeStore
	Pepper  string
}

var instanceIDPattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

func tokenHash(p, t string) string {
	x := sha256.Sum256([]byte(p + t))
	return hex.EncodeToString(x[:])
}
func newToken() (string, error) {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	return hex.EncodeToString(b), e
}
func (i InstanceHandler) List(w http.ResponseWriter, r *http.Request) {
	v, e := i.Store.ListInstances()
	if e != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	writeDashboardJSON(w, 200, map[string]any{"items": v})
}
func (i InstanceHandler) Create(w http.ResponseWriter, r *http.Request) {
	var q struct {
		ID   string `json:"instance_id"`
		Name string `json:"name"`
	}
	if json.NewDecoder(r.Body).Decode(&q) != nil || !instanceIDPattern.MatchString(q.ID) {
		writeDashboardError(w, 400, "invalid_instance_id")
		return
	}
	if _, ok, _ := i.Store.InstanceByID(q.ID); ok {
		writeDashboardError(w, 409, "instance_exists")
		return
	}
	n := time.Now().UTC()
	v := storage.Instance{ID: q.ID, Name: q.Name, Enabled: true, CreatedAt: n, UpdatedAt: n}
	if i.Store.CreateInstance(v) != nil {
		writeDashboardError(w, 500, "create_failed")
		return
	}
	t, e := newToken()
	if e != nil {
		writeDashboardError(w, 500, "create_failed")
		return
	}
	_ = i.Store.CreateInstanceToken(storage.InstanceToken{InstanceID: q.ID, TokenHash: tokenHash(i.Pepper, t), CreatedAt: n})
	writeDashboardJSON(w, 201, map[string]string{"instance_id": q.ID, "name": q.Name, "token": t})
}
func (i InstanceHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	v, ok, e := i.Store.InstanceByID(id)
	if e != nil || !ok {
		writeDashboardError(w, 404, "instance_not_found")
		return
	}
	var q struct {
		Name    *string `json:"name"`
		Enabled *bool   `json:"enabled"`
	}
	_ = json.NewDecoder(r.Body).Decode(&q)
	if q.Name != nil {
		v.Name = *q.Name
	}
	if q.Enabled != nil {
		v.Enabled = *q.Enabled
	}
	_ = i.Store.UpdateInstance(id, v.Name, v.Enabled, time.Now().UTC())
	writeDashboardJSON(w, 200, v)
}
func (i InstanceHandler) Rotate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok, _ := i.Store.InstanceByID(id); !ok {
		writeDashboardError(w, 404, "instance_not_found")
		return
	}
	n := time.Now().UTC()
	g := n.Add(24 * time.Hour)
	_ = i.Store.ExpireInstanceTokens(id, g, n)
	t, e := newToken()
	if e != nil {
		writeDashboardError(w, 500, "create_failed")
		return
	}
	_ = i.Store.CreateInstanceToken(storage.InstanceToken{InstanceID: id, TokenHash: tokenHash(i.Pepper, t), CreatedAt: n})
	writeDashboardJSON(w, 200, map[string]any{"token": t, "grace_until": g})
}

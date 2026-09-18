package dashboard

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type channelCacheConnector struct{ conn *channelCacheConn }

func (c channelCacheConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }
func (c channelCacheConnector) Driver() driver.Driver                        { return customerQueryDriver{} }

type channelCacheConn struct {
	calls int
	fail  bool
	name  string
}

func (c *channelCacheConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *channelCacheConn) Close() error              { return nil }
func (c *channelCacheConn) Begin() (driver.Tx, error) { return channelCacheTx{}, nil }
func (c *channelCacheConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return channelCacheTx{}, nil
}

type channelCacheTx struct{}

func (channelCacheTx) Commit() error   { return nil }
func (channelCacheTx) Rollback() error { return nil }
func (c *channelCacheConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.calls++
	if c.fail {
		return nil, errors.New("unavailable")
	}
	return &channelCacheRows{name: c.name}, nil
}

type channelCacheRows struct {
	name string
	read bool
}

func (r *channelCacheRows) Columns() []string { return []string{"id", "name"} }
func (r *channelCacheRows) Close() error      { return nil }
func (r *channelCacheRows) Next(dest []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	dest[0] = int64(7)
	dest[1] = r.name
	return nil
}
func TestReadonlyChannelCacheIsolationExpiryAndFailure(t *testing.T) {
	h := &PassthroughHandler{}
	c := &channelCacheConn{name: "first"}
	db := sql.OpenDB(channelCacheConnector{c})
	defer db.Close()
	read := func(db *sql.DB) []PassthroughLog {
		items := []PassthroughLog{{ChannelID: 7}, {ChannelID: 8}}
		h.hydrateCachedChannelNames(context.Background(), db, items)
		return items
	}
	require.Equal(t, "first", read(db)[0].ChannelName)
	c.name = "renamed"
	require.Equal(t, "first", read(db)[0].ChannelName)
	require.Equal(t, 1, c.calls)
	// Confirmed missing IDs are cached too, preventing repeated empty lookups.
	h.channelNames[readonlyChannelKey{db, 7}] = readonlyChannelName{"first", time.Now().Add(-time.Second)}
	require.Equal(t, "renamed", read(db)[0].ChannelName)
	c2 := &channelCacheConn{fail: true, name: "other site"}
	db2 := sql.OpenDB(channelCacheConnector{c2})
	defer db2.Close()
	require.Empty(t, read(db2)[0].ChannelName)
	c2.fail = false
	require.Equal(t, "other site", read(db2)[0].ChannelName)
	require.Equal(t, 2, c2.calls)
}

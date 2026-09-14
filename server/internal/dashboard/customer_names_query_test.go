package dashboard

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"
)

type customerQueryConfig struct{ unconfiguredReadonlyStore }

func (customerQueryConfig) ReadonlyDSNForSite(string) (string, error) { return "test-pool", nil }

type customerQueryConnector struct{ conn *customerQueryConn }

func (c customerQueryConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }
func (c customerQueryConnector) Driver() driver.Driver                        { return customerQueryDriver{c.conn} }

type customerQueryDriver struct{ conn *customerQueryConn }

func (d customerQueryDriver) Open(string) (driver.Conn, error) { return d.conn, nil }

type customerQueryConn struct {
	rows    *customerQueryRows
	query   string
	bounded bool
}

func (c *customerQueryConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *customerQueryConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected transaction")
}
func (c *customerQueryConn) Close() error { return nil }
func (c *customerQueryConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.query = query
	deadline, ok := ctx.Deadline()
	c.bounded = ok && time.Until(deadline) <= 2*time.Second
	if len(args) != 0 {
		return nil, errors.New("unexpected args")
	}
	return c.rows, nil
}

type customerQueryRows struct {
	values  [][]driver.Value
	index   int
	failure error
	closed  bool
}

func (r *customerQueryRows) Columns() []string { return []string{"id", "username", "display_name"} }
func (r *customerQueryRows) Close() error      { r.closed = true; return nil }
func (r *customerQueryRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		if r.failure != nil {
			return r.failure
		}
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

func TestCustomerNamesReadsOnlyProfilesAndRejectsPartialQuery(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "names", true: "partial failure"}[partial], func(t *testing.T) {
			rows := &customerQueryRows{values: [][]driver.Value{{int64(104), " alice ", "Alice Display"}, {int64(105), "", "Bob Display"}, {int64(106), "", ""}, {int64(0), "invalid", ""}}}
			if partial {
				rows.failure = errors.New("connection lost")
			}
			conn := &customerQueryConn{rows: rows}
			db := sql.OpenDB(customerQueryConnector{conn})
			defer db.Close()
			h := &PassthroughHandler{Config: customerQueryConfig{}, pools: map[string]passthroughPool{"site": {encrypted: "test-pool", db: db}}}
			names, err := h.CustomerNames(context.Background(), "site")
			if partial {
				if err == nil {
					t.Fatal("partial query accepted")
				}
			} else if err != nil || !reflect.DeepEqual(names, map[int64]string{104: "alice", 105: "Bob Display"}) {
				t.Fatalf("names=%v err=%v", names, err)
			}
			if conn.query != "SELECT id,COALESCE(username,''),COALESCE(display_name,'') FROM users" || !conn.bounded || !rows.closed {
				t.Fatalf("unbounded/unclosed/wrong query: %+v", conn)
			}
		})
	}
}

package logarchive

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"time"

	af "controltower/internal/archivecontract"
	"github.com/go-sql-driver/mysql"
)

type operationContextKey struct{}
type operationContext struct {
	phase, date string
	observe     func(af.Operation)
}

// The observer runs on the calling worker goroutine. Publication must copy it
// before sharing with the independent control heartbeat.
func WithOperationObserver(ctx context.Context, observe func(af.Operation)) context.Context {
	return context.WithValue(ctx, operationContextKey{}, operationContext{observe: observe})
}
func workflowOperationContext(ctx context.Context, phase, date string) context.Context {
	o, _ := ctx.Value(operationContextKey{}).(operationContext)
	o.phase, o.date = phase, date
	return context.WithValue(ctx, operationContextKey{}, o)
}
func reportOperation(ctx context.Context, code, database, table string) {
	o, _ := ctx.Value(operationContextKey{}).(operationContext)
	if o.observe == nil {
		return
	}
	p := af.Operation{Phase: o.phase, Date: o.date, Code: code, Database: database, Table: table, State: "executing", StartedAt: time.Now().UTC()}
	if p.Validate() == nil {
		o.observe(p)
	}
}

// Diagnose preserves the structured driver cause without copying its text.
func Diagnose(err error) af.Diagnostic {
	d := af.Diagnostic{Code: scanFailureCode(err), OccurredAt: time.Now().UTC()}
	var scan *scanError
	if errors.As(err, &scan) {
		d.RowID, d.RowBytes = scan.sourceID, scan.bytes
	}
	var db *mysql.MySQLError
	if errors.As(err, &db) {
		d.MySQLNumber = db.Number
		state := string(db.SQLState[:])
		valid := len(state) == 5
		for _, c := range state {
			valid = valid && (c >= 'A' && c <= 'Z' || c >= '0' && c <= '9')
		}
		if valid {
			d.SQLState = state
		}
		switch db.Number {
		case 1044, 1142, 1143, 1227:
			d.Code = "database_permission_denied"
		case 1045:
			d.Code = "database_authentication_failed"
		case 1049:
			d.Code = "database_missing"
		case 1146:
			d.Code = "database_table_missing"
		case 1054:
			d.Code = "database_column_missing"
		case 1061:
			d.Code = "database_index_conflict"
		case 1062:
			d.Code = "database_duplicate_key"
		case 1205:
			d.Code = "database_lock_timeout"
		case 1213:
			d.Code = "database_deadlock"
		case 1040, 1203:
			d.Code = "database_connection_limit"
		case 1290:
			d.Code = "database_write_restricted"
		case 1114:
			d.Code = "database_table_full"
		case 2006, 2013:
			d.Code = "database_connection_lost"
		default:
			d.Code = "database_error"
		}
		return d
	}
	var network net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		d.Code = "operation_timeout"
	case errors.Is(err, context.Canceled):
		d.Code = "operation_cancelled"
	case errors.Is(err, driver.ErrBadConn), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		d.Code = "database_connection_lost"
	case errors.As(err, &network):
		d.Code = "database_connection_failed"
		if network.Timeout() {
			d.Code = "database_connection_timeout"
		}
	}
	return d
}

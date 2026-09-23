package dashboard

import (
	"context"
	"errors"
	"log"
	"net"

	"github.com/go-sql-driver/mysql"
)

// Log enough to distinguish regex limits, database errors and timeouts without
// recording DSNs, SQL arguments, log content or scan errors containing values.
func logReadonlyQueryFailure(site, operation, stage string, err error) {
	category := "query_error"
	var code uint16
	var state string
	var dbErr *mysql.MySQLError
	var networkErr net.Error
	switch {
	case errors.As(err, &dbErr):
		category, code, state = "mysql", dbErr.Number, string(dbErr.SQLState[:])
	case errors.Is(err, context.DeadlineExceeded):
		category = "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		category = "canceled"
	case errors.As(err, &networkErr) && networkErr.Timeout():
		category = "network_timeout"
	}
	log.Printf("readonly query failed site=%q operation=%s stage=%s category=%s mysql_errno=%d sqlstate=%q error_type=%T", site, operation, stage, category, code, state, err)
}

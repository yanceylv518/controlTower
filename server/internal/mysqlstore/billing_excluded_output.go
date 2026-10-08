package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"math/big"
)

// Written in the same transaction as the page cursor; never enters bill exports.
func appendExcludedOutputDiagnostics(ctx context.Context, tx *sql.Tx, job string, details []billing.RequestDetail) error {
	type total struct {
		count  int64
		amount *big.Rat
	}
	groups := map[string]*total{}
	for _, d := range details {
		if !d.DiagnosticOnly {
			continue
		}
		v := groups[d.ModelName]
		if v == nil {
			v = &total{amount: new(big.Rat)}
			groups[d.ModelName] = v
		}
		v.count++
		if amount, ok := new(big.Rat).SetString(d.Charge.Total); ok {
			v.amount.Add(v.amount, amount)
		}
	}
	for model, v := range groups {
		if _, err := tx.ExecContext(ctx, `INSERT INTO billing_excluded_output_stats(job_id,model_name,request_count,total_amount) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE request_count=request_count+VALUES(request_count),total_amount=total_amount+VALUES(total_amount)`, job, model, v.count, v.amount.FloatString(12)); err != nil {
			return err
		}
	}
	return nil
}

func (s Store) addExcludedOutputDiagnostics(ctx context.Context, bill *billing.WorkspaceBill) error {
	rows, err := s.db.QueryContext(ctx, `SELECT model_name,request_count,CAST(total_amount AS CHAR) FROM billing_excluded_output_stats WHERE job_id=?`, bill.Job.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var model, amount string
		var count int64
		if err = rows.Scan(&model, &count, &amount); err != nil {
			return err
		}
		bill.EmptyCount += count
		bill.EmptyAmount = billing.MergeBefore(bill.EmptyAmount, amount)
		for i := range bill.Models {
			if bill.Models[i].Model == model {
				bill.Models[i].EmptyCount += count
				bill.Models[i].EmptyAmount = billing.MergeBefore(bill.Models[i].EmptyAmount, amount)
			}
		}
	}
	return rows.Err()
}

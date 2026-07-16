package mysqlstore

import (
	"context"
	"strings"

	"controltower/server/internal/storage"
)

func (s Store) QueryRequestDimensions(instanceID string, requestIDs []string) ([]storage.RequestDimension, error) {
	if instanceID == "" || len(requestIDs) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(requestIDs)), ",")
	args := make([]any, 0, len(requestIDs)+1)
	args = append(args, instanceID)
	for _, id := range requestIDs {
		args = append(args, id)
	}
	var out []storage.RequestDimension
	for _, source := range []struct{ table, label string }{{"log_samples", "sample"}, {"log_events", "event"}} {
		query := `SELECT source_log_id,request_id,user_id,username,channel_id,model_name,token_name FROM ` + source.table + ` WHERE instance_id=? AND request_id IN (` + placeholders + `)`
		rows, err := s.db.QueryContext(context.Background(), query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			v := storage.RequestDimension{Source: source.label, InstanceID: instanceID}
			if err := rows.Scan(&v.SourceLogID, &v.RequestID, &v.UserID, &v.Username, &v.ChannelID, &v.ModelName, &v.TokenName); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, v)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

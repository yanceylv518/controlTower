package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"controltower/server/internal/storage"
)

const presetColumns = "id,name,description,permissions,version,created_by,updated_by,created_at,updated_at"

func readPermissionPresets(rows *sql.Rows) ([]storage.PermissionPreset, error) {
	defer rows.Close()
	items := []storage.PermissionPreset{}
	for rows.Next() {
		var item storage.PermissionPreset
		var permissions []byte
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &permissions, &item.Version, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(permissions, &item.Permissions); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s Store) ListPermissionPresets(ctx context.Context) ([]storage.PermissionPreset, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+presetColumns+" FROM permission_presets ORDER BY id")
	if err != nil {
		return nil, err
	}
	return readPermissionPresets(rows)
}

type permissionPresetTx struct {
	ctx context.Context
	tx  *sql.Tx
}

func (s Store) WithPermissionPresetTransaction(ctx context.Context, apply func(storage.PermissionPresetTransaction) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := apply(permissionPresetTx{ctx, tx}); err != nil {
		return err
	}
	return tx.Commit()
}

func (t permissionPresetTx) Administrators() ([]storage.User, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT id,username,password_hash,role,scope_site,scope_user_ids,enabled,created_at,updated_at,display_name,permissions FROM users WHERE role='admin' ORDER BY id FOR UPDATE")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []storage.User{}
	for rows.Next() {
		var user storage.User
		if err := scanUser(rows, &user); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (t permissionPresetTx) Presets() ([]storage.PermissionPreset, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT "+presetColumns+" FROM permission_presets ORDER BY id FOR UPDATE")
	if err != nil {
		return nil, err
	}
	return readPermissionPresets(rows)
}

func (t permissionPresetTx) SavePreset(p *storage.PermissionPreset) error {
	permissions, err := json.Marshal(p.Permissions)
	if err != nil {
		return err
	}
	if p.ID == 0 {
		result, err := t.tx.ExecContext(t.ctx, "INSERT INTO permission_presets(name,description,permissions,version,created_by,updated_by,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)", p.Name, p.Description, permissions, p.Version, p.CreatedBy, p.UpdatedBy, p.CreatedAt, p.UpdatedAt)
		if err != nil {
			return err
		}
		p.ID, err = result.LastInsertId()
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, "UPDATE permission_presets SET name=?,description=?,permissions=?,version=?,updated_by=?,updated_at=? WHERE id=?", p.Name, p.Description, permissions, p.Version, p.UpdatedBy, p.UpdatedAt, p.ID)
	return err
}

func (t permissionPresetTx) DeletePreset(id int64) error {
	_, err := t.tx.ExecContext(t.ctx, "DELETE FROM permission_presets WHERE id=?", id)
	return err
}
func (t permissionPresetTx) SetPermissions(id int64, permissions []string, now time.Time) error {
	value, err := json.Marshal(permissions)
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, "UPDATE users SET permissions=?,updated_at=? WHERE id=?", value, now, id)
	return err
}
func (t permissionPresetTx) InsertAudit(audit storage.OperationAudit) error {
	return insertOperationAuditTx(t.tx, audit)
}

package voicealert

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type Person struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Phone    string `json:"phone"`
	Enabled  bool   `json:"enabled"`
	Revision int64  `json:"revision"`
}

func (p Person) Validate() error {
	if strings.TrimSpace(p.Name) == "" || utf8.RuneCountInString(p.Name) > 80 || !phonePattern.MatchString(p.Phone) {
		return fmt.Errorf("请填写姓名和有效的国内手机或固话")
	}
	return nil
}

func (s Store) People(ctx context.Context) ([]Person, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,phone,enabled,revision FROM operations_people ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Person{}
	for rows.Next() {
		var p Person
		if err = rows.Scan(&p.ID, &p.Name, &p.Phone, &p.Enabled, &p.Revision); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

var ErrConflict = errors.New("配置已被修改，请刷新后重试")

func lockVoice(ctx context.Context, tx *sql.Tx) error {
	var id int
	return tx.QueryRowContext(ctx, `SELECT id FROM voice_dispatch_guard WHERE id=1 FOR UPDATE`).Scan(&id)
}

func (s Store) SavePerson(ctx context.Context, p Person, actor string) (Person, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Phone = strings.TrimSpace(p.Phone)
	if err := p.Validate(); err != nil {
		return p, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	if err = lockVoice(ctx, tx); err != nil {
		return p, err
	}
	if p.ID == "" {
		p.ID, err = randomID(16)
		if err != nil {
			return p, err
		}
		p.Revision = 1
		_, err = tx.ExecContext(ctx, `INSERT INTO operations_people(id,name,phone,enabled,revision,updated_at) VALUES(?,?,?,?,1,?)`, p.ID, p.Name, p.Phone, p.Enabled, time.Now().UTC())
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE operations_people SET name=?,phone=?,enabled=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?`, p.Name, p.Phone, p.Enabled, time.Now().UTC(), p.ID, p.Revision)
		if err == nil {
			n, e := result.RowsAffected()
			if e != nil {
				return p, e
			}
			if n != 1 {
				return p, ErrConflict
			}
			p.Revision++
		}
	}
	if err != nil {
		return p, fmt.Errorf("保存失败，号码可能已被其他人员使用")
	}
	if err = auditTrial(ctx, tx, "operations_people.configure", p.ID, actor); err != nil {
		return p, err
	}
	return p, tx.Commit()
}

func auditTrial(ctx context.Context, tx *sql.Tx, action, id, actor string) error {
	key, err := randomID(16)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO operation_audits(id,instance_id,operation_type,target_type,target_id,actor_id,before_summary,after_summary,status,created_at) VALUES(?,'',?,'trial_followup',?,?,'{}','{}','succeeded',?)`, key, action, id, actor, time.Now().UTC())
	return err
}

package mysqlstore

import (
	"database/sql"
	"time"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/storage"
)

// Lock site configuration so source switching cannot race snapshot persistence.
func lockChannelSource(tx *sql.Tx, site string) (string, error) {
	rows, err := tx.Query(`SELECT logs_readonly_dsn FROM instances WHERE COALESCE(NULLIF(site_id,''),id)=? ORDER BY id FOR UPDATE`, site)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var selected string
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			return "", err
		}
		if selected == "" {
			selected = value
		}
	}
	return selected, rows.Err()
}

func (s Store) AcceptAgentChannelSnapshots(instance string) (bool, error) {
	var configured bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM instances i JOIN instances source
	ON COALESCE(NULLIF(i.site_id,''),i.id)=COALESCE(NULLIF(source.site_id,''),source.id)
	WHERE source.id=? AND i.logs_readonly_dsn<>'')`, instance).Scan(&configured)
	return !configured, err
}

func (s Store) StoreReadonlyChannels(site string, channels []channelcontrol.Channel, at time.Time, encrypted string) error {
	return s.storeServerChannels(site, channels, at, encrypted)
}

func (s Store) storeServerChannels(site string, channels []channelcontrol.Channel, at time.Time, source string) error {
	instance, err := controlInstanceForSite(s.db, site)
	if err != nil {
		return err
	}
	snapshots := make([]storage.ChannelSnapshot, 0, len(channels))
	for _, c := range channels {
		priority, group := c.Priority, c.Group
		snapshots = append(snapshots, storage.ChannelSnapshot{ID: randomCommandID(), InstanceID: instance, ChannelID: c.ID, ChannelName: c.Name, Status: channelStatusLabel(c.Status), Weight: int64(c.Weight), Priority: &priority, ModelsText: c.Models, GroupName: &group, CapturedAt: at})
	}
	return s.syncChannelSnapshotsAt(instance, snapshots, at, &source)
}

// Server collections are site-wide. Consolidate legacy collector copies in the
// same transaction, retaining confirmed writes newer than the collection start.
func consolidateChannelSnapshots(tx *sql.Tx, site, instance string, incoming []storage.ChannelSnapshot, at time.Time) ([]storage.ChannelSnapshot, error) {
	rows, err := tx.Query(`SELECT c.id,c.channel_id,c.channel_name,c.status,c.weight,c.models_text,c.group_name,c.priority,c.captured_at
	FROM channel_current c JOIN instances i ON i.id=c.instance_id
	WHERE COALESCE(NULLIF(i.site_id,''),i.id)=? ORDER BY c.captured_at,c.instance_id DESC FOR UPDATE`, site)
	if err != nil {
		return nil, err
	}
	latest := map[int64]storage.ChannelSnapshot{}
	for rows.Next() {
		var v storage.ChannelSnapshot
		var group sql.NullString
		var priority sql.NullInt64
		if err = rows.Scan(&v.ID, &v.ChannelID, &v.ChannelName, &v.Status, &v.Weight, &v.ModelsText, &group, &priority, &v.CapturedAt); err != nil {
			rows.Close()
			return nil, err
		}
		v.InstanceID = instance
		if group.Valid {
			v.GroupName = &group.String
		}
		if priority.Valid {
			v.Priority = &priority.Int64
		}
		if v.CapturedAt.After(at) {
			latest[v.ChannelID] = v
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := make([]storage.ChannelSnapshot, 0, len(incoming)+len(latest))
	for _, v := range incoming {
		if newer, ok := latest[v.ChannelID]; ok {
			v = newer
			delete(latest, v.ChannelID)
		}
		result = append(result, v)
	}
	for _, v := range latest {
		result = append(result, v)
	}
	_, err = tx.Exec(`DELETE c FROM channel_current c JOIN instances i ON i.id=c.instance_id WHERE COALESCE(NULLIF(i.site_id,''),i.id)=? AND c.instance_id<>?`, site, instance)
	return result, err
}

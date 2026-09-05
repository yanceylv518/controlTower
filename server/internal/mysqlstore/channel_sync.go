package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"time"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/channelupdates"
	"controltower/server/internal/storage"
)

// ApplyChannelWrite updates only fields confirmed written, preserving the
// anchor and all other metadata. All collectors for the site share the result.
func (s Store) ApplyChannelWrite(siteID string, channelID int64, weight *uint, priority *int64, status *int, at time.Time) error {
	_, err := s.db.ExecContext(context.Background(), `UPDATE channel_current c JOIN instances i ON i.id=c.instance_id
SET c.weight=COALESCE(?,c.weight),c.priority=COALESCE(?,c.priority),c.status=COALESCE(?,c.status),c.captured_at=?
WHERE CASE WHEN i.site_id='' THEN i.id ELSE i.site_id END=? AND c.channel_id=? AND c.captured_at<=?`, weight, priority, status, at, siteID, channelID, at)
	if err == nil {
		channelupdates.Notify()
	}
	return err
}

func (s Store) StoreFreshChannels(siteID string, channels []channelcontrol.Channel, at time.Time) error {
	instanceID, err := controlInstanceForSite(s.db, siteID)
	if err != nil {
		return err
	}
	return s.StoreInstanceChannels(instanceID, channels, at)
}

func (s Store) StoreInstanceChannels(instanceID string, channels []channelcontrol.Channel, at time.Time) error {
	snapshots := make([]storage.ChannelSnapshot, 0, len(channels))
	for _, c := range channels {
		priority := c.Priority
		snapshots = append(snapshots, storage.ChannelSnapshot{ID: randomCommandID(), InstanceID: instanceID, ChannelID: c.ID, ChannelName: c.Name, Status: strconv.Itoa(c.Status), Weight: int64(c.Weight), Priority: &priority, ModelsText: c.Models, GroupName: &c.Group, CapturedAt: at})
	}
	return s.SyncChannelSnapshotsAt(instanceID, snapshots, at)
}

func applyCompletedChannelWrite(tx *sql.Tx, command storage.ChannelCommand, at time.Time) error {
	var payload struct {
		Weight   *uint  `json:"weight"`
		Priority *int64 `json:"priority"`
		Status   *int   `json:"status"`
	}
	if err := json.Unmarshal([]byte(command.PayloadJSON), &payload); err != nil {
		return err
	}
	if payload.Weight == nil && payload.Priority == nil && payload.Status == nil {
		return nil
	}
	_, err := tx.Exec(`UPDATE channel_current c JOIN instances i ON i.id=c.instance_id
JOIN instances source ON source.id=?
SET c.weight=COALESCE(?,c.weight),c.priority=COALESCE(?,c.priority),c.status=COALESCE(?,c.status),c.captured_at=?
WHERE CASE WHEN i.site_id='' THEN i.id ELSE i.site_id END=CASE WHEN source.site_id='' THEN source.id ELSE source.site_id END
AND c.channel_id=? AND c.captured_at<=?`, command.InstanceID, payload.Weight, payload.Priority, payload.Status, at, command.ChannelID, at)
	return err
}

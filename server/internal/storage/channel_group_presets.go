package storage

import (
	"context"
	"errors"
	"time"
)

var ErrGroupPresetConflict = errors.New("group preset revision conflict")

type ChannelGroupPreset struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Groups []string `json:"groups"`
}
type ChannelGroupPresets struct {
	Items    []ChannelGroupPreset `json:"items"`
	Revision int64                `json:"revision"`
}
type ChannelGroupPresetStore interface {
	LoadChannelGroupPresets(context.Context, string) (ChannelGroupPresets, error)
	SaveChannelGroupPresets(context.Context, string, ChannelGroupPresets, string, time.Time) error
}

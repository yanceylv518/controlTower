package tuning

import "errors"

// ManualDisabled is durable operator ownership, independent of circuit recovery.
const ManualDisabled = "manual_disabled"

var ErrChannelControlBusy = errors.New("channel control is busy")
var ErrManualStatusUnavailable = errors.New("manual channel status requires direct control")

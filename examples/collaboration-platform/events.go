package main

import "github.com/zoobzio/hookz"

// Key is a type alias for hookz.Key for cleaner usage within the package
type Key = hookz.Key

// Canvas collaboration events following the EventCanvasAction naming pattern
const (
	EventCanvasAction   Key = "canvas.action"
	EventCanvasSync     Key = "canvas.sync"
	EventCanvasUpdate   Key = "canvas.update"
	EventCanvasConflict Key = "canvas.conflict"
)

// User presence events
const (
	EventUserJoined   Key = "user.joined"
	EventUserLeft     Key = "user.left"
	EventUserActive   Key = "user.active"
	EventUserInactive Key = "user.inactive"
)

// Broadcast events
const (
	EventBroadcastSent     Key = "broadcast.sent"
	EventBroadcastReceived Key = "broadcast.received"
	EventBroadcastFailed   Key = "broadcast.failed"
)

// History events
const (
	EventHistorySaved  Key = "history.saved"
	EventHistoryUndo   Key = "history.undo"
	EventHistoryRedo   Key = "history.redo"
	EventHistoryPurged Key = "history.purged"
)

// Permission events
const (
	EventPermissionGranted Key = "permission.granted"
	EventPermissionDenied  Key = "permission.denied"
	EventPermissionChanged Key = "permission.changed"
)

// Analytics events
const (
	EventAnalyticsAction    Key = "analytics.action"
	EventAnalyticsUser      Key = "analytics.user"
	EventAnalyticsSession   Key = "analytics.session"
	EventAnalyticsMetrics   Key = "analytics.metrics"
)

// System events
const (
	EventSystemError       Key = "system.error"
	EventSystemMaintenance Key = "system.maintenance"
	EventSystemShutdown    Key = "system.shutdown"
)
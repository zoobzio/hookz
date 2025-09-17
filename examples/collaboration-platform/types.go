package main

import (
	"sync"
	"time"
)

// Action represents any user action on the canvas
type Action struct {
	ID        string
	UserID    string
	Type      ActionType
	Target    string // Object ID being modified
	Data      interface{}
	Timestamp time.Time
	Version   int64 // For conflict resolution
}

// ActionType defines types of canvas actions
type ActionType string

const (
	ActionDraw     ActionType = "draw"
	ActionMove     ActionType = "move"
	ActionResize   ActionType = "resize"
	ActionDelete   ActionType = "delete"
	ActionTypeText ActionType = "type"
	ActionSelect   ActionType = "select"
	ActionPaste    ActionType = "paste"
	ActionGroup    ActionType = "group"
)

// User represents a collaborator
type User struct {
	ID          string
	Name        string
	Email       string
	Role        UserRole
	CursorPos   Position
	Color       string // User's cursor color
	LastSeen    time.Time
	IsActive    bool
	SessionID   string
}

// UserRole defines permission levels
type UserRole string

const (
	RoleViewer UserRole = "viewer"
	RoleEditor UserRole = "editor"
	RoleAdmin  UserRole = "admin"
)

// Position represents x,y coordinates
type Position struct {
	X float64
	Y float64
}

// CanvasObject represents any object on the canvas
type CanvasObject struct {
	ID         string
	Type       string
	Position   Position
	Size       Size
	Properties map[string]interface{}
	CreatedBy  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Version    int64
}

// Size represents width and height
type Size struct {
	Width  float64
	Height float64
}

// Room represents a collaborative session
type Room struct {
	ID        string
	Name      string
	CanvasID  string
	Users     map[string]*User
	UsersMu   sync.RWMutex
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Broadcast represents an event to be sent to users
type Broadcast struct {
	RoomID    string
	Event     string
	Data      interface{}
	Exclude   []string // User IDs to exclude
	OnlyTo    []string // User IDs to send only to (if set)
	Priority  int      // Higher priority sends first
	Timestamp time.Time
}

// PresenceUpdate represents user presence information
type PresenceUpdate struct {
	UserID    string
	RoomID    string
	CursorPos Position
	Status    string // "active", "idle", "away"
	Timestamp time.Time
}

// HistoryEntry represents an action in the history log
type HistoryEntry struct {
	ID        string
	RoomID    string
	Action    Action
	Timestamp time.Time
	Undoable  bool
}

// AnalyticsEvent for tracking user behavior
type AnalyticsEvent struct {
	EventType  string
	UserID     string
	RoomID     string
	Properties map[string]interface{}
	Timestamp  time.Time
}

// PermissionCache caches permission checks
type PermissionCache struct {
	cache map[string]bool
	mu    sync.RWMutex
	ttl   time.Duration
}

func NewPermissionCache(ttl time.Duration) *PermissionCache {
	return &PermissionCache{
		cache: make(map[string]bool),
		ttl:   ttl,
	}
}

func (p *PermissionCache) Get(key string) (bool, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	val, exists := p.cache[key]
	return val, exists
}

func (p *PermissionCache) Set(key string, value bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cache[key] = value
	
	// Simple TTL implementation - clear after duration
	go func() {
		time.Sleep(p.ttl)
		p.mu.Lock()
		delete(p.cache, key)
		p.mu.Unlock()
	}()
}

// CollaborationMetrics tracks system performance
type CollaborationMetrics struct {
	ActiveUsers      int
	ActionsProcessed int64
	BroadcastsSent   int64
	AverageLatency   time.Duration
	MemoryPerUser    int64
	EventQueueDepth  int
}
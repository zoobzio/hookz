package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zoobzio/hookz"
)

// Canvas represents the collaborative drawing canvas
type Canvas struct {
	ID      string
	Objects map[string]*CanvasObject
	mu      sync.RWMutex
	
	// Hook system for async operations
	hooks *hookz.Hooks[Action]
	
	// Services
	server    *SyncServer
	broadcast *BroadcastService
	presence  *PresenceTracker
	history   *HistoryService
	perms     *PermissionService
	analytics *AnalyticsService
	
	// Metrics
	actionsProcessed atomic.Int64
	renderTime       atomic.Int64 // microseconds
}

// NewCanvas creates a production-ready collaborative canvas
func NewCanvas(id string) *Canvas {
	// Use 30 workers for parallel processing
	hooks := hookz.New[Action](hookz.WithWorkers(30), hookz.WithTimeout(2*time.Second))
	
	c := &Canvas{
		ID:        id,
		Objects:   make(map[string]*CanvasObject),
		hooks:     hooks,
		server:    NewSyncServer(),
		broadcast: NewBroadcastService(),
		presence:  NewPresenceTracker(),
		history:   NewHistoryService(),
		perms:     NewPermissionService(),
		analytics: NewAnalyticsService(),
	}
	
	// Register all async handlers
	c.registerHooks()
	
	return c
}

// registerHooks sets up all async event handlers
func (c *Canvas) registerHooks() {
	// Server sync - critical but shouldn't block UI
	c.hooks.Hook(EventCanvasAction, func(ctx context.Context, action Action) error {
		if err := c.server.Sync(ctx, c.ID, action); err != nil {
			log.Printf("Server sync failed: %v", err)
			// Retry logic would go here in production
		}
		return nil
	})
	
	// Broadcast to other users - important for collaboration
	c.hooks.Hook(EventCanvasAction, func(ctx context.Context, action Action) error {
		broadcast := Broadcast{
			RoomID:    c.ID,
			Event:     string(EventCanvasUpdate),
			Data:      action,
			Exclude:   []string{action.UserID}, // Don't send back to originator
			Priority:  1,
			Timestamp: time.Now(),
		}
		
		if err := c.broadcast.Send(ctx, broadcast); err != nil {
			log.Printf("Broadcast failed: %v", err)
		}
		return nil
	})
	
	// Update presence - low priority
	c.hooks.Hook(EventCanvasAction, func(ctx context.Context, action Action) error {
		update := PresenceUpdate{
			UserID:    action.UserID,
			RoomID:    c.ID,
			Status:    "active",
			Timestamp: time.Now(),
		}
		
		c.presence.Update(ctx, update)
		return nil
	})
	
	// Save to history - important for undo/redo
	c.hooks.Hook(EventCanvasAction, func(ctx context.Context, action Action) error {
		entry := HistoryEntry{
			ID:        fmt.Sprintf("hist_%d", time.Now().UnixNano()),
			RoomID:    c.ID,
			Action:    action,
			Timestamp: time.Now(),
			Undoable:  true,
		}
		
		if err := c.history.Save(ctx, entry); err != nil {
			log.Printf("History save failed: %v", err)
		}
		return nil
	})
	
	// Check permissions - cache for performance
	c.hooks.Hook(EventCanvasAction, func(ctx context.Context, action Action) error {
		allowed, err := c.perms.Check(ctx, action.UserID, action.Type)
		if err != nil {
			log.Printf("Permission check failed: %v", err)
			return nil
		}
		
		if !allowed {
			log.Printf("Permission denied for user %s action %s", action.UserID, action.Type)
			// In production, might send error back to user
		}
		return nil
	})
	
	// Track analytics - lowest priority
	c.hooks.Hook(EventCanvasAction, func(ctx context.Context, action Action) error {
		event := AnalyticsEvent{
			EventType: string(action.Type),
			UserID:    action.UserID,
			RoomID:    c.ID,
			Properties: map[string]interface{}{
				"target": action.Target,
				"version": action.Version,
			},
			Timestamp: time.Now(),
		}
		
		c.analytics.Track(ctx, event)
		return nil
	})
}

// HandleUserAction processes user actions with instant UI feedback
func (c *Canvas) HandleUserAction(ctx context.Context, action Action) error {
	// CRITICAL: Update local state immediately
	start := time.Now()
	
	// Update canvas state
	c.updateCanvas(action)
	
	// Render UI instantly
	c.renderUI()
	
	// Track render time
	renderMicros := time.Since(start).Microseconds()
	c.renderTime.Store(renderMicros)
	
	// Everything else happens async
	if err := c.hooks.Emit(ctx, EventCanvasAction, action); err != nil {
		// Log but don't fail - UI already updated
		log.Printf("Failed to emit canvas action: %v", err)
	}
	
	c.actionsProcessed.Add(1)
	
	return nil // UI updated instantly!
}

// updateCanvas applies the action to local state
func (c *Canvas) updateCanvas(action Action) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	switch action.Type {
	case ActionDraw:
		// Add new object
		obj := &CanvasObject{
			ID:        action.Target,
			Type:      "shape",
			Position:  Position{X: 0, Y: 0},
			CreatedBy: action.UserID,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Version:   action.Version,
		}
		c.Objects[obj.ID] = obj
		
	case ActionMove:
		// Update position
		if obj, exists := c.Objects[action.Target]; exists {
			if pos, ok := action.Data.(Position); ok {
				obj.Position = pos
				obj.UpdatedAt = time.Now()
				obj.Version = action.Version
			}
		}
		
	case ActionDelete:
		// Remove object
		delete(c.Objects, action.Target)
		
	case ActionResize:
		// Update size
		if obj, exists := c.Objects[action.Target]; exists {
			if size, ok := action.Data.(Size); ok {
				obj.Size = size
				obj.UpdatedAt = time.Now()
				obj.Version = action.Version
			}
		}
	}
}

// renderUI simulates UI rendering
func (c *Canvas) renderUI() {
	// In real app, this would trigger React/Vue/etc re-render
	// Simulate render time
	time.Sleep(5 * time.Millisecond)
}

// GetMetrics returns current performance metrics
func (c *Canvas) GetMetrics() CollaborationMetrics {
	return CollaborationMetrics{
		ActiveUsers:      c.presence.GetActiveCount(),
		ActionsProcessed: c.actionsProcessed.Load(),
		BroadcastsSent:   c.broadcast.GetSentCount(),
		AverageLatency:   time.Duration(c.renderTime.Load()) * time.Microsecond,
		EventQueueDepth:  0, // Would check hookz queue depth
	}
}

// Shutdown gracefully closes the canvas
func (c *Canvas) Shutdown() error {
	log.Println("Shutting down canvas...")
	return c.hooks.Close()
}

// SimulateSynchronousMode shows the old blocking way
func SimulateSynchronousMode(action Action) time.Duration {
	start := time.Now()
	
	// Simulate all operations blocking sequentially
	time.Sleep(500 * time.Millisecond) // Server sync
	time.Sleep(200 * time.Millisecond) // Broadcast
	time.Sleep(100 * time.Millisecond) // Presence update
	time.Sleep(300 * time.Millisecond) // History save
	time.Sleep(150 * time.Millisecond) // Permission check
	time.Sleep(200 * time.Millisecond) // Analytics
	time.Sleep(5 * time.Millisecond)   // UI render
	
	return time.Since(start)
}
package main

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// SyncServer simulates backend synchronization
type SyncServer struct {
	latency    time.Duration
	errorRate  float64
	syncs      atomic.Int64
	failures   atomic.Int64
}

func NewSyncServer() *SyncServer {
	return &SyncServer{
		latency:   500 * time.Millisecond,
		errorRate: 0.02, // 2% failure rate
	}
}

func (s *SyncServer) Sync(ctx context.Context, canvasID string, action Action) error {
	select {
	case <-time.After(s.latency):
		if rand.Float64() < s.errorRate {
			s.failures.Add(1)
			return fmt.Errorf("sync server unavailable")
		}
		s.syncs.Add(1)
		return nil
	case <-ctx.Done():
		s.failures.Add(1)
		return ctx.Err()
	}
}

func (s *SyncServer) GetStats() (syncs, failures int64) {
	return s.syncs.Load(), s.failures.Load()
}

// BroadcastService handles event distribution to users
type BroadcastService struct {
	connections map[string]chan Broadcast // Simulated WebSocket connections
	mu          sync.RWMutex
	sent        atomic.Int64
	dropped     atomic.Int64
}

func NewBroadcastService() *BroadcastService {
	return &BroadcastService{
		connections: make(map[string]chan Broadcast),
	}
}

func (b *BroadcastService) Connect(userID string) chan Broadcast {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	ch := make(chan Broadcast, 100)
	b.connections[userID] = ch
	return ch
}

func (b *BroadcastService) Disconnect(userID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	if ch, exists := b.connections[userID]; exists {
		close(ch)
		delete(b.connections, userID)
	}
}

func (b *BroadcastService) Send(ctx context.Context, broadcast Broadcast) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	
	// Simulate network latency
	time.Sleep(50 * time.Millisecond)
	
	for userID, ch := range b.connections {
		// Check if user should receive this broadcast
		if b.shouldSend(userID, broadcast) {
			select {
			case ch <- broadcast:
				b.sent.Add(1)
			default:
				// Channel full, drop message
				b.dropped.Add(1)
			}
		}
	}
	
	return nil
}

func (b *BroadcastService) shouldSend(userID string, broadcast Broadcast) bool {
	// Check exclusions
	for _, excluded := range broadcast.Exclude {
		if userID == excluded {
			return false
		}
	}
	
	// Check if only specific users should receive
	if len(broadcast.OnlyTo) > 0 {
		for _, target := range broadcast.OnlyTo {
			if userID == target {
				return true
			}
		}
		return false
	}
	
	return true
}

func (b *BroadcastService) GetSentCount() int64 {
	return b.sent.Load()
}

// PresenceTracker manages user presence and cursor positions
type PresenceTracker struct {
	users    map[string]*PresenceUpdate
	mu       sync.RWMutex
	updates  atomic.Int64
}

func NewPresenceTracker() *PresenceTracker {
	return &PresenceTracker{
		users: make(map[string]*PresenceUpdate),
	}
}

func (p *PresenceTracker) Update(ctx context.Context, update PresenceUpdate) {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	// Simulate processing
	time.Sleep(20 * time.Millisecond)
	
	p.users[update.UserID] = &update
	p.updates.Add(1)
}

func (p *PresenceTracker) GetActiveCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	
	active := 0
	cutoff := time.Now().Add(-30 * time.Second)
	
	for _, presence := range p.users {
		if presence.Timestamp.After(cutoff) && presence.Status == "active" {
			active++
		}
	}
	
	return active
}

// HistoryService manages action history for undo/redo
type HistoryService struct {
	entries  []HistoryEntry
	mu       sync.Mutex
	saves    atomic.Int64
	batching bool
	batch    []HistoryEntry
	batchMu  sync.Mutex
}

func NewHistoryService() *HistoryService {
	h := &HistoryService{
		entries:  make([]HistoryEntry, 0, 10000),
		batching: true,
		batch:    make([]HistoryEntry, 0, 100),
	}
	
	// Start batch processor
	if h.batching {
		go h.processBatches()
	}
	
	return h
}

func (h *HistoryService) Save(ctx context.Context, entry HistoryEntry) error {
	if h.batching {
		// Add to batch
		h.batchMu.Lock()
		h.batch = append(h.batch, entry)
		h.batchMu.Unlock()
		return nil
	}
	
	// Direct save (simulated)
	time.Sleep(100 * time.Millisecond)
	
	h.mu.Lock()
	h.entries = append(h.entries, entry)
	h.mu.Unlock()
	
	h.saves.Add(1)
	return nil
}

func (h *HistoryService) processBatches() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	
	for range ticker.C {
		h.batchMu.Lock()
		if len(h.batch) == 0 {
			h.batchMu.Unlock()
			continue
		}
		
		// Process batch
		toSave := h.batch
		h.batch = make([]HistoryEntry, 0, 100)
		h.batchMu.Unlock()
		
		// Simulate batch save
		time.Sleep(50 * time.Millisecond)
		
		h.mu.Lock()
		h.entries = append(h.entries, toSave...)
		h.mu.Unlock()
		
		h.saves.Add(int64(len(toSave)))
	}
}

// PermissionService checks user permissions with caching
type PermissionService struct {
	cache      *PermissionCache
	checks     atomic.Int64
	cacheHits  atomic.Int64
}

func NewPermissionService() *PermissionService {
	return &PermissionService{
		cache: NewPermissionCache(5 * time.Minute),
	}
}

func (p *PermissionService) Check(ctx context.Context, userID string, action ActionType) (bool, error) {
	p.checks.Add(1)
	
	// Check cache first
	key := fmt.Sprintf("%s:%s", userID, action)
	if allowed, exists := p.cache.Get(key); exists {
		p.cacheHits.Add(1)
		return allowed, nil
	}
	
	// Simulate permission check
	time.Sleep(50 * time.Millisecond)
	
	// Simple permission logic
	allowed := true
	if action == ActionDelete {
		// Only some users can delete
		allowed = rand.Float64() > 0.3
	}
	
	// Cache result
	p.cache.Set(key, allowed)
	
	return allowed, nil
}

// AnalyticsService tracks user behavior
type AnalyticsService struct {
	events   []AnalyticsEvent
	mu       sync.Mutex
	tracked  atomic.Int64
	batching bool
}

func NewAnalyticsService() *AnalyticsService {
	return &AnalyticsService{
		events:   make([]AnalyticsEvent, 0, 10000),
		batching: true,
	}
}

func (a *AnalyticsService) Track(ctx context.Context, event AnalyticsEvent) {
	// Simulate async tracking
	if a.batching {
		time.Sleep(5 * time.Millisecond) // Fast local queue
	} else {
		time.Sleep(200 * time.Millisecond) // Slow API call
	}
	
	a.mu.Lock()
	a.events = append(a.events, event)
	a.mu.Unlock()
	
	a.tracked.Add(1)
}

func (a *AnalyticsService) GetEventCount() int64 {
	return a.tracked.Load()
}
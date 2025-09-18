package hookz

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zoobzio/clockz"
)

// Option configures a Hooks service during creation.
type Option func(*config)

// config holds internal configuration for service creation.
type config struct {
	clock        clockz.Clock // Time abstraction for deterministic testing
	workers      int
	timeout      time.Duration
	queueSize    int
	backpressure *BackpressureConfig
	overflow     *OverflowConfig
}

// WithWorkers sets the number of worker goroutines for async hook execution.
// Default is 10 workers.
func WithWorkers(count int) Option {
	return func(c *config) {
		c.workers = count
	}
}

// WithTimeout sets the global timeout for all hook executions.
// Default is no timeout (0).
func WithTimeout(timeout time.Duration) Option {
	return func(c *config) {
		c.timeout = timeout
	}
}

// WithQueueSize sets the worker pool queue size.
// Default is 0, which auto-calculates as workers * 2.
func WithQueueSize(size int) Option {
	return func(c *config) {
		c.queueSize = size
	}
}

// WithClock sets the clock implementation for time operations.
// Default is clockz.RealClock for production use.
// Use clockz.FakeClock for deterministic testing.
func WithClock(clock clockz.Clock) Option {
	return func(c *config) {
		c.clock = clock
	}
}

// BackpressureConfig configures backpressure behavior for queue management.
// Backpressure provides temporary delays when queue utilization is high,
// allowing brief wait periods instead of immediate rejection.
type BackpressureConfig struct {
	// Maximum time Emit() will block waiting for queue space
	MaxWait time.Duration

	// Queue utilization threshold to start applying backpressure (0.0-1.0)
	// Example: 0.8 means start backing off when queue is 80% full
	StartThreshold float32

	// Backpressure strategy: "fixed", "linear", "exponential"
	Strategy string
}

// WithBackpressure configures backpressure behavior.
// When enabled, Emit() may block briefly instead of failing immediately
// when the queue is full, smoothing out microsecond-level traffic spikes.
func WithBackpressure(cfg BackpressureConfig) Option {
	return func(c *config) {
		c.backpressure = &cfg
	}
}

// OverflowConfig configures overflow queue behavior for sustained load.
// Overflow queues provide additional buffering beyond the primary queue,
// absorbing traffic bursts without blocking callers.
type OverflowConfig struct {
	// Maximum number of tasks to buffer beyond primary queue
	Capacity int

	// How frequently to attempt draining overflow to primary queue
	DrainInterval time.Duration

	// Strategy for overflow eviction when overflow queue fills
	// "fifo" (drop oldest), "lifo" (drop newest), "reject" (return error)
	EvictionStrategy string
}

// WithOverflow configures overflow queue behavior.
// When enabled, tasks that cannot fit in the primary queue are buffered
// in a secondary queue and drained as capacity becomes available.
func WithOverflow(cfg OverflowConfig) Option {
	return func(c *config) {
		c.overflow = &cfg
	}
}

// Resource limits prevent memory exhaustion attacks.
// These limits are enforced during hook registration.
const (
	maxHooksPerEvent = 100   // Prevents single event from dominating memory
	maxTotalHooks    = 10000 // Prevents unlimited hook registration across all events
)

// Hooks is the primary hook service implementation that manages event hooks.
//
// This struct provides:
//   - Hook registration and storage
//   - Event emission and hook coordination
//   - Worker pool for async execution
//   - Resource limits and service lifecycle
//   - Global timeout configuration
//
// Thread Safety:
// All methods are protected by a read-write mutex to ensure safe
// concurrent access to the hook storage and service state.
//
// Usage Pattern:
// Always embed Hooks as a private field, never as an anonymous field:
//
//	type OrderService struct {
//	    hooks hookz.Hooks[Order] // CORRECT - private field
//	    // NOT: hookz.Hooks[Order] - avoid anonymous embedding
//	}
//
//	func (s *OrderService) Events() *hookz.Hooks[Order] {
//	    return &s.hooks // Return reference for external use
//	}
type Hooks[T any] struct {
	clock         clockz.Clock // Time abstraction injected at creation
	hooks         map[string][]hookEntry[T]
	workers       *workerPool[T]
	mu            sync.RWMutex
	GlobalTimeout time.Duration
	totalHooks    int // Tracks total hook count across all events
	closed        bool

	// Metrics field - zero initialization provides safe defaults
	metrics Metrics
}

// New creates a new hook service with the specified options.
//
// Default configuration:
//   - 10 worker goroutines for async hook execution
//   - No global timeout (0)
//   - Auto-calculated queue size (workers * 2)
//
// Example:
//
//	// Default configuration
//	service := hookz.New[User]()
//
//	// Custom configuration
//	service := hookz.New[Order](
//	    hookz.WithWorkers(20),
//	    hookz.WithTimeout(5*time.Second),
//	    hookz.WithQueueSize(100),
//	)
//	defer service.Close()
func New[T any](opts ...Option) *Hooks[T] {
	cfg := config{
		clock:     clockz.RealClock, // default to real clock
		workers:   10,               // default
		timeout:   0,                // no timeout default
		queueSize: 0,                // auto-calculate from workers
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.queueSize == 0 {
		cfg.queueSize = cfg.workers * 2 // maintain current behavior
	}

	impl := &Hooks[T]{
		clock:         cfg.clock,
		hooks:         make(map[string][]hookEntry[T]),
		GlobalTimeout: cfg.timeout,
		closed:        false,
		totalHooks:    0,
	}

	impl.workers = newWorkerPoolWithResilience[T](cfg, &impl.metrics)
	return impl
}

// Hook registers a callback function for the specified event.
// Global timeout configuration (if set) applies to all hook executions.
func (h *Hooks[T]) Hook(event Key, callback func(context.Context, T) error) (Hook, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Check service state
	if h.closed {
		return Hook{}, ErrServiceClosed
	}

	// Enforce resource limits to prevent memory exhaustion
	if len(h.hooks[event]) >= maxHooksPerEvent {
		return Hook{}, ErrTooManyHooks
	}

	// Enforce total hook limit across all events
	if h.totalHooks >= maxTotalHooks {
		return Hook{}, ErrTooManyHooks
	}

	// Create new hook entry
	id := h.generateID()
	entry := hookEntry[T]{
		id:       id,
		callback: callback,
	}

	// Add to hook storage
	h.hooks[event] = append(h.hooks[event], entry)

	// Increment total hook counter
	h.totalHooks++

	// Return hook handle with unhook closure
	return Hook{
		unhook: func() error {
			return h.removeHook(event, id)
		},
	}, nil
}

// removeHook safely removes a hook by ID.
func (h *Hooks[T]) removeHook(event, id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	hooks := h.hooks[event]
	for i, hook := range hooks {
		if hook.id == id {
			// Remove hook from slice efficiently
			h.hooks[event] = append(hooks[:i], hooks[i+1:]...)

			// Clean up empty events to prevent memory leaks
			if len(h.hooks[event]) == 0 {
				delete(h.hooks, event)
			}

			// Decrement total hook counter
			h.totalHooks--

			return nil
		}
	}
	return ErrHookNotFound
}

// Unhook removes a specific hook using its handle.
func (h *Hooks[T]) Unhook(hook Hook) error {
	return hook.Unhook()
}

// Clear removes all hooks for the specified event.
func (h *Hooks[T]) Clear(event Key) int {
	h.mu.Lock()
	defer h.mu.Unlock()

	count := len(h.hooks[event])
	h.totalHooks -= count // Decrement total hook counter
	delete(h.hooks, event)
	return count
}

// ClearAll removes all hooks for all events.
func (h *Hooks[T]) ClearAll() int {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Count all hooks across all events
	count := 0
	for _, hooks := range h.hooks {
		count += len(hooks)
	}

	h.hooks = make(map[string][]hookEntry[T])
	h.totalHooks = 0 // Reset total hook counter
	return count
}

// Emit executes all hooks for the event using the provided context.
func (h *Hooks[T]) Emit(ctx context.Context, event Key, data T) error {
	// Get hooks under read lock to minimize lock contention
	h.mu.RLock()
	if h.closed {
		h.mu.RUnlock()
		return ErrServiceClosed
	}

	// Copy hooks slice to prevent race conditions during iteration
	originalHooks := h.hooks[event]
	hooks := make([]hookEntry[T], len(originalHooks))
	copy(hooks, originalHooks)
	h.mu.RUnlock()

	// No hooks registered for this event
	if len(hooks) == 0 {
		return nil
	}

	// Submit hook tasks to worker pool
	for _, hook := range hooks {
		task := hookTask[T]{
			ctx:   ctx,
			data:  data,
			hook:  hook,
			event: event,
		}

		if err := h.workers.submit(task); err != nil {
			// Worker pool is full - return error immediately
			return err
		}
	}

	return nil
}

// Metrics returns current service metrics with thread-safe access.
// RegisteredHooks requires mutex acquisition for consistency with Hook/Unhook operations.
// All counter values are read atomically for thread safety.
func (h *Hooks[T]) Metrics() Metrics {
	h.mu.RLock()
	registeredHooks := int64(h.totalHooks)
	h.mu.RUnlock()

	return Metrics{
		QueueDepth:      atomic.LoadInt64(&h.metrics.QueueDepth),
		QueueCapacity:   int64(cap(h.workers.tasks)),
		TasksProcessed:  atomic.LoadInt64(&h.metrics.TasksProcessed),
		TasksRejected:   atomic.LoadInt64(&h.metrics.TasksRejected),
		TasksFailed:     atomic.LoadInt64(&h.metrics.TasksFailed),
		TasksExpired:    atomic.LoadInt64(&h.metrics.TasksExpired),
		RegisteredHooks: registeredHooks,
		// Overflow metrics set to 0 initially, Phase 2 implementation
		OverflowDepth:    0,
		OverflowCapacity: 0,
		OverflowDrained:  0,
	}
}

// Close shuts down the service gracefully.
func (h *Hooks[T]) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return ErrAlreadyClosed
	}
	h.closed = true
	h.mu.Unlock()

	// Shutdown worker pool - this waits for queued hooks to complete
	h.workers.close()

	// Count remaining tasks as expired for metrics consistency
	remaining := atomic.LoadInt64(&h.metrics.QueueDepth)
	if remaining > 0 {
		atomic.AddInt64(&h.metrics.TasksExpired, remaining)
		atomic.StoreInt64(&h.metrics.QueueDepth, 0)
	}

	return nil
}

// generateID creates a cryptographically random unique identifier for hooks.
//
// Uses crypto/rand for security - hook IDs cannot be predicted or forged.
// This prevents malicious unhook attempts on other services' hooks.
func (h *Hooks[T]) generateID() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to timestamp-based ID if random fails
		// This should never happen in practice with crypto/rand
		return fmt.Sprintf("%d", h.clock.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}

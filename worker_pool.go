package hookz

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zoobzio/clockz"
)

// workerPool manages async execution of hook callbacks.
//
// The worker pool:
//   - Executes hooks asynchronously to prevent blocking event emission
//   - Provides panic recovery to prevent hook failures from crashing the service
//   - Applies global timeout to prevent runaway hook execution
//   - Supports graceful shutdown with proper resource cleanup
//   - Optional backpressure for smoothing traffic spikes
//   - Optional overflow queuing for absorbing sustained load
type workerPool[T any] struct {
	// Time abstraction for deterministic testing
	clock clockz.Clock

	// Channel for receiving hook execution tasks
	tasks chan hookTask[T]

	// Optional resilience features
	backpressure *BackpressureConfig
	overflow     *overflowQueue[T]

	// WaitGroup to track worker goroutines for graceful shutdown
	wg sync.WaitGroup

	mu sync.RWMutex

	// Global timeout applied to all hook executions
	// Zero value means no timeout
	globalTimeout time.Duration

	// Tracks if the pool has been closed
	closed bool

	// Metrics pointer for atomic updates
	metrics *Metrics
}

// hookTask represents a single hook execution task.
type hookTask[T any] struct {
	ctx   context.Context // Context for cancellation and timeout
	data  T               // Event data to pass to hook
	hook  hookEntry[T]    // Hook callback and configuration
	event string          // Event name (for logging)
}

// hookEntry contains the callback function and its configuration.
type hookEntry[T any] struct {
	id       string                         // Unique identifier for this hook
	callback func(context.Context, T) error // The actual callback function
}

// newWorkerPool creates and starts a worker pool with the specified configuration.
//
// The pool immediately starts numWorkers goroutines that will process
// hook tasks from the queue. The queueSize parameter sets the buffering
// capacity for handling bursty workloads.

// newWorkerPoolWithResilience creates a worker pool with optional resilience features.
// Replaces newWorkerPool when backpressure or overflow features are enabled.
func newWorkerPoolWithResilience[T any](cfg config, metrics *Metrics) *workerPool[T] {
	pool := &workerPool[T]{
		clock:         cfg.clock,
		tasks:         make(chan hookTask[T], cfg.queueSize),
		globalTimeout: cfg.timeout,
		metrics:       metrics,
	}

	// Configure backpressure
	if cfg.backpressure != nil {
		pool.backpressure = cfg.backpressure
	}

	// Configure overflow
	if cfg.overflow != nil {
		pool.overflow = newOverflowQueue[T](*cfg.overflow, pool.tasks, pool.clock)
	}

	// Start worker goroutines
	for i := 0; i < cfg.workers; i++ {
		pool.wg.Add(1)
		go pool.worker()
	}

	return pool
}

// submit queues a hook for execution.
//
// Returns ErrQueueFull if the worker pool cannot accept more tasks.
// This prevents unbounded queuing that could lead to memory exhaustion.
//
// With resilience features enabled:
//   - Backpressure: May briefly delay instead of immediate rejection
//   - Overflow: Buffers tasks beyond primary queue capacity
//   - Combined: Backpressure first, then overflow as fallback
func (p *workerPool[T]) submit(task hookTask[T]) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.closed {
		return ErrServiceClosed
	}

	// Route based on enabled features
	if p.backpressure != nil && p.overflow != nil {
		return p.submitWithBackpressureThenOverflow(task)
	} else if p.backpressure != nil {
		return p.submitWithBackpressure(task)
	} else if p.overflow != nil {
		return p.submitWithOverflow(task)
	}

	// Default unchanged behavior
	return p.submitDefault(task)
}

// submitDefault maintains original submission behavior for backward compatibility.
func (p *workerPool[T]) submitDefault(task hookTask[T]) error {
	// Channel send must be protected by mutex to prevent race with close()
	// Without this protection, close() could close the channel between the
	// closed check above and the send operation below, causing panic
	select {
	case p.tasks <- task:
		// Task successfully queued - update depth atomically
		atomic.AddInt64(&p.metrics.QueueDepth, 1)
		return nil
	default:
		// Queue is full - increment rejection counter
		atomic.AddInt64(&p.metrics.TasksRejected, 1)
		return ErrQueueFull
	}
}

// submitWithBackpressure applies backpressure delay when queue utilization exceeds threshold.
// Fix applied: Check queue full before calculating utilization to avoid race condition.
func (p *workerPool[T]) submitWithBackpressure(task hookTask[T]) error {
	// Try immediate submission first
	select {
	case p.tasks <- task:
		// Task successfully queued - update depth atomically
		atomic.AddInt64(&p.metrics.QueueDepth, 1)
		return nil
	default:
		// Queue definitely full at this point - calculate backpressure
	}

	// Calculate current queue utilization - queue is known to be full
	utilization := float32(1.0) // 100% utilization when queue is full

	// Apply backpressure if threshold reached
	if utilization >= p.backpressure.StartThreshold {
		delay := p.calculateBackpressureDelay(utilization)
		if delay > p.backpressure.MaxWait {
			delay = p.backpressure.MaxWait
		}

		// Apply backpressure with timeout and context cancellation
		select {
		case p.tasks <- task:
			// Task successfully queued after backpressure - update depth atomically
			atomic.AddInt64(&p.metrics.QueueDepth, 1)
			return nil
		case <-p.clock.After(delay):
			// Final attempt after backpressure wait
			select {
			case p.tasks <- task:
				// Task successfully queued after wait - update depth atomically
				atomic.AddInt64(&p.metrics.QueueDepth, 1)
				return nil
			default:
				// Queue still full - increment rejection counter
				atomic.AddInt64(&p.metrics.TasksRejected, 1)
				return ErrQueueFull
			}
		case <-task.ctx.Done():
			// Context canceled during backpressure - count as expired
			atomic.AddInt64(&p.metrics.TasksExpired, 1)
			return task.ctx.Err()
		}
	}

	// Below threshold - try one more immediate attempt
	select {
	case p.tasks <- task:
		// Task successfully queued - update depth atomically
		atomic.AddInt64(&p.metrics.QueueDepth, 1)
		return nil
	default:
		// Queue still full - increment rejection counter
		atomic.AddInt64(&p.metrics.TasksRejected, 1)
		return ErrQueueFull
	}
}

// calculateBackpressureDelay computes delay based on utilization and strategy.
// Fix applied: Added bounds checking for negative delays.
func (p *workerPool[T]) calculateBackpressureDelay(utilization float32) time.Duration {
	switch p.backpressure.Strategy {
	case "fixed":
		return p.backpressure.MaxWait
	case "linear":
		// Linear increase from 0 to MaxWait based on utilization above threshold
		factor := (utilization - p.backpressure.StartThreshold) / (1.0 - p.backpressure.StartThreshold)
		if factor < 0 {
			factor = 0 // Defensive programming against negative delays
		}
		return time.Duration(float32(p.backpressure.MaxWait) * factor)
	case "exponential":
		// Exponential backoff
		factor := (utilization - p.backpressure.StartThreshold) / (1.0 - p.backpressure.StartThreshold)
		if factor < 0 {
			factor = 0 // Defensive programming
		}
		exponential := factor * factor
		return time.Duration(float32(p.backpressure.MaxWait) * exponential)
	default:
		return p.backpressure.MaxWait
	}
}

// submitWithOverflow tries primary queue, then overflow queue.
func (p *workerPool[T]) submitWithOverflow(task hookTask[T]) error {
	// Try primary queue first
	select {
	case p.tasks <- task:
		// Task successfully queued - update depth atomically
		atomic.AddInt64(&p.metrics.QueueDepth, 1)
		return nil
	default:
		// Primary full, try overflow (metrics tracked in overflow enqueue)
		return p.overflow.enqueue(task)
	}
}

// submitWithBackpressureThenOverflow combines both resilience strategies.
// Priority: Backpressure first (for microsecond spikes), then overflow (for sustained load).
func (p *workerPool[T]) submitWithBackpressureThenOverflow(task hookTask[T]) error {
	// Phase 1: Try primary queue
	select {
	case p.tasks <- task:
		// Task successfully queued - update depth atomically
		atomic.AddInt64(&p.metrics.QueueDepth, 1)
		return nil
	default:
		// Primary full, continue to Phase 2
	}

	// Phase 2: Apply backpressure if threshold reached
	utilization := float32(1.0) // Queue is definitely full at this point

	if utilization >= p.backpressure.StartThreshold {
		delay := p.calculateBackpressureDelay(utilization)
		if delay > p.backpressure.MaxWait {
			delay = p.backpressure.MaxWait
		}

		select {
		case p.tasks <- task:
			// Task successfully queued after backpressure - update depth atomically
			atomic.AddInt64(&p.metrics.QueueDepth, 1)
			return nil
		case <-p.clock.After(delay):
			// Still full after backpressure, continue to Phase 3
		case <-task.ctx.Done():
			// Context canceled during backpressure - count as expired
			atomic.AddInt64(&p.metrics.TasksExpired, 1)
			return task.ctx.Err()
		}
	}

	// Phase 3: Use overflow queue
	return p.overflow.enqueue(task)
}

// close shuts down the worker pool gracefully.
//
// This method:
//  1. Marks the pool as closed to prevent new submissions
//  2. Drains overflow queue if enabled
//  3. Closes the task channel
//  4. Waits for all worker goroutines to finish processing queued tasks
//  5. Returns when all resources have been cleaned up
func (p *workerPool[T]) close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()

	// Stop overflow drain loop and drain remaining items
	if p.overflow != nil {
		close(p.overflow.drainStop)
		// Wait for drainLoop to complete draining before closing primary channel
		p.overflow.drainWG.Wait()
	}

	close(p.tasks)
	p.wg.Wait()
}

// worker is the main loop for worker goroutines.
//
// Each worker continuously processes tasks from the queue until
// the task channel is closed during shutdown.
func (p *workerPool[T]) worker() {
	defer p.wg.Done()

	for task := range p.tasks {
		// Decrement queue depth as soon as task is retrieved
		atomic.AddInt64(&p.metrics.QueueDepth, -1)

		// Execute hook with panic recovery and metrics tracking
		// Note: We don't pre-check context cancellation here because the hook
		// itself is responsible for respecting context cancellation
		if err := p.executeHookSafely(task); err != nil {
			// Check if error was due to context cancellation
			if task.ctx.Err() != nil {
				atomic.AddInt64(&p.metrics.TasksExpired, 1)
			} else {
				atomic.AddInt64(&p.metrics.TasksFailed, 1)
			}
		} else {
			atomic.AddInt64(&p.metrics.TasksProcessed, 1)
		}
	}
}

// executeHookSafely runs a hook with panic recovery.
//
// This method ensures that panicking hooks cannot crash the entire
// service. Panics are recovered and handled gracefully.
// Returns error if hook failed or panicked.
func (p *workerPool[T]) executeHookSafely(task hookTask[T]) (err error) {
	defer func() {
		if r := recover(); r != nil {
			// Hook panicked - treat as error
			// In production, this should be reported to error tracking
			_ = r // Placeholder for panic value handling
			err = ErrHookPanicked
		}
	}()

	err = p.executeHook(task)
	return err
}

// executeHook runs the actual hook callback.
//
// This method:
//   - Applies global timeout if configured
//   - Executes the callback function
//   - Handles context cancellation gracefully
//   - Returns error if hook execution failed
func (p *workerPool[T]) executeHook(task hookTask[T]) error {
	ctx := task.ctx

	// Apply global timeout if configured
	if p.globalTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = p.clock.WithTimeout(ctx, p.globalTimeout)
		defer cancel()
	}

	// Execute the hook callback and return any error
	return task.hook.callback(ctx, task.data)
}

// overflowQueue provides secondary buffering beyond the primary task queue.
// Tasks are periodically drained back to the primary queue as capacity becomes available.
type overflowQueue[T any] struct {
	clock     clockz.Clock // Time abstraction for deterministic testing
	items     []hookTask[T]
	drainStop chan struct{}
	strategy  string
	mu        sync.Mutex
	drainWG   sync.WaitGroup // Synchronizes drain loop completion
	capacity  int
}

// newOverflowQueue creates and starts an overflow queue with background draining.
// Fix applied: Added panic recovery to drain loop to prevent crash.
func newOverflowQueue[T any](config OverflowConfig, primary chan<- hookTask[T], clock clockz.Clock) *overflowQueue[T] {
	oq := &overflowQueue[T]{
		clock:     clock,
		items:     make([]hookTask[T], 0, config.Capacity),
		capacity:  config.Capacity,
		drainStop: make(chan struct{}),
		strategy:  config.EvictionStrategy,
	}

	// Start background drain goroutine with synchronization
	oq.drainWG.Add(1)
	go oq.drainLoop(primary, config.DrainInterval)
	return oq
}

// enqueue adds a task to the overflow queue, applying eviction strategy if full.
func (oq *overflowQueue[T]) enqueue(task hookTask[T]) error {
	oq.mu.Lock()
	defer oq.mu.Unlock()

	// Check capacity
	if len(oq.items) >= oq.capacity {
		// Special case: zero capacity always rejects regardless of strategy
		if oq.capacity == 0 {
			return ErrQueueFull
		}

		switch oq.strategy {
		case "fifo":
			// Drop oldest, add newest
			oq.items = oq.items[1:]
			oq.items = append(oq.items, task)
			return nil
		case "lifo":
			// Replace newest with new task
			oq.items[len(oq.items)-1] = task
			return nil
		case "reject":
			return ErrQueueFull
		default:
			return ErrQueueFull
		}
	}

	oq.items = append(oq.items, task)
	return nil
}

// drainLoop periodically attempts to move tasks from overflow back to primary queue.
// Fix applied: Added panic recovery, complete shutdown draining, and proper synchronization.
func (oq *overflowQueue[T]) drainLoop(primary chan<- hookTask[T], interval time.Duration) {
	defer func() {
		// Signal that drain loop has completed
		oq.drainWG.Done()

		if r := recover(); r != nil {
			// Panic in drain loop - log error and exit gracefully
			// In production, this should restart the drain loop or mark service unhealthy
			_ = r // Placeholder for panic value handling
		}
	}()

	ticker := oq.clock.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C():
			oq.drainToPrimary(primary)
		case <-oq.drainStop:
			// Drain remaining items during shutdown
			oq.drainAllToPrimary(primary)
			return
		}
	}
}

// drainToPrimary attempts to move overflow tasks back to primary queue.
// Fix applied: Added context expiration check to avoid executing stale tasks.
func (oq *overflowQueue[T]) drainToPrimary(primary chan<- hookTask[T]) {
	oq.mu.Lock()
	defer oq.mu.Unlock()

	remaining := make([]hookTask[T], 0, len(oq.items))

	for _, task := range oq.items {
		// Check if task context has expired
		if task.ctx.Err() != nil {
			continue // Skip expired tasks
		}

		select {
		case primary <- task:
			// Successfully drained to primary
		default:
			// Primary still full, keep in overflow
			remaining = append(remaining, task)
		}
	}

	oq.items = remaining
}

// drainAllToPrimary aggressively drains all remaining tasks during shutdown.
// Fix applied: Non-blocking send operation to prevent race condition with channel close.
func (oq *overflowQueue[T]) drainAllToPrimary(primary chan<- hookTask[T]) {
	oq.mu.Lock()
	defer oq.mu.Unlock()

	for _, task := range oq.items {
		// Check if task context has expired
		if task.ctx.Err() != nil {
			continue // Skip expired tasks
		}

		// Non-blocking send - prevents race condition with channel close
		select {
		case primary <- task:
			// Successfully drained to primary
		default:
			// Primary channel is full or closed
			// During shutdown this is acceptable - task is discarded
		}
	}

	// Clear overflow queue
	oq.items = oq.items[:0]
}

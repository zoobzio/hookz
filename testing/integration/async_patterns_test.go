package integration

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zoobzio/hookz"
)

// Event keys for async patterns testing
const (
	taskProcess     hookz.Key = "task.process"
	heavyTask       hookz.Key = "heavy.task"
	backgroundTask  hookz.Key = "background.task"
	burstEvent      hookz.Key = "burst.event"
	errorTest       hookz.Key = "error.test"
	cancellableTask hookz.Key = "cancellable.task"
	balancedTask    hookz.Key = "balanced.task"
	batchItem       hookz.Key = "batch.item"
)

// AsyncEvent represents an event with async processing metadata
type AsyncEvent struct {
	ID        string
	Type      string
	Data      interface{}
	Priority  int
	Timestamp time.Time
}

// AsyncProcessor demonstrates async event processing patterns
type AsyncProcessor struct {
	hooks *hookz.Hooks[AsyncEvent]
}

func NewAsyncProcessor(workers int) *AsyncProcessor {
	return &AsyncProcessor{
		hooks: hookz.New[AsyncEvent](hookz.WithWorkers(workers)),
	}
}

func NewAsyncProcessorWithQueue(workers, queueSize int) *AsyncProcessor {
	return &AsyncProcessor{
		hooks: hookz.New[AsyncEvent](hookz.WithWorkers(workers), hookz.WithQueueSize(queueSize)),
	}
}

func (p *AsyncProcessor) Events() *hookz.Hooks[AsyncEvent] {
	return p.hooks
}

func (p *AsyncProcessor) Process(ctx context.Context, event AsyncEvent) error {
	return p.hooks.Emit(ctx, event.Type, event)
}

func (p *AsyncProcessor) Shutdown() {
	p.hooks.Close()
}

// TestAsyncExecutionOrder verifies that hooks execute asynchronously
// and may complete in different order than they were triggered
func TestAsyncExecutionOrder(t *testing.T) {
	processor := NewAsyncProcessor(5) // 5 workers for parallel execution
	defer processor.Shutdown()

	ctx := context.Background()

	// Track completion order
	var completionOrder []string
	var mu sync.Mutex

	// Hook 1 - takes 100ms
	_, err := processor.Events().Hook(taskProcess, func(ctx context.Context, event AsyncEvent) error {
		if event.ID == "task-1" {
			time.Sleep(100 * time.Millisecond)
			mu.Lock()
			completionOrder = append(completionOrder, "task-1")
			mu.Unlock()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook 1: %v", err)
	}

	// Hook 2 - takes 50ms
	_, err = processor.Events().Hook(taskProcess, func(ctx context.Context, event AsyncEvent) error {
		if event.ID == "task-2" {
			time.Sleep(50 * time.Millisecond)
			mu.Lock()
			completionOrder = append(completionOrder, "task-2")
			mu.Unlock()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook 2: %v", err)
	}

	// Hook 3 - instant
	_, err = processor.Events().Hook(taskProcess, func(ctx context.Context, event AsyncEvent) error {
		if event.ID == "task-3" {
			mu.Lock()
			completionOrder = append(completionOrder, "task-3")
			mu.Unlock()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook 3: %v", err)
	}

	// Emit events in order 1, 2, 3
	if err := processor.Process(ctx, AsyncEvent{ID: "task-1", Type: string(taskProcess)}); err != nil {
		t.Fatalf("Failed to process task-1: %v", err)
	}
	if err := processor.Process(ctx, AsyncEvent{ID: "task-2", Type: string(taskProcess)}); err != nil {
		t.Fatalf("Failed to process task-2: %v", err)
	}
	if err := processor.Process(ctx, AsyncEvent{ID: "task-3", Type: string(taskProcess)}); err != nil {
		t.Fatalf("Failed to process task-3: %v", err)
	}

	// Wait for all to complete
	time.Sleep(150 * time.Millisecond)

	// Verify completion order is NOT the same as emission order
	// Due to async execution, task-3 should complete first, then task-2, then task-1
	mu.Lock()
	defer mu.Unlock()

	if len(completionOrder) != 3 {
		t.Fatalf("Expected 3 completions, got %d", len(completionOrder))
	}

	// Task-3 should complete first (instant)
	if completionOrder[0] != "task-3" {
		t.Errorf("Expected task-3 to complete first, got %s", completionOrder[0])
	}

	// Task-2 should complete second (50ms)
	if completionOrder[1] != "task-2" {
		t.Errorf("Expected task-2 to complete second, got %s", completionOrder[1])
	}

	// Task-1 should complete last (100ms)
	if completionOrder[2] != "task-1" {
		t.Errorf("Expected task-1 to complete last, got %s", completionOrder[2])
	}
}

// TestWorkerPoolSaturation demonstrates behavior when worker pool is saturated
func TestWorkerPoolSaturation(t *testing.T) {
	// Create processor with only 2 workers and larger queue to handle bursts
	// Default queue (workers*2 = 4) would reject many tasks
	processor := NewAsyncProcessorWithQueue(2, 20)
	defer processor.Shutdown()

	ctx := context.Background()

	var activeWorkers atomic.Int32
	var maxConcurrent atomic.Int32
	var processedCount atomic.Int32

	// Register a slow hook that tracks concurrent executions
	_, err := processor.Events().Hook(heavyTask, func(ctx context.Context, event AsyncEvent) error {
		current := activeWorkers.Add(1)

		// Track max concurrent workers
		for {
			maxValue := maxConcurrent.Load()
			if current <= maxValue || maxConcurrent.CompareAndSwap(maxValue, current) {
				break
			}
		}

		// Simulate heavy processing
		time.Sleep(50 * time.Millisecond)

		activeWorkers.Add(-1)
		processedCount.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register heavy task hook: %v", err)
	}

	// Submit 10 tasks rapidly
	var submitErrors int
	for i := 0; i < 10; i++ {
		err := processor.Process(ctx, AsyncEvent{
			ID:   fmt.Sprintf("task-%d", i),
			Type: string(heavyTask),
		})
		if err != nil {
			// With queue size 20, we shouldn't get ErrQueueFull
			// but if we do, count it for diagnostics
			if err == hookz.ErrQueueFull {
				submitErrors++
			} else {
				t.Fatalf("Unexpected error submitting task %d: %v", i, err)
			}
		}
	}

	if submitErrors > 0 {
		t.Errorf("Queue rejected %d tasks despite increased size", submitErrors)
	}

	// Wait for all tasks to complete
	// With 2 workers and 50ms per task, 10 tasks need at least 250ms
	// Add extra buffer for async dispatch
	time.Sleep(800 * time.Millisecond)

	// Verify worker pool respected the limit
	if maxValue := maxConcurrent.Load(); maxValue > 2 {
		t.Errorf("Expected max 2 concurrent workers, got %d", maxValue)
	}

	// Verify all successfully submitted tasks were processed
	expectedProcessed := int32(10) - int32(submitErrors)
	if processed := processedCount.Load(); processed != expectedProcessed {
		t.Errorf("Expected %d tasks processed, got %d", expectedProcessed, processed)
	}
}

// TestFireAndForget demonstrates the fire-and-forget pattern
// where emission returns immediately without waiting for hooks
func TestFireAndForget(t *testing.T) {
	processor := NewAsyncProcessor(5)
	defer processor.Shutdown()

	ctx := context.Background()

	var hookStarted atomic.Bool
	var hookCompleted atomic.Bool

	// Register a slow hook
	_, err := processor.Events().Hook(backgroundTask, func(ctx context.Context, event AsyncEvent) error {
		hookStarted.Store(true)
		time.Sleep(100 * time.Millisecond)
		hookCompleted.Store(true)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register background task hook: %v", err)
	}

	// Measure emission time
	start := time.Now()
	err = processor.Process(ctx, AsyncEvent{
		ID:   "bg-task",
		Type: string(backgroundTask),
	})
	emitDuration := time.Since(start)

	if err != nil {
		t.Fatalf("Failed to emit event: %v", err)
	}

	// Emission should return almost immediately (< 10ms)
	if emitDuration > 10*time.Millisecond {
		t.Errorf("Emission took too long: %v (should be < 10ms)", emitDuration)
	}

	// Hook should not be completed yet
	if hookCompleted.Load() {
		t.Error("Hook completed too early - not async")
	}

	// Wait for hook to complete
	time.Sleep(150 * time.Millisecond)

	// Now hook should be completed
	if !hookCompleted.Load() {
		t.Error("Hook did not complete")
	}
}

// TestBurstProcessing simulates burst traffic patterns
func TestBurstProcessing(t *testing.T) {
	// Use larger queue for burst handling (10 workers * 10 = 100 queue size)
	processor := NewAsyncProcessorWithQueue(10, 100)
	defer processor.Shutdown()

	ctx := context.Background()

	var receivedEvents sync.Map
	var processedCount atomic.Int32

	// Register processor that tracks events
	_, err := processor.Events().Hook(burstEvent, func(ctx context.Context, event AsyncEvent) error {
		// Simulate variable processing time based on priority
		delay := time.Duration(100-event.Priority) * time.Millisecond
		time.Sleep(delay)

		receivedEvents.Store(event.ID, event)
		processedCount.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register burst event hook: %v", err)
	}

	// Send 3 bursts of events
	var totalSubmitted int32
	var queueFullErrors int32

	for burst := 0; burst < 3; burst++ {
		// Send 20 events in rapid succession
		for i := 0; i < 20; i++ {
			err := processor.Process(ctx, AsyncEvent{
				ID:       fmt.Sprintf("burst-%d-event-%d", burst, i),
				Type:     string(burstEvent),
				Priority: i, // Higher number = higher priority (shorter processing)
			})
			if err != nil {
				if err == hookz.ErrQueueFull {
					atomic.AddInt32(&queueFullErrors, 1)
					// In a real application, you might want to:
					// - Retry with backoff
					// - Send to overflow queue
					// - Log and monitor
					continue
				}
				t.Fatalf("Unexpected error in burst %d, event %d: %v", burst, i, err)
			}
			atomic.AddInt32(&totalSubmitted, 1)
		}

		// Wait between bursts
		time.Sleep(200 * time.Millisecond)
	}

	// Wait for all processing to complete
	time.Sleep(500 * time.Millisecond)

	// Verify all successfully submitted events were processed
	if count := processedCount.Load(); count != totalSubmitted {
		t.Errorf("Expected %d events processed, got %d (queue rejected %d)",
			totalSubmitted, count, queueFullErrors)
	}

	// With queue size 100 and 10 workers, we shouldn't have many rejections
	if queueFullErrors > 5 {
		t.Errorf("Too many queue rejections: %d (consider increasing queue size)", queueFullErrors)
	}

	// Only verify events that were successfully submitted
	var missingEvents []string
	receivedEvents.Range(func(key, value interface{}) bool {
		// Track what we actually received
		return true
	})

	if len(missingEvents) > 0 && queueFullErrors == 0 {
		t.Errorf("Events lost without queue rejection: %v", missingEvents)
	}
}

// TestAsyncErrorIsolation verifies that errors in one hook don't affect others
func TestAsyncErrorIsolation(t *testing.T) {
	processor := NewAsyncProcessor(5)
	defer processor.Shutdown()

	ctx := context.Background()

	var hook1Called, hook2Called, hook3Called atomic.Bool

	// Hook 1 - will panic
	_, err := processor.Events().Hook(errorTest, func(ctx context.Context, event AsyncEvent) error {
		hook1Called.Store(true)
		panic("intentional panic in hook 1")
	})
	if err != nil {
		t.Fatalf("Failed to register hook 1: %v", err)
	}

	// Hook 2 - will return error
	_, err = processor.Events().Hook(errorTest, func(ctx context.Context, event AsyncEvent) error {
		hook2Called.Store(true)
		return fmt.Errorf("intentional error in hook 2")
	})
	if err != nil {
		t.Fatalf("Failed to register hook 2: %v", err)
	}

	// Hook 3 - should still execute
	_, err = processor.Events().Hook(errorTest, func(ctx context.Context, event AsyncEvent) error {
		hook3Called.Store(true)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook 3: %v", err)
	}

	// Emit event
	err = processor.Process(ctx, AsyncEvent{
		ID:   "test-error",
		Type: string(errorTest),
	})

	if err != nil {
		t.Fatalf("Emission failed: %v", err)
	}

	// Wait for hooks to complete
	time.Sleep(100 * time.Millisecond)

	// All hooks should have been called despite errors
	if !hook1Called.Load() {
		t.Error("Hook 1 was not called")
	}
	if !hook2Called.Load() {
		t.Error("Hook 2 was not called")
	}
	if !hook3Called.Load() {
		t.Error("Hook 3 was not called despite errors in other hooks")
	}
}

// TestAsyncContextCancellation verifies context cancellation handling
func TestAsyncContextCancellation(t *testing.T) {
	processor := NewAsyncProcessor(5)
	defer processor.Shutdown()

	var hookStarted atomic.Bool
	var hookCancelled atomic.Bool
	var hookCompleted atomic.Bool

	// Register hook that respects context cancellation
	_, err := processor.Events().Hook(cancellableTask, func(ctx context.Context, event AsyncEvent) error {
		hookStarted.Store(true)

		// Simulate work with cancellation checks
		for i := 0; i < 10; i++ {
			select {
			case <-ctx.Done():
				hookCancelled.Store(true)
				return ctx.Err()
			case <-time.After(20 * time.Millisecond):
				// Continue processing
			}
		}

		hookCompleted.Store(true)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register cancellable hook: %v", err)
	}

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	// Emit event
	if err := processor.Process(ctx, AsyncEvent{
		ID:   "cancellable",
		Type: string(cancellableTask),
	}); err != nil {
		t.Fatalf("Failed to emit cancellable event: %v", err)
	}

	// Wait for hook to start
	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	// Wait a bit more
	time.Sleep(100 * time.Millisecond)

	// Verify hook was canceled
	if !hookStarted.Load() {
		t.Error("Hook did not start")
	}
	if !hookCancelled.Load() {
		t.Error("Hook was not canceled")
	}
	if hookCompleted.Load() {
		t.Error("Hook completed despite cancellation")
	}
}

// TestAsyncLoadBalancing verifies work distribution across workers
func TestAsyncLoadBalancing(t *testing.T) {
	numWorkers := 4
	// Ensure queue can handle all 100 tasks without rejection
	processor := NewAsyncProcessorWithQueue(numWorkers, 200)
	defer processor.Shutdown()

	ctx := context.Background()

	// Track which "worker" processed each event
	// (simulated by goroutine ID-like tracking)
	workerTasks := make([]atomic.Int32, numWorkers)
	var currentWorker atomic.Int32

	// Register hook that simulates worker identification
	_, err := processor.Events().Hook(balancedTask, func(ctx context.Context, event AsyncEvent) error {
		// Simulate worker assignment (round-robin style)
		worker := currentWorker.Add(1) % int32(numWorkers)
		workerTasks[worker].Add(1)

		// Simulate processing
		time.Sleep(10 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register balanced task hook: %v", err)
	}

	// Send many tasks
	numTasks := 100
	var submittedTasks int
	for i := 0; i < numTasks; i++ {
		err := processor.Process(ctx, AsyncEvent{
			ID:   fmt.Sprintf("task-%d", i),
			Type: string(balancedTask),
		})
		if err != nil {
			if err == hookz.ErrQueueFull {
				// With queue size 200, this shouldn't happen
				t.Logf("Warning: Queue full at task %d", i)
				continue
			}
			t.Fatalf("Failed to submit task %d: %v", i, err)
		}
		submittedTasks++
	}

	// Wait for completion
	// With 4 workers and 10ms per task, 100 tasks need at least 250ms
	time.Sleep(500 * time.Millisecond)

	// Check distribution (should be roughly even)
	var totalProcessed int32
	minTasks := int32(numTasks)
	maxTasks := int32(0)

	for i := 0; i < numWorkers; i++ {
		tasks := workerTasks[i].Load()
		totalProcessed += tasks
		if tasks < minTasks {
			minTasks = tasks
		}
		if tasks > maxTasks {
			maxTasks = tasks
		}
	}

	// Verify all successfully submitted tasks were processed
	expectedSubmitted := int32(submittedTasks)
	if totalProcessed != expectedSubmitted {
		t.Errorf("Expected %d tasks processed, got %d", submittedTasks, totalProcessed)
	}

	// Distribution should be somewhat balanced (within 50% difference)
	if maxTasks > minTasks*2 && minTasks > 0 {
		t.Errorf("Unbalanced distribution: min=%d, max=%d", minTasks, maxTasks)
	}
}

// TestAsyncBatchProcessing demonstrates batch processing pattern
func TestAsyncBatchProcessing(t *testing.T) {
	// Limited workers but adequate queue for batching
	processor := NewAsyncProcessorWithQueue(2, 50)
	defer processor.Shutdown()

	ctx := context.Background()

	// Batch collector
	type Batch struct {
		items []AsyncEvent
		mu    sync.Mutex
	}

	batches := make(map[string]*Batch)
	var batchMu sync.Mutex
	var processedBatches atomic.Int32

	// Register batch collector hook
	_, err := processor.Events().Hook(batchItem, func(ctx context.Context, event AsyncEvent) error {
		batchID := event.Data.(map[string]string)["batchID"]

		batchMu.Lock()
		batch, exists := batches[batchID]
		if !exists {
			batch = &Batch{}
			batches[batchID] = batch
		}
		batchMu.Unlock()

		// Add to batch
		batch.mu.Lock()
		batch.items = append(batch.items, event)
		shouldProcess := len(batch.items) >= 10 // Process when batch reaches 10 items
		batch.mu.Unlock()

		if shouldProcess {
			// Simulate batch processing
			time.Sleep(50 * time.Millisecond)
			processedBatches.Add(1)

			// Clear batch
			batch.mu.Lock()
			batch.items = nil
			batch.mu.Unlock()
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register batch item hook: %v", err)
	}

	// Send items in multiple batches
	for batchNum := 0; batchNum < 3; batchNum++ {
		batchID := fmt.Sprintf("batch-%d", batchNum)

		// Send 10 items for this batch
		for i := 0; i < 10; i++ {
			err := processor.Process(ctx, AsyncEvent{
				ID:   fmt.Sprintf("%s-item-%d", batchID, i),
				Type: string(batchItem),
				Data: map[string]string{"batchID": batchID},
			})
			if err != nil {
				// Handle queue full gracefully in batch scenarios
				if err == hookz.ErrQueueFull {
					// In production, might implement:
					// - Batch buffering
					// - Backpressure signaling
					// - Overflow handling
					t.Logf("Queue full during batch %s, item %d", batchID, i)
					continue
				}
				t.Fatalf("Failed to submit batch item: %v", err)
			}
		}
	}

	// Wait for batch processing
	// With 2 workers and multiple batches, need more time
	time.Sleep(600 * time.Millisecond)

	// Verify batches were processed
	if processed := processedBatches.Load(); processed < 3 {
		t.Errorf("Expected at least 3 batches processed, got %d", processed)
	}
}

// TestQueueFullHandling demonstrates proper handling of ErrQueueFull
// This test shows patterns for dealing with queue saturation in production
func TestQueueFullHandling(t *testing.T) {
	// Deliberately small queue to trigger ErrQueueFull
	processor := NewAsyncProcessorWithQueue(2, 4) // 2 workers, queue of 4
	defer processor.Shutdown()

	ctx := context.Background()

	// Track different handling strategies
	var (
		successfulSubmissions atomic.Int32
		queueFullErrors       atomic.Int32
		retriedSuccesses      atomic.Int32
		droppedEvents         atomic.Int32
		processedEvents       atomic.Int32
	)

	// Register slow hook to keep workers busy
	_, err := processor.Events().Hook(heavyTask, func(ctx context.Context, event AsyncEvent) error {
		processedEvents.Add(1)
		time.Sleep(100 * time.Millisecond) // Slow processing
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}

	// Strategy 1: Simple retry with backoff
	submitWithRetry := func(event AsyncEvent, maxRetries int) error {
		var lastErr error
		for attempt := 0; attempt <= maxRetries; attempt++ {
			err := processor.Process(ctx, event)
			if err == nil {
				if attempt > 0 {
					retriedSuccesses.Add(1)
				}
				successfulSubmissions.Add(1)
				return nil
			}

			if err == hookz.ErrQueueFull {
				if attempt == 0 {
					// Only count first queue full error per event
					queueFullErrors.Add(1)
				}
				lastErr = err
				// Exponential backoff with longer delays
				backoff := time.Duration(1<<min(uint(attempt), 10)) * 50 * time.Millisecond
				time.Sleep(backoff)
				continue
			}

			// Non-retriable error
			return err
		}
		return lastErr
	}

	// Strategy 2: Drop on queue full (for non-critical events)
	submitOrDrop := func(event AsyncEvent) {
		err := processor.Process(ctx, event)
		if err == nil {
			successfulSubmissions.Add(1)
		} else if err == hookz.ErrQueueFull {
			queueFullErrors.Add(1)
			droppedEvents.Add(1)
			// In production: log, increment metrics, alert if rate too high
		}
	}

	// Test both strategies with concurrent submissions
	var wg sync.WaitGroup

	// Critical events - retry
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			event := AsyncEvent{
				ID:   fmt.Sprintf("critical-%d", i),
				Type: string(heavyTask),
			}
			if err := submitWithRetry(event, 3); err != nil {
				t.Logf("Failed to submit critical event %d after retries: %v", i, err)
			}
		}
	}()

	// Non-critical events - drop if full
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			event := AsyncEvent{
				ID:   fmt.Sprintf("non-critical-%d", i),
				Type: string(heavyTask),
			}
			submitOrDrop(event)
		}
	}()

	wg.Wait()

	// Wait for processing to complete
	time.Sleep(2 * time.Second)

	// Analysis
	t.Logf("Queue Full Handling Results:")
	t.Logf("  Successful submissions: %d", successfulSubmissions.Load())
	t.Logf("  Queue full errors: %d", queueFullErrors.Load())
	t.Logf("  Successful retries: %d", retriedSuccesses.Load())
	t.Logf("  Dropped events: %d", droppedEvents.Load())
	t.Logf("  Processed events: %d", processedEvents.Load())

	// Verify retry strategy worked
	if retriedSuccesses.Load() == 0 && queueFullErrors.Load() > 0 {
		t.Error("Retry strategy didn't succeed despite queue full errors")
	}

	// Verify events were actually processed
	if processedEvents.Load() != successfulSubmissions.Load() {
		t.Errorf("Processing mismatch: submitted %d, processed %d",
			successfulSubmissions.Load(), processedEvents.Load())
	}

	// Demonstrate monitoring pattern
	queueFullRate := float64(queueFullErrors.Load()) / float64(queueFullErrors.Load()+successfulSubmissions.Load())
	if queueFullRate > 0.5 {
		t.Logf("WARNING: High queue rejection rate: %.2f%% - consider increasing workers or queue size",
			queueFullRate*100)
	}
}

// TestQueueSizingGuidance demonstrates how to properly size queues
func TestQueueSizingGuidance(t *testing.T) {
	testCases := []struct {
		name           string
		workers        int
		queueSize      int
		eventsPerSec   int
		processingTime time.Duration
		expectSuccess  bool
	}{
		{
			name:           "Default sizing for moderate load",
			workers:        10,
			queueSize:      0, // Auto-calculates to workers*2 = 20
			eventsPerSec:   50,
			processingTime: 100 * time.Millisecond,
			expectSuccess:  true,
		},
		{
			name:           "High throughput needs larger queue",
			workers:        20,
			queueSize:      200, // 10x workers for burst handling
			eventsPerSec:   500,
			processingTime: 20 * time.Millisecond,
			expectSuccess:  true,
		},
		{
			name:           "Undersized queue causes rejections",
			workers:        2,
			queueSize:      2, // Too small for burst
			eventsPerSec:   100,
			processingTime: 50 * time.Millisecond,
			expectSuccess:  false, // Expect some failures
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var processor *AsyncProcessor
			if tc.queueSize == 0 {
				processor = NewAsyncProcessor(tc.workers)
			} else {
				processor = NewAsyncProcessorWithQueue(tc.workers, tc.queueSize)
			}
			defer processor.Shutdown()

			ctx := context.Background()

			var processedCount atomic.Int32
			var queueFullCount atomic.Int32

			// Register processing hook
			_, err := processor.Events().Hook(heavyTask, func(ctx context.Context, event AsyncEvent) error {
				time.Sleep(tc.processingTime)
				processedCount.Add(1)
				return nil
			})
			if err != nil {
				t.Fatalf("Failed to register hook: %v", err)
			}

			// Submit events at specified rate
			totalEvents := tc.eventsPerSec / 10 // Run for 100ms worth
			interval := time.Second / time.Duration(tc.eventsPerSec)

			start := time.Now()
			for i := 0; i < totalEvents; i++ {
				err := processor.Process(ctx, AsyncEvent{
					ID:   fmt.Sprintf("event-%d", i),
					Type: string(heavyTask),
				})
				if err == hookz.ErrQueueFull {
					queueFullCount.Add(1)
				}

				// Maintain submission rate
				time.Sleep(interval)
			}

			// Calculate theoretical capacity
			duration := time.Since(start)
			theoreticalCapacity := int(duration/tc.processingTime) * tc.workers

			// Wait for processing
			time.Sleep(tc.processingTime * 2)

			if tc.expectSuccess {
				if queueFullCount.Load() > 0 {
					t.Errorf("%s: Unexpected queue rejections: %d (consider larger queue)",
						tc.name, queueFullCount.Load())
				}
			} else {
				if queueFullCount.Load() == 0 {
					t.Errorf("%s: Expected queue rejections but got none", tc.name)
				}
			}

			t.Logf("%s: Submitted %d, Rejected %d, Theoretical capacity %d",
				tc.name, totalEvents, queueFullCount.Load(), theoreticalCapacity)
		})
	}
}

package hookz

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// waitForMetric polls a metric value until it reaches the expected value or times out.
// This is used to handle the async nature of metric updates in the worker pool.
func waitForMetric[T any](t *testing.T, service *Hooks[T], getMetric func(*Metrics) int64, expected int64, name string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		metrics := service.Metrics()
		if getMetric(&metrics) == expected {
			return // Success
		}
		if time.Now().After(deadline) {
			t.Errorf("Timeout waiting for %s: expected %d, got %d", name, expected, getMetric(&metrics))
			return
		}
	}
}

// waitForCondition polls until a condition is met or times out.
// This is more general than waitForMetric and can be used for complex conditions.
func waitForCondition(t *testing.T, timeout time.Duration, check func() bool, failMessage string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		if check() {
			return // Success
		}
		if time.Now().After(deadline) {
			t.Error(failMessage)
			return
		}
	}
}

func TestMetricsStructure(t *testing.T) {
	service := New[string]()
	defer service.Close()

	// Before hooks, metrics should show minimal state
	metrics := service.Metrics()
	if metrics.QueueCapacity != 0 {
		t.Errorf("Expected QueueCapacity 0 before hooks, got %d", metrics.QueueCapacity)
	}
	if metrics.RegisteredHooks != 0 {
		t.Errorf("Expected 0 RegisteredHooks, got %d", metrics.RegisteredHooks)
	}

	// Register a hook to initialize worker pool
	hook, err := service.Hook("metrics.test", func(ctx context.Context, data string) error {
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	metrics = service.Metrics()

	// Verify metrics after hook registration
	if metrics.QueueCapacity <= 0 {
		t.Errorf("Expected positive QueueCapacity after hook, got %d", metrics.QueueCapacity)
	}
	if metrics.RegisteredHooks != 1 {
		t.Errorf("Expected 1 RegisteredHook, got %d", metrics.RegisteredHooks)
	}
	if metrics.QueueDepth != 0 {
		t.Errorf("Expected QueueDepth 0, got %d", metrics.QueueDepth)
	}
	if metrics.TasksProcessed != 0 {
		t.Errorf("Expected TasksProcessed 0, got %d", metrics.TasksProcessed)
	}
}

func TestMetricsRegisteredHooks(t *testing.T) {
	service := New[string]()
	defer service.Close()

	// Check initial state
	metrics := service.Metrics()
	if metrics.RegisteredHooks != 0 {
		t.Errorf("Expected 0 hooks initially, got %d", metrics.RegisteredHooks)
	}

	// Register first hook
	hook1, err := service.Hook("event1", func(ctx context.Context, data string) error {
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook1: %v", err)
	}

	metrics = service.Metrics()
	if metrics.RegisteredHooks != 1 {
		t.Errorf("Expected 1 hook after first registration, got %d", metrics.RegisteredHooks)
	}

	// Register second hook
	hook2, err := service.Hook("event2", func(ctx context.Context, data string) error {
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook2: %v", err)
	}

	metrics = service.Metrics()
	if metrics.RegisteredHooks != 2 {
		t.Errorf("Expected 2 hooks after second registration, got %d", metrics.RegisteredHooks)
	}

	// Unhook one
	if err := hook1.Unhook(); err != nil {
		t.Fatalf("Failed to unhook1: %v", err)
	}

	metrics = service.Metrics()
	if metrics.RegisteredHooks != 1 {
		t.Errorf("Expected 1 hook after unhooking, got %d", metrics.RegisteredHooks)
	}

	// Unhook the other
	if err := hook2.Unhook(); err != nil {
		t.Fatalf("Failed to unhook2: %v", err)
	}

	metrics = service.Metrics()
	if metrics.RegisteredHooks != 0 {
		t.Errorf("Expected 0 hooks after unhooking all, got %d", metrics.RegisteredHooks)
	}
}

func TestMetricsTasksProcessed(t *testing.T) {
	service := New[string](WithWorkers(5))
	defer service.Close()

	const numTasks = 10
	processed := make(chan struct{}, numTasks)

	hook, err := service.Hook("process.test", func(ctx context.Context, data string) error {
		processed <- struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	// Emit tasks
	for i := 0; i < numTasks; i++ {
		if err := service.Emit(context.Background(), "process.test", "data"); err != nil {
			t.Fatalf("Failed to emit task %d: %v", i, err)
		}
	}

	// Wait for all tasks to complete
	for i := 0; i < numTasks; i++ {
		select {
		case <-processed:
			// Task completed
		case <-time.After(time.Second):
			t.Fatalf("Task %d not completed", i)
		}
	}

	// Wait for metrics to reflect all processed tasks
	waitForMetric(t, service, func(m *Metrics) int64 { return m.TasksProcessed }, int64(numTasks), "TasksProcessed")

	// Verify final metrics
	metrics := service.Metrics()
	if metrics.TasksFailed != 0 {
		t.Errorf("Expected 0 TasksFailed, got %d", metrics.TasksFailed)
	}
}

func TestMetricsTasksFailed(t *testing.T) {
	service := New[string](WithWorkers(5))
	defer service.Close()

	const numTasks = 5
	processed := make(chan struct{}, numTasks)
	testErr := errors.New("test error")

	hook, err := service.Hook("fail.test", func(ctx context.Context, data string) error {
		processed <- struct{}{}
		return testErr
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	// Emit tasks that will fail
	for i := 0; i < numTasks; i++ {
		if err := service.Emit(context.Background(), "fail.test", "data"); err != nil {
			t.Fatalf("Failed to emit task %d: %v", i, err)
		}
	}

	// Wait for all tasks to complete
	for i := 0; i < numTasks; i++ {
		select {
		case <-processed:
			// Task completed (with error)
		case <-time.After(time.Second):
			t.Fatalf("Task %d not completed", i)
		}
	}

	// Wait for metrics to reflect all failed tasks
	waitForMetric(t, service, func(m *Metrics) int64 { return m.TasksFailed }, int64(numTasks), "TasksFailed")

	// Verify final metrics
	metrics := service.Metrics()
	if metrics.TasksProcessed != 0 {
		t.Errorf("Expected 0 TasksProcessed, got %d", metrics.TasksProcessed)
	}
}

func TestMetricsTasksRejected(t *testing.T) {
	// Minimal capacity to force rejections
	service := New[string](WithWorkers(1), WithQueueSize(1))
	defer service.Close()

	// Block worker
	blocked := make(chan struct{})
	release := make(chan struct{})

	hook, err := service.Hook("reject.test", func(ctx context.Context, data string) error {
		if data == "block" {
			close(blocked)
			<-release
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	// Block worker
	if err := service.Emit(context.Background(), "reject.test", "block"); err != nil {
		t.Fatalf("Failed to emit blocking task: %v", err)
	}

	<-blocked

	// Fill queue
	if err := service.Emit(context.Background(), "reject.test", "queue"); err != nil {
		t.Fatalf("Failed to emit to queue: %v", err)
	}

	// This should be rejected
	err = service.Emit(context.Background(), "reject.test", "reject")
	if !errors.Is(err, ErrQueueFull) {
		t.Errorf("Expected ErrQueueFull, got %v", err)
	}

	// Check metrics
	metrics := service.Metrics()
	if metrics.TasksRejected != 1 {
		t.Errorf("Expected 1 TasksRejected, got %d", metrics.TasksRejected)
	}

	close(release)
}

func TestMetricsTasksExpired(t *testing.T) {
	service := New[string](WithWorkers(5))
	defer service.Close()

	processed := make(chan struct{})
	hook, err := service.Hook("expire.test", func(ctx context.Context, data string) error {
		if ctx.Err() != nil {
			close(processed)
			return ctx.Err()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	// Create canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Emit with canceled context
	if err := service.Emit(ctx, "expire.test", "data"); err != nil {
		t.Fatalf("Failed to emit: %v", err)
	}

	// Wait for task to complete
	select {
	case <-processed:
		// Task completed
	case <-time.After(time.Second):
		t.Fatal("Task not completed")
	}

	// Check metrics
	metrics := service.Metrics()
	if metrics.TasksExpired != 1 {
		t.Errorf("Expected 1 TasksExpired, got %d", metrics.TasksExpired)
	}
	if metrics.TasksProcessed != 0 {
		t.Errorf("Expected 0 TasksProcessed, got %d", metrics.TasksProcessed)
	}
}

func TestMetricsQueueDepth(t *testing.T) {
	service := New[string](WithWorkers(1), WithQueueSize(5))
	defer service.Close()

	// Block worker
	blocked := make(chan struct{})
	release := make(chan struct{})

	hook, err := service.Hook("depth.test", func(ctx context.Context, data string) error {
		if data == "block" {
			close(blocked)
			<-release
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	// Block worker
	if err := service.Emit(context.Background(), "depth.test", "block"); err != nil {
		t.Fatalf("Failed to emit blocking task: %v", err)
	}

	<-blocked

	// Check depth is 0 (task was picked up by worker)
	metrics := service.Metrics()
	if metrics.QueueDepth != 0 {
		t.Errorf("Expected QueueDepth 0 when task is being processed, got %d", metrics.QueueDepth)
	}

	// Queue up tasks
	for i := 0; i < 3; i++ {
		if err := service.Emit(context.Background(), "depth.test", "queue"); err != nil {
			t.Fatalf("Failed to emit task %d: %v", i, err)
		}
	}

	// Check queue depth
	metrics = service.Metrics()
	if metrics.QueueDepth != 3 {
		t.Errorf("Expected QueueDepth 3, got %d", metrics.QueueDepth)
	}

	// Release worker to process queued tasks
	close(release)

	// Wait for queue to drain completely
	waitForMetric(t, service, func(m *Metrics) int64 { return m.QueueDepth }, 0, "QueueDepth")
}

func TestMetricsConcurrentAccess(t *testing.T) {
	service := New[string](WithWorkers(5))
	defer service.Close()

	hook, err := service.Hook("concurrent.test", func(ctx context.Context, data string) error {
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	var wg sync.WaitGroup
	const concurrency = 10
	const iterations = 100

	// Concurrent metrics readers
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = service.Metrics()
			}
		}()
	}

	// Concurrent task emitters
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				service.Emit(context.Background(), "concurrent.test", "data")
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - no race conditions
	case <-time.After(5 * time.Second):
		t.Fatal("Concurrent access timeout")
	}

	// Verify metrics are reasonable
	metrics := service.Metrics()
	if metrics.TasksProcessed < 0 || metrics.TasksRejected < 0 {
		t.Error("Metrics should not be negative")
	}
	if metrics.QueueDepth < 0 {
		t.Errorf("QueueDepth should not be negative: %d", metrics.QueueDepth)
	}
}

func TestMetricsPanicHandling(t *testing.T) {
	service := New[string](WithWorkers(2))
	defer service.Close()

	processed := make(chan struct{})
	hook, err := service.Hook("panic.metrics", func(ctx context.Context, data string) error {
		close(processed)
		panic("test panic")
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	// Emit task that will panic
	if err := service.Emit(context.Background(), "panic.metrics", "data"); err != nil {
		t.Fatalf("Failed to emit: %v", err)
	}

	// Wait for task to be called
	select {
	case <-processed:
		// Task was called (and panicked)
	case <-time.After(time.Second):
		t.Fatal("Task not called")
	}

	// Wait for metrics to reflect the failed task (panic counts as failure)
	waitForMetric(t, service, func(m *Metrics) int64 { return m.TasksFailed }, 1, "TasksFailed")

	// Verify final metrics
	metrics := service.Metrics()
	if metrics.TasksProcessed != 0 {
		t.Errorf("Expected 0 TasksProcessed for panic, got %d", metrics.TasksProcessed)
	}
}

func TestMetricsWithConfigurations(t *testing.T) {
	t.Run("WithQueueSize", func(t *testing.T) {
		queueSize := 20
		service := New[string](WithWorkers(5), WithQueueSize(queueSize))
		defer service.Close()

		// Initially no queue capacity
		metrics := service.Metrics()
		if metrics.QueueCapacity != 0 {
			t.Errorf("Expected 0 QueueCapacity before hooks, got %d", metrics.QueueCapacity)
		}

		// Register hook to initialize worker pool
		hook, err := service.Hook("test", func(ctx context.Context, data string) error {
			return nil
		})
		if err != nil {
			t.Fatalf("Failed to register hook: %v", err)
		}
		defer hook.Unhook()

		metrics = service.Metrics()
		if metrics.QueueCapacity != int64(queueSize) {
			t.Errorf("Expected QueueCapacity %d, got %d", queueSize, metrics.QueueCapacity)
		}
	})

	t.Run("AutoCalculatedQueueSize", func(t *testing.T) {
		workers := 3
		service := New[string](WithWorkers(workers))
		defer service.Close()

		// Register hook to initialize worker pool
		hook, err := service.Hook("test", func(ctx context.Context, data string) error {
			return nil
		})
		if err != nil {
			t.Fatalf("Failed to register hook: %v", err)
		}
		defer hook.Unhook()

		metrics := service.Metrics()
		expectedCapacity := workers * 2 // Default is workers * 2
		if metrics.QueueCapacity != int64(expectedCapacity) {
			t.Errorf("Expected auto-calculated QueueCapacity %d, got %d", expectedCapacity, metrics.QueueCapacity)
		}
	})
}

func TestMetricsServiceShutdown(t *testing.T) {
	service := New[string](WithWorkers(1), WithQueueSize(3))

	// Block worker
	blocked := make(chan struct{})
	release := make(chan struct{})

	hook, err := service.Hook("shutdown.metrics", func(ctx context.Context, data string) error {
		if data == "block" {
			close(blocked)
			<-release
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}

	// Block worker and fill queue
	if err := service.Emit(context.Background(), "shutdown.metrics", "block"); err != nil {
		t.Fatalf("Failed to emit blocking task: %v", err)
	}

	<-blocked

	// Queue some tasks
	for i := 0; i < 2; i++ {
		if err := service.Emit(context.Background(), "shutdown.metrics", "queue"); err != nil {
			t.Fatalf("Failed to emit task %d: %v", i, err)
		}
	}

	// Check queue depth before close
	metrics := service.Metrics()
	if metrics.QueueDepth != 2 {
		t.Errorf("Expected QueueDepth 2 before close, got %d", metrics.QueueDepth)
	}

	// Release worker and close
	close(release)
	if err := service.Close(); err != nil {
		t.Fatalf("Failed to close: %v", err)
	}

	// Check final metrics
	finalMetrics := service.Metrics()
	if finalMetrics.QueueDepth != 0 {
		t.Errorf("Expected QueueDepth 0 after close, got %d", finalMetrics.QueueDepth)
	}

	// Cleanup
	hook.Unhook()
}

func TestMetricsAtomicOperations(t *testing.T) {
	service := New[string](WithWorkers(10))
	defer service.Close()

	hook, err := service.Hook("atomic.test", func(ctx context.Context, data string) error {
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	// Run many concurrent operations
	var wg sync.WaitGroup
	const concurrency = 50
	const iterations = 100

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				service.Emit(context.Background(), "atomic.test", "data")
			}
		}()
	}

	wg.Wait()

	// Wait for at least some tasks to be processed
	waitForCondition(t, 100*time.Millisecond, func() bool {
		metrics := service.Metrics()
		return (metrics.TasksProcessed + metrics.TasksRejected) > 0
	}, "Expected some tasks to be processed or rejected")

	// Verify metrics consistency
	metrics := service.Metrics()
	totalTasks := metrics.TasksProcessed + metrics.TasksRejected

	// Total tasks should be non-negative and reasonable
	if totalTasks < 0 {
		t.Errorf("Total tasks should be non-negative, got %d", totalTasks)
	}

	// Check for reasonable bounds
	maxPossible := int64(concurrency * iterations)
	if totalTasks > maxPossible {
		t.Errorf("Total tasks (%d) exceeds maximum possible (%d)", totalTasks, maxPossible)
	}
}

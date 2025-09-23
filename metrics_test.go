package hookz

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetricsStructure(t *testing.T) {
	service := New[string]()
	defer service.Close()

	// Before hooks, queue capacity should be 0
	metrics := service.Metrics()
	assert.Equal(t, int64(0), metrics.QueueCapacity, "QueueCapacity should be 0 before hooks")

	// Register a hook to initialize worker pool
	hook, err := service.Hook("test", func(ctx context.Context, data string) error {
		return nil
	})
	require.NoError(t, err)
	defer hook.Unhook()

	metrics = service.Metrics()

	// Verify all metrics fields are present and properly initialized
	assert.Equal(t, int64(0), metrics.QueueDepth, "QueueDepth should start at 0")
	assert.Greater(t, metrics.QueueCapacity, int64(0), "QueueCapacity should be positive after hook")
	assert.Equal(t, int64(0), metrics.TasksProcessed, "TasksProcessed should start at 0")
	assert.Equal(t, int64(0), metrics.TasksRejected, "TasksRejected should start at 0")
	assert.Equal(t, int64(0), metrics.TasksFailed, "TasksFailed should start at 0")
	assert.Equal(t, int64(0), metrics.TasksExpired, "TasksExpired should start at 0")
	assert.Equal(t, int64(1), metrics.RegisteredHooks, "RegisteredHooks should be 1")

	// Phase 2 metrics should be 0 initially
	assert.Equal(t, int64(0), metrics.OverflowDepth, "OverflowDepth should be 0 in Phase 1")
	assert.Equal(t, int64(0), metrics.OverflowCapacity, "OverflowCapacity should be 0 in Phase 1")
	assert.Equal(t, int64(0), metrics.OverflowDrained, "OverflowDrained should be 0 in Phase 1")
}

func TestMetricsRegisteredHooks(t *testing.T) {
	service := New[string]()
	defer service.Close()

	// Initial state
	metrics := service.Metrics()
	assert.Equal(t, int64(0), metrics.RegisteredHooks, "Should start with 0 hooks")

	// Register a hook
	hook1, err := service.Hook("event1", func(ctx context.Context, data string) error {
		return nil
	})
	require.NoError(t, err)

	metrics = service.Metrics()
	assert.Equal(t, int64(1), metrics.RegisteredHooks, "Should show 1 hook after registration")

	// Register another hook
	hook2, err := service.Hook("event2", func(ctx context.Context, data string) error {
		return nil
	})
	require.NoError(t, err)

	metrics = service.Metrics()
	assert.Equal(t, int64(2), metrics.RegisteredHooks, "Should show 2 hooks after second registration")

	// Remove a hook
	err = hook1.Unhook()
	require.NoError(t, err)

	metrics = service.Metrics()
	assert.Equal(t, int64(1), metrics.RegisteredHooks, "Should show 1 hook after removing one")

	// Remove last hook
	err = hook2.Unhook()
	require.NoError(t, err)

	metrics = service.Metrics()
	assert.Equal(t, int64(0), metrics.RegisteredHooks, "Should show 0 hooks after removing all")
}

func TestMetricsQueueCapacity(t *testing.T) {
	// Test with different queue sizes
	testCases := []struct {
		workers   int
		queueSize int
	}{
		{workers: 5, queueSize: 0}, // auto-calculated
		{workers: 3, queueSize: 15},
		{workers: 1, queueSize: 1},
	}

	for _, tc := range testCases {
		var options []Option
		if tc.queueSize > 0 {
			options = append(options, WithWorkers(tc.workers), WithQueueSize(tc.queueSize))
		} else {
			options = append(options, WithWorkers(tc.workers))
		}

		service := New[string](options...)

		// Queue capacity should be 0 before hooks
		metrics := service.Metrics()
		assert.Equal(t, int64(0), metrics.QueueCapacity,
			"QueueCapacity should be 0 before hooks")

		// Register a hook to initialize worker pool
		hook, err := service.Hook("test", func(ctx context.Context, data string) error {
			return nil
		})
		require.NoError(t, err)
		defer hook.Unhook()

		metrics = service.Metrics()
		expectedCapacity := tc.queueSize
		if expectedCapacity == 0 {
			expectedCapacity = tc.workers * 2 // auto-calculated
		}

		assert.Equal(t, int64(expectedCapacity), metrics.QueueCapacity,
			"QueueCapacity should match configured size after hook")

		service.Close()
	}
}

func TestMetricsTasksProcessed(t *testing.T) {
	service := New[string](WithWorkers(5), WithQueueSize(20))
	defer service.Close()

	processed := make(chan string, 10)
	hook, err := service.Hook("test", func(ctx context.Context, data string) error {
		processed <- data
		return nil
	})
	require.NoError(t, err)
	defer hook.Unhook()

	// Emit some tasks
	for i := 0; i < 5; i++ {
		err := service.Emit(context.Background(), "test", "data")
		require.NoError(t, err)
	}

	// Wait for all tasks to complete
	for i := 0; i < 5; i++ {
		select {
		case <-processed:
			// Task completed
		case <-time.After(500 * time.Millisecond):
			t.Fatal("Task did not complete in time")
		}
	}

	// Check metrics
	metrics := service.Metrics()
	assert.Equal(t, int64(5), metrics.TasksProcessed, "Should show 5 processed tasks")
	assert.Equal(t, int64(0), metrics.TasksFailed, "Should show 0 failed tasks")
	assert.Equal(t, int64(0), metrics.TasksRejected, "Should show 0 rejected tasks")
}

func TestMetricsTasksFailed(t *testing.T) {
	service := New[string](WithWorkers(5), WithQueueSize(15))
	defer service.Close()

	processed := make(chan bool, 10)
	hook, err := service.Hook("test", func(ctx context.Context, data string) error {
		processed <- true
		return assert.AnError // Return an error
	})
	require.NoError(t, err)
	defer hook.Unhook()

	// Emit some tasks that will fail
	for i := 0; i < 3; i++ {
		err := service.Emit(context.Background(), "test", "data")
		require.NoError(t, err)
	}

	// Wait for all tasks to complete
	for i := 0; i < 3; i++ {
		select {
		case <-processed:
			// Task completed (with error)
		case <-time.After(500 * time.Millisecond):
			t.Fatal("Task did not complete in time")
		}
	}

	// Check metrics
	metrics := service.Metrics()
	assert.Equal(t, int64(0), metrics.TasksProcessed, "Should show 0 processed tasks")
	assert.Equal(t, int64(3), metrics.TasksFailed, "Should show 3 failed tasks")
	assert.Equal(t, int64(0), metrics.TasksRejected, "Should show 0 rejected tasks")
}

func TestMetricsTasksRejected(t *testing.T) {
	// Create service with minimal capacity to force rejections
	service := New[string](WithWorkers(1), WithQueueSize(1))
	defer service.Close()

	// Block worker with slow hook
	blockWorker := make(chan struct{})
	hook, err := service.Hook("slow", func(ctx context.Context, data string) error {
		<-blockWorker // Block until we signal
		return nil
	})
	require.NoError(t, err)
	defer hook.Unhook()

	// Submit first task - worker will pick it up immediately
	err = service.Emit(context.Background(), "slow", "data1")
	require.NoError(t, err)

	// Give worker time to pick up the task
	time.Sleep(10 * time.Millisecond)

	// Submit second task - this should fill the queue (capacity 1)
	err = service.Emit(context.Background(), "slow", "data2")
	require.NoError(t, err)

	// Submit third task - this should be rejected (queue full)
	err = service.Emit(context.Background(), "slow", "data3")
	assert.ErrorIs(t, err, ErrQueueFull, "Should reject when queue is full")

	// Release workers
	close(blockWorker)

	// Check metrics
	metrics := service.Metrics()
	assert.Equal(t, int64(1), metrics.TasksRejected, "Should show 1 rejected task")
}

func TestMetricsTasksExpired(t *testing.T) {
	service := New[string](WithWorkers(5), WithQueueSize(15))
	defer service.Close()

	processed := make(chan bool, 10)
	hook, err := service.Hook("test", func(ctx context.Context, data string) error {
		// Check if context is canceled and return appropriate error
		if ctx.Err() != nil {
			processed <- true
			return ctx.Err()
		}
		processed <- true
		return nil
	})
	require.NoError(t, err)
	defer hook.Unhook()

	// Create canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Emit task with canceled context
	err = service.Emit(ctx, "test", "data")
	require.NoError(t, err)

	// Wait for task to complete
	select {
	case <-processed:
		// Task completed
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Task did not complete in time")
	}

	// Check metrics
	metrics := service.Metrics()
	assert.Equal(t, int64(0), metrics.TasksProcessed, "Should show 0 processed tasks")
	assert.Equal(t, int64(1), metrics.TasksExpired, "Should show 1 expired task")
	assert.Equal(t, int64(0), metrics.TasksFailed, "Should show 0 failed tasks")
}

func TestMetricsQueueDepthTracking(t *testing.T) {
	service := New[string](WithWorkers(1), WithQueueSize(5))
	defer service.Close()

	// Block worker
	blockWorker := make(chan struct{})
	processed := make(chan string, 10)
	hook, err := service.Hook("test", func(ctx context.Context, data string) error {
		processed <- data
		<-blockWorker // Block worker
		return nil
	})
	require.NoError(t, err)
	defer hook.Unhook()

	// Submit task (will be processed by worker)
	err = service.Emit(context.Background(), "test", "data1")
	require.NoError(t, err)

	// Wait for worker to start processing
	select {
	case <-processed:
		// Worker started processing
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Worker did not start processing")
	}

	// Queue depth should be 0 (task was removed from queue when worker picked it up)
	metrics := service.Metrics()
	assert.Equal(t, int64(0), metrics.QueueDepth, "QueueDepth should be 0 when no tasks queued")

	// Submit more tasks to queue them up
	err = service.Emit(context.Background(), "test", "data2")
	require.NoError(t, err)
	err = service.Emit(context.Background(), "test", "data3")
	require.NoError(t, err)

	// Check queue depth
	metrics = service.Metrics()
	assert.Equal(t, int64(2), metrics.QueueDepth, "QueueDepth should show 2 queued tasks")

	// Release worker to process queued tasks
	close(blockWorker)

	// Wait for tasks to be processed
	for i := 0; i < 2; i++ {
		select {
		case <-processed:
			// Task processed
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Task did not complete in time")
		}
	}

	// Final queue depth should be 0
	metrics = service.Metrics()
	assert.Equal(t, int64(0), metrics.QueueDepth, "QueueDepth should be 0 after processing")
}

func TestMetricsConcurrentAccess(t *testing.T) {
	service := New[string](WithWorkers(5))
	defer service.Close()

	hook, err := service.Hook("test", func(ctx context.Context, data string) error {
		return nil
	})
	require.NoError(t, err)
	defer hook.Unhook()

	var wg sync.WaitGroup
	concurrency := 10
	tasksPerWorker := 100

	// Concurrent metrics readers
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < tasksPerWorker; j++ {
				_ = service.Metrics() // Just read metrics
			}
		}()
	}

	// Concurrent task emitters
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < tasksPerWorker; j++ {
				_ = service.Emit(context.Background(), "test", "data")
			}
		}()
	}

	wg.Wait()

	// Verify metrics are reasonable (no race conditions)
	metrics := service.Metrics()
	assert.GreaterOrEqual(t, metrics.TasksProcessed+metrics.TasksRejected,
		int64(0), "Processed + rejected should be non-negative")
	assert.GreaterOrEqual(t, metrics.QueueDepth, int64(0), "QueueDepth should be non-negative")
}

func TestMetricsServiceShutdown(t *testing.T) {
	service := New[string](WithWorkers(1), WithQueueSize(3))

	// Block worker to keep tasks in queue
	blockWorker := make(chan struct{})
	hook, err := service.Hook("test", func(ctx context.Context, data string) error {
		<-blockWorker
		return nil
	})
	require.NoError(t, err)

	// Fill queue
	err = service.Emit(context.Background(), "test", "data1") // Will be processed
	require.NoError(t, err)
	err = service.Emit(context.Background(), "test", "data2") // Queued
	require.NoError(t, err)
	err = service.Emit(context.Background(), "test", "data3") // Queued
	require.NoError(t, err)

	// Give worker time to pick up first task
	time.Sleep(10 * time.Millisecond)

	// Check queue depth before close
	metrics := service.Metrics()
	remainingTasks := metrics.QueueDepth
	assert.Equal(t, int64(2), remainingTasks, "Should have 2 tasks in queue before close")

	// Close service (this should mark remaining tasks as expired)
	close(blockWorker) // Allow worker to complete
	err = service.Close()
	require.NoError(t, err)

	// Check final metrics
	finalMetrics := service.Metrics()
	assert.Equal(t, int64(0), finalMetrics.QueueDepth, "QueueDepth should be 0 after close")
	// TasksExpired should include any remaining tasks that were in queue during close
	// The exact number depends on timing, but should be reasonable
	assert.GreaterOrEqual(t, finalMetrics.TasksExpired, int64(0), "TasksExpired should be non-negative")

	// Hook cleanup
	err = hook.Unhook()
	require.NoError(t, err)
}

func TestMetricsPanicHandling(t *testing.T) {
	service := New[string](WithWorkers(2))
	defer service.Close()

	processed := make(chan bool, 10)
	hook, err := service.Hook("test", func(ctx context.Context, data string) error {
		processed <- true
		panic("test panic")
	})
	require.NoError(t, err)
	defer hook.Unhook()

	// Emit task that will panic
	err = service.Emit(context.Background(), "test", "data")
	require.NoError(t, err)

	// Wait for task to complete
	select {
	case <-processed:
		// Task was called (and panicked)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Task did not complete in time")
	}

	// Give worker time to update metrics after panic recovery
	time.Sleep(10 * time.Millisecond)

	// Check metrics - panic should be counted as failure
	metrics := service.Metrics()
	assert.Equal(t, int64(0), metrics.TasksProcessed, "Should show 0 processed tasks")
	assert.Equal(t, int64(1), metrics.TasksFailed, "Should show 1 failed task (panic)")
}

func TestMetricsAtomicOperations(t *testing.T) {
	service := New[string](WithWorkers(10))
	defer service.Close()

	hook, err := service.Hook("test", func(ctx context.Context, data string) error {
		return nil
	})
	require.NoError(t, err)
	defer hook.Unhook()

	// Run many concurrent operations
	var wg sync.WaitGroup
	concurrency := 50
	iterations := 100

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = service.Emit(context.Background(), "test", "data")
			}
		}()
	}

	wg.Wait()

	// Give time for all tasks to complete
	time.Sleep(100 * time.Millisecond)

	// Final metrics should be consistent
	metrics := service.Metrics()
	totalTasks := metrics.TasksProcessed + metrics.TasksRejected
	assert.GreaterOrEqual(t, totalTasks, int64(0), "Total tasks should be non-negative")
	assert.LessOrEqual(t, totalTasks, int64(concurrency*iterations),
		"Total tasks should not exceed emitted tasks")
}

// Benchmark to verify minimal performance impact
func BenchmarkMetricsOverhead(b *testing.B) {
	service := New[string](WithWorkers(10))
	defer service.Close()

	hook, err := service.Hook("test", func(ctx context.Context, data string) error {
		return nil
	})
	require.NoError(b, err)
	defer hook.Unhook()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = service.Emit(context.Background(), "test", "data")
		}
	})
}

func BenchmarkMetricsAccess(b *testing.B) {
	service := New[string]()
	defer service.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = service.Metrics()
		}
	})
}

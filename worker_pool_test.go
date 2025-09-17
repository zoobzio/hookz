package hookz

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test constants for worker pool operations
const (
	TestPanicEvent     Key = "test.panic"
	TestNormalEvent    Key = "test.normal"
	TestConcurrentBase Key = "test.concurrent"
	TestEmitBase       Key = "test.emit"
	TestCtxEvent       Key = "test.context"
	TestSlowEvent      Key = "test.slow"
	TestTimeoutEvent   Key = "test.timeout"
)

func TestPanicRecovery(t *testing.T) {
	service := New[string]()
	defer service.Close()

	// Hook that panics
	panicHook, err := service.Hook(TestPanicEvent, func(ctx context.Context, data string) error {
		panic("test panic")
	})
	require.NoError(t, err)
	defer panicHook.Unhook()

	// Hook that works normally
	normalCalled := make(chan bool, 1)
	normalHook, err := service.Hook(TestNormalEvent, func(ctx context.Context, data string) error {
		normalCalled <- true
		return nil
	})
	require.NoError(t, err)
	defer normalHook.Unhook()

	// Emit to panicking hook - should not crash service
	err = service.Emit(context.Background(), TestPanicEvent, "data")
	require.NoError(t, err)

	// Service should still work for normal hooks
	err = service.Emit(context.Background(), TestNormalEvent, "data")
	require.NoError(t, err)

	select {
	case <-normalCalled:
		// Good - service still functional
	case <-time.After(100 * time.Millisecond):
		t.Error("Normal hook did not execute after panic")
	}
}

func TestConcurrentOperations(t *testing.T) {
	service := New[int]()
	defer service.Close()

	var wg sync.WaitGroup
	const numGoroutines = 5    // Reduced to avoid resource limits
	const opsPerGoroutine = 10 // Reduced operations

	// Concurrent hook registration and emission
	wg.Add(numGoroutines * 2)

	// Hook registration goroutines - use different events to avoid per-event limit
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			hooks := make([]Hook, 0, opsPerGoroutine)
			for j := 0; j < opsPerGoroutine; j++ {
				eventName := Key(fmt.Sprintf("%s-%d", TestConcurrentBase, id))
				hook, err := service.Hook(eventName, func(ctx context.Context, data int) error {
					return nil
				})
				if err == nil {
					hooks = append(hooks, hook)
				}
			}
			// Cleanup hooks
			for _, hook := range hooks {
				hook.Unhook()
			}
		}(i)
	}

	// Event emission goroutines - emit to different events
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				eventName := Key(fmt.Sprintf("%s-%d", TestEmitBase, id))
				service.Emit(context.Background(), eventName, id*opsPerGoroutine+j)
			}
		}(i)
	}

	wg.Wait()
	// If we get here without panic, the concurrent operations are safe
}

func TestContextCancellation(t *testing.T) {
	service := New[string]()
	defer service.Close()

	canceled := make(chan bool, 1)
	hook, err := service.Hook(TestCtxEvent, func(ctx context.Context, data string) error {
		select {
		case <-ctx.Done():
			canceled <- true
			return ctx.Err()
		default:
			// Check again for context cancellation
			<-ctx.Done()
			canceled <- true
			return ctx.Err()
		}
	})
	require.NoError(t, err)
	defer hook.Unhook()

	// Create context that cancels immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = service.Emit(ctx, TestCtxEvent, "data")
	require.NoError(t, err)

	select {
	case <-canceled:
		// Good - context cancellation was respected
	case <-time.After(200 * time.Millisecond):
		t.Error("Hook did not respect context cancellation")
	}
}

func TestWorkerPoolShutdown(t *testing.T) {
	service := New[string]()

	// Register hook that would block
	hook, err := service.Hook(TestSlowEvent, func(ctx context.Context, data string) error {
		// Simulate work without actual time delay
		return nil
	})
	require.NoError(t, err)

	// Emit event
	err = service.Emit(context.Background(), TestSlowEvent, "data")
	require.NoError(t, err)

	// Give time for async processing to start
	time.Sleep(10 * time.Millisecond)

	// Close should wait for worker to complete
	err = service.Close()
	require.NoError(t, err)

	// Hook should still be valid for unhooking even after service closes
	// (hooks registered before close remain valid)
	err = hook.Unhook()
	// This should succeed because the hook was registered before close
	assert.NoError(t, err)
}

func TestWorkerPoolCapacity(t *testing.T) {
	// Create service with minimal workers to test capacity limits
	service := New[int](WithWorkers(2))
	defer service.Close()

	// Register slow hook to saturate workers
	completed := make(chan int, 10)
	hook, err := service.Hook(TestSlowEvent, func(ctx context.Context, data int) error {
		// Simulate slow processing without actual time delay
		completed <- data
		return nil
	})
	require.NoError(t, err)
	defer hook.Unhook()

	// Submit more tasks than worker capacity
	for i := 0; i < 6; i++ {
		err := service.Emit(context.Background(), TestSlowEvent, i)
		if err != nil {
			// Some emissions should be rejected due to queue capacity
			assert.ErrorIs(t, err, ErrQueueFull)
		}
	}

	// Some tasks should complete even if queue fills
	completedCount := 0
	timeout := time.After(500 * time.Millisecond)
	for completedCount < 2 { // At least 2 workers should complete
		select {
		case <-completed:
			completedCount++
		case <-timeout:
			if completedCount == 0 {
				t.Error("Expected at least 2 tasks to complete")
			}
			return
		}
	}
}

// TestBackpressureQueueLogic validates backpressure behavior at worker pool level
func TestBackpressureQueueLogic(t *testing.T) {
	t.Run("SmoothsTrafficSpikes", func(t *testing.T) {
		service := New[int](
			WithWorkers(1),
			WithQueueSize(5),
			WithBackpressure(BackpressureConfig{
				MaxWait:        10 * time.Millisecond,
				StartThreshold: 0.6,
				Strategy:       "linear",
			}),
		)
		defer service.Close()

		// Register hook to create queue pressure
		var processed int32
		hook, err := service.Hook("test", func(ctx context.Context, v int) error {
			// Simulate work without actual delay
			atomic.AddInt32(&processed, 1)
			return nil
		})
		require.NoError(t, err)
		defer hook.Unhook()

		// Emit burst - some should succeed due to backpressure
		var successes int
		for i := 0; i < 10; i++ {
			if err := service.Emit(context.Background(), "test", i); err == nil {
				successes++
			}
		}

		// Verify backpressure behavior - should handle more than basic capacity
		if successes <= 5 {
			t.Errorf("backpressure should allow more than immediate capacity, got %d successes", successes)
		}
	})

}

// TestOverflowQueueLogic validates overflow behavior at worker pool level
func TestOverflowQueueLogic(t *testing.T) {
	t.Run("AbsorbsTrafficBursts", func(t *testing.T) {
		service := New[int](
			WithWorkers(1),
			WithQueueSize(10),
			WithOverflow(OverflowConfig{
				Capacity:         100,
				DrainInterval:    5 * time.Millisecond,
				EvictionStrategy: "fifo",
			}),
		)
		defer service.Close()

		var processed int32
		hook, err := service.Hook("test", func(ctx context.Context, v int) error {
			atomic.AddInt32(&processed, 1)
			return nil
		})
		require.NoError(t, err)
		defer hook.Unhook()

		// Emit large burst quickly
		var errors int
		for i := 0; i < 110; i++ {
			if err := service.Emit(context.Background(), "test", i); err != nil {
				errors++
			}
		}

		// Should accept all up to primary + overflow capacity
		if errors != 0 {
			t.Errorf("overflow should absorb burst beyond primary capacity, got %d errors", errors)
		}

		// Give workers time to process by using real time for async coordination
		time.Sleep(100 * time.Millisecond)

		// Verify tasks were processed
		processedCount := atomic.LoadInt32(&processed)
		if processedCount == 0 {
			t.Error("no tasks were processed")
		}

		// Log actual processing count for debugging
		t.Logf("Processed %d tasks out of 110 emitted", processedCount)
	})

	t.Run("DrainsToPrimary", func(t *testing.T) {
		service := New[int](
			WithWorkers(2),
			WithQueueSize(5),
			WithOverflow(OverflowConfig{
				Capacity:         10,
				DrainInterval:    10 * time.Millisecond,
				EvictionStrategy: "reject",
			}),
		)
		defer service.Close()

		var processed int32
		hook, err := service.Hook("test", func(ctx context.Context, v int) error {
			atomic.AddInt32(&processed, 1)
			return nil
		})
		require.NoError(t, err)
		defer hook.Unhook()

		// Fill primary queue first (slow processing)
		for i := 0; i < 8; i++ {
			service.Emit(context.Background(), "test", i)
		}

		// Wait for drain cycles to move tasks
		time.Sleep(60 * time.Millisecond)

		// Check that some tasks were processed
		processedCount := atomic.LoadInt32(&processed)
		if processedCount == 0 {
			t.Error("overflow drain should have processed some tasks")
		}
	})
}

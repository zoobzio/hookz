package integration

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/zoobzio/hookz"
)

// TestCombinedResilience validates backpressure + overflow working together
func TestCombinedResilience(t *testing.T) {
	t.Run("GracefulDegradation", func(t *testing.T) {
		service := New[int](
			WithWorkers(2),
			WithQueueSize(20),
			WithBackpressure(BackpressureConfig{
				MaxWait:        5 * time.Millisecond,
				StartThreshold: 0.8,
				Strategy:       "linear",
			}),
			WithOverflow(OverflowConfig{
				Capacity:         50,
				DrainInterval:    2 * time.Millisecond,
				EvictionStrategy: "fifo",
			}),
		)
		defer service.Close()

		var processed int32
		hook, err := service.Hook("test", func(ctx context.Context, v int) error {
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt32(&processed, 1)
			return nil
		})
		if err != nil {
			t.Fatalf("failed to register hook: %v", err)
		}
		defer hook.Unhook()

		// Phase 1: Normal load (should succeed immediately)
		for i := 0; i < 10; i++ {
			err := service.Emit(context.Background(), "test", i)
			if err != nil {
				t.Errorf("normal load should succeed, got error: %v", err)
			}
		}

		// Phase 2: High load (triggers backpressure, then overflow)
		var errors int
		for i := 10; i < 80; i++ {
			if err := service.Emit(context.Background(), "test", i); err != nil {
				errors++
			}
		}

		// Most should succeed due to combined resilience
		if errors > 10 {
			t.Errorf("combined resilience should handle most burst traffic, got %d errors", errors)
		}

		// Wait for processing
		time.Sleep(100 * time.Millisecond)

		// Verify reasonable processing occurred
		processedCount := atomic.LoadInt32(&processed)
		if processedCount < 20 {
			t.Errorf("expected significant processing, got %d", processedCount)
		}
	})

	t.Run("ContextCancellationInCombined", func(t *testing.T) {
		service := New[int](
			WithWorkers(1),
			WithQueueSize(2),
			WithBackpressure(BackpressureConfig{
				MaxWait:        50 * time.Millisecond,
				StartThreshold: 0.5,
				Strategy:       "fixed",
			}),
			WithOverflow(OverflowConfig{
				Capacity:         10,
				DrainInterval:    100 * time.Millisecond,
				EvictionStrategy: "fifo",
			}),
		)
		defer service.Close()

		// Fill queue completely
		hook, err := service.Hook("test", func(ctx context.Context, v int) error {
			time.Sleep(100 * time.Millisecond)
			return nil
		})
		if err != nil {
			t.Fatalf("failed to register hook: %v", err)
		}
		defer hook.Unhook()

		// Fill primary queue
		for i := 0; i < 3; i++ {
			service.Emit(context.Background(), "test", i)
		}

		// Emit with canceled context during backpressure phase
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		err = service.Emit(ctx, "test", 99)
		if err != context.DeadlineExceeded {
			t.Errorf("expected context cancellation during backpressure, got %v", err)
		}
	})
}

// TestShutdownBehavior validates proper cleanup of resilience features
func TestShutdownBehavior(t *testing.T) {
	t.Run("OverflowDrainsDuringShutdown", func(t *testing.T) {
		service := New[int](
			WithWorkers(1),
			WithQueueSize(2),
			WithOverflow(OverflowConfig{
				Capacity:         10,
				DrainInterval:    100 * time.Millisecond, // Slow drain
				EvictionStrategy: "fifo",
			}),
		)

		var processed int32
		hook, err := service.Hook("test", func(ctx context.Context, v int) error {
			atomic.AddInt32(&processed, 1)
			return nil
		})
		if err != nil {
			t.Fatalf("failed to register hook: %v", err)
		}
		// Note: hook.Unhook() not needed since service.Close() handles cleanup
		_ = hook // Suppress unused variable warning

		// Fill overflow queue (primary will be full too)
		for i := 0; i < 12; i++ {
			service.Emit(context.Background(), "test", i)
		}

		// Close should drain remaining tasks
		service.Close()

		// Give time for drain completion
		time.Sleep(50 * time.Millisecond)

		processedCount := atomic.LoadInt32(&processed)
		if processedCount == 0 {
			t.Error("shutdown should have drained and processed some overflow tasks")
		}
	})

	t.Run("NoDeadlockDuringConcurrentClose", func(t *testing.T) {
		service := New[int](
			WithWorkers(2),
			WithQueueSize(5),
			WithBackpressure(BackpressureConfig{
				MaxWait:        20 * time.Millisecond,
				StartThreshold: 0.5,
				Strategy:       "linear",
			}),
		)

		hook, err := service.Hook("test", func(ctx context.Context, v int) error {
			time.Sleep(10 * time.Millisecond)
			return nil
		})
		if err != nil {
			t.Fatalf("failed to register hook: %v", err)
		}
		defer hook.Unhook()

		// Start concurrent operations
		done := make(chan bool)
		go func() {
			for i := 0; i < 20; i++ {
				service.Emit(context.Background(), "test", i)
				time.Sleep(1 * time.Millisecond)
			}
			done <- true
		}()

		// Close while operations are ongoing
		time.Sleep(10 * time.Millisecond)
		err = service.Close()

		if err != nil {
			t.Errorf("close should succeed even with concurrent operations, got %v", err)
		}

		// Wait for goroutine completion
		<-done
	})
}

// TestEdgeCases validates handling of unusual configurations and conditions
func TestEdgeCases(t *testing.T) {
	t.Run("BackpressureWithTinyQueue", func(t *testing.T) {
		// Edge case: queue size of 1 with backpressure
		service := New[int](
			WithWorkers(1),
			WithQueueSize(1),
			WithBackpressure(BackpressureConfig{
				MaxWait:        10 * time.Millisecond,
				StartThreshold: 0.0, // Always apply backpressure when full
				Strategy:       "linear",
			}),
		)
		defer service.Close()

		// Blocking hook to ensure predictable queue state
		processing := make(chan struct{})
		hook, err := service.Hook("test", func(ctx context.Context, v int) error {
			<-processing
			return nil
		})
		if err != nil {
			t.Fatalf("failed to register hook: %v", err)
		}
		defer hook.Unhook()

		// Fill the single queue slot and ensure worker is blocked
		service.Emit(context.Background(), "test", 1)
		time.Sleep(5 * time.Millisecond) // Ensure worker picks up task and blocks

		// Fill the queue (capacity 1)
		service.Emit(context.Background(), "test", 2)

		// Next emit should trigger backpressure logic
		start := time.Now()
		err = service.Emit(context.Background(), "test", 3)
		elapsed := time.Since(start)

		// Release the blocker
		close(processing)

		// Should have applied some delay and ultimately failed
		if elapsed < 5*time.Millisecond {
			t.Errorf("should have applied some backpressure delay, took %v", elapsed)
		}
		if err != ErrQueueFull {
			t.Errorf("expected queue full error with tiny queue, got %v", err)
		}
	})

	t.Run("OverflowWithZeroCapacity", func(t *testing.T) {
		// Edge case: overflow with zero capacity
		service := New[int](
			WithWorkers(1),
			WithQueueSize(2),
			WithOverflow(OverflowConfig{
				Capacity:         0, // Zero capacity
				DrainInterval:    10 * time.Millisecond,
				EvictionStrategy: "reject",
			}),
		)
		defer service.Close()

		hook, err := service.Hook("test", func(ctx context.Context, v int) error {
			time.Sleep(50 * time.Millisecond) // Longer delay to ensure queue backup
			return nil
		})
		if err != nil {
			t.Fatalf("failed to register hook: %v", err)
		}
		defer hook.Unhook()

		// Fill primary queue: need enough items to trigger overflow
		// With 1 worker + queue size 2, we need at least 4 items to trigger overflow
		for i := 0; i < 4; i++ {
			service.Emit(context.Background(), "test", i)
		}

		// This should trigger overflow and fail immediately with zero capacity
		err = service.Emit(context.Background(), "test", 99)
		if err != ErrQueueFull {
			t.Errorf("zero capacity overflow should reject immediately, got %v", err)
		}
	})

	t.Run("ExpiredContextInOverflow", func(t *testing.T) {
		service := New[int](
			WithWorkers(1),
			WithQueueSize(2),
			WithOverflow(OverflowConfig{
				Capacity:         5,
				DrainInterval:    100 * time.Millisecond, // Long interval for predictable test
				EvictionStrategy: "fifo",
			}),
		)
		defer service.Close()

		var processed int32
		testHook, err := service.Hook("test", func(ctx context.Context, v int) error {
			atomic.AddInt32(&processed, 1)
			return nil
		})
		if err != nil {
			t.Fatalf("failed to register test hook: %v", err)
		}
		defer testHook.Unhook()

		// Block worker processing completely
		blocker := make(chan struct{})
		blockerHook, err := service.Hook("blocker", func(ctx context.Context, v int) error {
			<-blocker // Block indefinitely
			return nil
		})
		if err != nil {
			t.Fatalf("failed to register blocker hook: %v", err)
		}
		defer blockerHook.Unhook()

		// Fill primary queue and worker with blocking task
		service.Emit(context.Background(), "blocker", 0)
		time.Sleep(5 * time.Millisecond) // Ensure worker picks up blocker

		// Fill remaining queue slots
		service.Emit(context.Background(), "blocker", 1) // Queue slot 1
		service.Emit(context.Background(), "blocker", 2) // Queue slot 2

		// Submit task with short context that will expire - goes to overflow
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		err = service.Emit(ctx, "test", 99)
		if err != nil {
			t.Errorf("overflow submission should succeed, got %v", err)
		}

		// Wait for context to expire
		time.Sleep(30 * time.Millisecond)

		// Now trigger a drain cycle by unblocking worker briefly then blocking again
		close(blocker)
		time.Sleep(10 * time.Millisecond) // Let one task process and drain cycle happen

		// The expired task should not have been processed
		processedCount := atomic.LoadInt32(&processed)
		if processedCount != 0 {
			t.Errorf("expired context task should not be processed, got %d", processedCount)
		}
	})
}

// TestBackpressureStrategies validates different backpressure strategy behaviors
func TestBackpressureStrategies(t *testing.T) {
	strategies := []string{"fixed", "linear", "exponential"}

	for _, strategy := range strategies {
		t.Run(strategy, func(t *testing.T) {
			service := New[int](
				WithWorkers(1),
				WithQueueSize(1), // Very small queue
				WithBackpressure(BackpressureConfig{
					MaxWait:        20 * time.Millisecond,
					StartThreshold: 0.0, // Always apply backpressure when full
					Strategy:       strategy,
				}),
			)
			defer service.Close()

			// Blocking hook to ensure queue stays full
			processing := make(chan struct{})
			hook, err := service.Hook("test", func(ctx context.Context, v int) error {
				<-processing // Block until we signal
				return nil
			})
			if err != nil {
				t.Fatalf("failed to register hook: %v", err)
			}
			defer hook.Unhook()

			// Fill primary queue (only 1 slot) and ensure worker is blocked
			service.Emit(context.Background(), "test", 1)
			time.Sleep(5 * time.Millisecond) // Ensure worker picks up the task and blocks

			// Queue is now full (worker blocked) and queue should be empty but worker busy
			// Submit second task to fill queue
			service.Emit(context.Background(), "test", 2)
			time.Sleep(5 * time.Millisecond) // Ensure queue has the task

			// Now queue is definitely full - measure backpressure timing
			start := time.Now()
			err = service.Emit(context.Background(), "test", 99)
			elapsed := time.Since(start)

			// Release the blocking hook to let test complete
			close(processing)

			// Should have applied delay based on strategy
			if elapsed < 15*time.Millisecond {
				t.Errorf("should have applied backpressure delay for strategy %s, took %v", strategy, elapsed)
			}
			if elapsed > 30*time.Millisecond {
				t.Errorf("backpressure delay too long for strategy %s, took %v", strategy, elapsed)
			}

			// Should ultimately fail since queue remained full during backpressure
			if err != ErrQueueFull {
				t.Errorf("expected ErrQueueFull after backpressure for strategy %s, got %v", strategy, err)
			}
		})
	}
}

// TestOverflowEvictionStrategies validates different overflow eviction strategies
func TestOverflowEvictionStrategies(t *testing.T) {
	strategies := map[string]bool{
		"fifo":   true,  // Should not error
		"lifo":   true,  // Should not error
		"reject": false, // Should error when full
	}

	for strategy, shouldSucceed := range strategies {
		t.Run(strategy, func(t *testing.T) {
			service := New[int](
				WithWorkers(1),
				WithQueueSize(2),
				WithOverflow(OverflowConfig{
					Capacity:         5,
					DrainInterval:    100 * time.Millisecond, // Slow drain to fill overflow
					EvictionStrategy: strategy,
				}),
			)
			defer service.Close()

			// Slow processing to fill both queues
			hook, err := service.Hook("test", func(ctx context.Context, v int) error {
				time.Sleep(50 * time.Millisecond)
				return nil
			})
			if err != nil {
				t.Fatalf("failed to register hook: %v", err)
			}
			defer hook.Unhook()

			// Fill primary + overflow + extra
			var lastErr error
			for i := 0; i < 10; i++ {
				err := service.Emit(context.Background(), "test", i)
				if err != nil {
					lastErr = err
				}
			}

			if shouldSucceed && lastErr != nil {
				t.Errorf("strategy %s should not fail, got error: %v", strategy, lastErr)
			}
			if !shouldSucceed && lastErr == nil {
				t.Errorf("strategy %s should fail when overflow full", strategy)
			}
		})
	}
}

package hookz

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerPoolPanicRecovery(t *testing.T) {
	service := New[string]()
	defer service.Close()

	// Hook that panics
	panicDone := make(chan struct{})
	panicHook, err := service.Hook("panic.test", func(ctx context.Context, data string) error {
		close(panicDone)
		panic("test panic")
	})
	if err != nil {
		t.Fatalf("Failed to register panic hook: %v", err)
	}
	defer panicHook.Unhook()

	// Hook that works normally
	normalCalled := make(chan struct{})
	normalHook, err := service.Hook("normal.test", func(ctx context.Context, data string) error {
		close(normalCalled)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register normal hook: %v", err)
	}
	defer normalHook.Unhook()

	// Emit to panicking hook - should not crash service
	if err := service.Emit(context.Background(), "panic.test", "data"); err != nil {
		t.Fatalf("Failed to emit to panic hook: %v", err)
	}

	// Wait for panic to happen
	select {
	case <-panicDone:
		// Panic occurred
	case <-time.After(time.Second):
		t.Fatal("Panic hook was not called")
	}

	// Service should still work for normal hooks
	if err := service.Emit(context.Background(), "normal.test", "data"); err != nil {
		t.Fatalf("Failed to emit to normal hook: %v", err)
	}

	select {
	case <-normalCalled:
		// Good - service still functional after panic
	case <-time.After(time.Second):
		t.Fatal("Normal hook did not execute after panic")
	}
}

func TestWorkerPoolConcurrentOps(t *testing.T) {
	service := New[int]()
	defer service.Close()

	var wg sync.WaitGroup
	const numGoroutines = 5
	const opsPerGoroutine = 10

	// Concurrent hook registration and emission
	wg.Add(numGoroutines * 2)

	// Hook registration goroutines
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			hooks := make([]Hook, 0, opsPerGoroutine)
			eventName := fmt.Sprintf("concurrent-%d", id)
			for j := 0; j < opsPerGoroutine; j++ {
				hook, err := service.Hook(Key(eventName), func(ctx context.Context, data int) error {
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

	// Event emission goroutines
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			eventName := fmt.Sprintf("emit-%d", id)
			for j := 0; j < opsPerGoroutine; j++ {
				service.Emit(context.Background(), Key(eventName), id*opsPerGoroutine+j)
			}
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - no panic or deadlock
	case <-time.After(5 * time.Second):
		t.Fatal("Concurrent operations timeout")
	}
}

func TestWorkerPoolContextCancellation(t *testing.T) {
	service := New[string]()
	defer service.Close()

	started := make(chan struct{})
	canceled := make(chan struct{})

	hook, err := service.Hook("ctx.cancel", func(ctx context.Context, data string) error {
		close(started)
		select {
		case <-ctx.Done():
			close(canceled)
			return ctx.Err()
		case <-time.After(5 * time.Second):
			return errors.New("context not canceled")
		}
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	// Create context and cancel immediately after emission
	ctx, cancel := context.WithCancel(context.Background())

	if err := service.Emit(ctx, "ctx.cancel", "data"); err != nil {
		t.Fatalf("Failed to emit: %v", err)
	}

	// Wait for handler to start
	<-started

	// Cancel context
	cancel()

	// Handler should detect cancellation
	select {
	case <-canceled:
		// Good - context cancellation was respected
	case <-time.After(time.Second):
		t.Fatal("Hook did not respect context cancellation")
	}
}

func TestWorkerPoolShutdown(t *testing.T) {
	service := New[string](WithWorkers(2))

	// Register hook
	processing := make(chan struct{})
	done := make(chan struct{})

	hook, err := service.Hook("shutdown.test", func(ctx context.Context, data string) error {
		close(processing)
		<-done // Wait for signal
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}

	// Emit event
	if err := service.Emit(context.Background(), "shutdown.test", "data"); err != nil {
		t.Fatalf("Failed to emit: %v", err)
	}

	// Wait for processing to start
	<-processing

	// Close in another goroutine (it will block until worker completes)
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- service.Close()
	}()

	// Give close a moment to block
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned too early: %v", err)
	case <-time.After(50 * time.Millisecond):
		// Good - close is blocked waiting for worker
	}

	// Release the worker
	close(done)

	// Now close should complete
	select {
	case err := <-closeDone:
		if err != nil {
			t.Errorf("Close failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not complete after worker released")
	}

	// Hook should still be unhookable after close
	if err := hook.Unhook(); err != nil {
		t.Errorf("Failed to unhook after close: %v", err)
	}
}

func TestWorkerPoolCapacity(t *testing.T) {
	// Create service with minimal workers to test capacity
	service := New[int](WithWorkers(1), WithQueueSize(2))
	defer service.Close()

	// Register slow hook to saturate workers
	blocked := make(chan struct{})
	release := make(chan struct{})

	hook, err := service.Hook("capacity.test", func(ctx context.Context, data int) error {
		if data == 0 {
			close(blocked)
			<-release // Block first task
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	// First task blocks the single worker
	if err := service.Emit(context.Background(), "capacity.test", 0); err != nil {
		t.Fatalf("Failed to emit blocking task: %v", err)
	}

	// Wait for worker to be blocked
	<-blocked

	// Fill the queue (capacity 2)
	successCount := 0
	for i := 1; i <= 3; i++ {
		if err := service.Emit(context.Background(), "capacity.test", i); err == nil {
			successCount++
		}
	}

	// We should have been able to queue exactly 2 more tasks
	if successCount != 2 {
		t.Errorf("Expected to queue 2 tasks, got %d", successCount)
	}

	// Further emissions should fail
	if err := service.Emit(context.Background(), "capacity.test", 99); err != ErrQueueFull {
		t.Errorf("Expected ErrQueueFull when queue is full, got %v", err)
	}

	// Release worker
	close(release)
}

func TestWorkerPoolBackpressure(t *testing.T) {
	service := New[int](
		WithWorkers(1),
		WithQueueSize(2),
		WithBackpressure(BackpressureConfig{
			MaxWait:        50 * time.Millisecond,
			StartThreshold: 0.5, // Start backpressure at 50% full
			Strategy:       "linear",
		}),
	)
	defer service.Close()

	// Block the worker
	blocked := make(chan struct{})
	release := make(chan struct{})

	hook, err := service.Hook("backpressure.test", func(ctx context.Context, v int) error {
		if v == 0 {
			close(blocked)
			<-release
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	// First task blocks the worker
	if err := service.Emit(context.Background(), "backpressure.test", 0); err != nil {
		t.Fatalf("Failed to emit blocking task: %v", err)
	}

	<-blocked // Wait for worker to be blocked

	// Fill queue to 50% (1 of 2)
	if err := service.Emit(context.Background(), "backpressure.test", 1); err != nil {
		t.Fatalf("Failed to emit to queue: %v", err)
	}

	// Next emission should experience backpressure delay
	start := time.Now()
	err = service.Emit(context.Background(), "backpressure.test", 2)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Emission failed: %v", err)
	}

	// Should have experienced some delay (but not the full max wait)
	if elapsed < 10*time.Millisecond {
		t.Logf("Warning: Expected backpressure delay, got %v", elapsed)
	}

	close(release)
}

func TestWorkerPoolOverflow(t *testing.T) {
	service := New[int](
		WithWorkers(1),
		WithQueueSize(2),
		WithOverflow(OverflowConfig{
			Capacity:         5,
			DrainInterval:    10 * time.Millisecond,
			EvictionStrategy: "fifo",
		}),
	)
	defer service.Close()

	var processed int32
	done := make(chan struct{})

	hook, err := service.Hook("overflow.test", func(ctx context.Context, v int) error {
		count := atomic.AddInt32(&processed, 1)
		if count >= 7 { // We'll emit 8 total
			close(done)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	// Emit burst - should go to primary queue + overflow
	// Primary: 2, Overflow: 5, Total capacity: 7
	for i := 0; i < 8; i++ {
		err := service.Emit(context.Background(), "overflow.test", i)
		if i < 7 && err != nil {
			t.Errorf("Failed to emit task %d: %v", i, err)
		}
		if i == 7 && err == nil {
			// 8th should either succeed (if drain happened) or fail
			t.Logf("8th emission succeeded (drain may have occurred)")
		}
	}

	// Wait for processing
	select {
	case <-done:
		if p := atomic.LoadInt32(&processed); p < 7 {
			t.Errorf("Expected at least 7 processed, got %d", p)
		}
	case <-time.After(2 * time.Second):
		t.Errorf("Timeout: only processed %d tasks", atomic.LoadInt32(&processed))
	}
}

func TestWorkerPoolErrorHandling(t *testing.T) {
	service := New[string]()
	defer service.Close()

	errorOccurred := make(chan struct{})
	successOccurred := make(chan struct{})

	// Hook that returns error
	errorHook, err := service.Hook("error.test", func(ctx context.Context, data string) error {
		close(errorOccurred)
		return errors.New("test error")
	})
	if err != nil {
		t.Fatalf("Failed to register error hook: %v", err)
	}
	defer errorHook.Unhook()

	// Hook that succeeds
	successHook, err := service.Hook("success.test", func(ctx context.Context, data string) error {
		close(successOccurred)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register success hook: %v", err)
	}
	defer successHook.Unhook()

	// Emit to error hook
	if err := service.Emit(context.Background(), "error.test", "data"); err != nil {
		t.Fatalf("Failed to emit to error hook: %v", err)
	}

	// Emit to success hook
	if err := service.Emit(context.Background(), "success.test", "data"); err != nil {
		t.Fatalf("Failed to emit to success hook: %v", err)
	}

	// Both should complete despite error
	select {
	case <-errorOccurred:
		// Good
	case <-time.After(time.Second):
		t.Fatal("Error hook not called")
	}

	select {
	case <-successOccurred:
		// Good
	case <-time.After(time.Second):
		t.Fatal("Success hook not called")
	}
}

func TestWorkerPoolMultipleHooksPerEvent(t *testing.T) {
	service := New[int](WithWorkers(5))
	defer service.Close()

	const numHooks = 5
	var callCount int32
	done := make(chan struct{})

	// Register multiple hooks for same event
	var hooks []Hook
	for i := 0; i < numHooks; i++ {
		hook, err := service.Hook("multi.hook", func(ctx context.Context, data int) error {
			if atomic.AddInt32(&callCount, 1) == int32(numHooks) {
				close(done)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("Failed to register hook %d: %v", i, err)
		}
		hooks = append(hooks, hook)
	}

	// Cleanup
	defer func() {
		for _, h := range hooks {
			h.Unhook()
		}
	}()

	// Single emission should trigger all hooks
	if err := service.Emit(context.Background(), "multi.hook", 42); err != nil {
		t.Fatalf("Failed to emit: %v", err)
	}

	// All hooks should be called
	select {
	case <-done:
		if c := atomic.LoadInt32(&callCount); c != numHooks {
			t.Errorf("Expected %d calls, got %d", numHooks, c)
		}
	case <-time.After(time.Second):
		t.Fatalf("Timeout: only %d/%d hooks called", atomic.LoadInt32(&callCount), numHooks)
	}
}

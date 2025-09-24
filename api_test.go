package hookz

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBasicHookRegistration(t *testing.T) {
	service := New[string]()
	defer service.Close()

	done := make(chan struct{})
	var received string

	hook, err := service.Hook("test.event", func(ctx context.Context, data string) error {
		received = data
		close(done)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	if err := service.Emit(context.Background(), "test.event", "test-data"); err != nil {
		t.Fatalf("Failed to emit event: %v", err)
	}

	select {
	case <-done:
		if received != "test-data" {
			t.Errorf("Expected 'test-data', got '%s'", received)
		}
	case <-time.After(time.Second):
		t.Fatal("Hook was not called within timeout")
	}
}

func TestMultipleHooksForSameEvent(t *testing.T) {
	service := New[int]()
	defer service.Close()

	var count int32
	var wg sync.WaitGroup

	for i := 0; i < 3; i++ {
		wg.Add(1)
		hook, err := service.Hook("multi.event", func(ctx context.Context, data int) error {
			atomic.AddInt32(&count, 1)
			wg.Done()
			return nil
		})
		if err != nil {
			t.Fatalf("Failed to register hook %d: %v", i, err)
		}
		defer hook.Unhook()
	}

	if err := service.Emit(context.Background(), "multi.event", 42); err != nil {
		t.Fatalf("Failed to emit event: %v", err)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		if c := atomic.LoadInt32(&count); c != 3 {
			t.Errorf("Expected 3 hooks to be called, got %d", c)
		}
	case <-time.After(time.Second):
		t.Fatal("Not all hooks were called within timeout")
	}
}

func TestEmitWithNoHooks(t *testing.T) {
	service := New[string]()
	defer service.Close()

	// Should not error when emitting to event with no hooks
	if err := service.Emit(context.Background(), "no.hooks", "data"); err != nil {
		t.Errorf("Emit with no hooks should not error: %v", err)
	}
}

func TestServiceClose(t *testing.T) {
	service := New[string]()

	if err := service.Close(); err != nil {
		t.Fatalf("Failed to close service: %v", err)
	}

	// Double close should return error
	if err := service.Close(); err == nil {
		t.Error("Expected error on double close")
	}

	// Operations after close should fail
	_, err := service.Hook("test", func(ctx context.Context, data string) error {
		return nil
	})
	if err == nil {
		t.Error("Hook registration should fail after close")
	}

	if err := service.Emit(context.Background(), "test", "data"); err == nil {
		t.Error("Emit should fail after close")
	}
}

func TestWithOptions(t *testing.T) {
	t.Run("WithWorkers", func(t *testing.T) {
		service := New[string](WithWorkers(20))
		defer service.Close()

		// Service should function normally
		done := make(chan struct{})
		hook, err := service.Hook("test", func(ctx context.Context, data string) error {
			close(done)
			return nil
		})
		if err != nil {
			t.Fatalf("Failed to register hook: %v", err)
		}
		defer hook.Unhook()

		if err := service.Emit(context.Background(), "test", "data"); err != nil {
			t.Fatalf("Failed to emit: %v", err)
		}

		select {
		case <-done:
			// Success
		case <-time.After(time.Second):
			t.Fatal("Hook not called")
		}
	})

	t.Run("WithTimeout", func(t *testing.T) {
		timeout := 100 * time.Millisecond
		service := New[string](WithTimeout(timeout))
		defer service.Close()

		if service.GlobalTimeout != timeout {
			t.Errorf("Expected timeout %v, got %v", timeout, service.GlobalTimeout)
		}
	})

	t.Run("WithQueueSize", func(t *testing.T) {
		service := New[string](WithQueueSize(100))
		defer service.Close()

		// Register a hook to initialize worker pool
		hook, err := service.Hook("test", func(ctx context.Context, data string) error {
			return nil
		})
		if err != nil {
			t.Fatalf("Failed to register hook: %v", err)
		}
		defer hook.Unhook()

		// Service should function with custom queue size
		if err := service.Emit(context.Background(), "test", "data"); err != nil {
			t.Fatalf("Failed to emit: %v", err)
		}
	})

	t.Run("CombinedOptions", func(t *testing.T) {
		service := New[string](
			WithWorkers(15),
			WithTimeout(2*time.Second),
			WithQueueSize(50),
		)
		defer service.Close()

		if service.GlobalTimeout != 2*time.Second {
			t.Errorf("Expected timeout 2s, got %v", service.GlobalTimeout)
		}
	})
}

func TestContextCancellation(t *testing.T) {
	service := New[string]()
	defer service.Close()

	started := make(chan struct{})
	finished := make(chan error, 1)

	hook, err := service.Hook("ctx.test", func(ctx context.Context, data string) error {
		close(started)
		select {
		case <-ctx.Done():
			finished <- ctx.Err()
			return ctx.Err()
		case <-time.After(5 * time.Second):
			finished <- nil
			return nil
		}
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	ctx, cancel := context.WithCancel(context.Background())

	if err := service.Emit(ctx, "ctx.test", "data"); err != nil {
		t.Fatalf("Failed to emit: %v", err)
	}

	// Wait for handler to start
	<-started

	// Cancel the context
	cancel()

	// Handler should receive cancellation
	select {
	case err := <-finished:
		if err != context.Canceled {
			t.Errorf("Expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Handler did not respond to cancellation")
	}
}

func TestConcurrentOperations(t *testing.T) {
	service := New[int]()
	defer service.Close()

	const numGoroutines = 10
	const opsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2)

	// Concurrent hook registration/unregistration
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				hook, err := service.Hook(Key("concurrent.test"), func(ctx context.Context, data int) error {
					return nil
				})
				if err == nil {
					hook.Unhook()
				}
			}
		}(i)
	}

	// Concurrent emissions
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				service.Emit(context.Background(), Key("concurrent.test"), id*opsPerGoroutine+j)
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
		// Success - no race conditions
	case <-time.After(5 * time.Second):
		t.Fatal("Concurrent operations timeout")
	}
}

func TestKeyTypeUsage(t *testing.T) {
	service := New[string]()
	defer service.Close()

	// Test with Key constants
	const testEvent Key = "test.keyed.event"

	called := make(chan struct{})
	hook, err := service.Hook(testEvent, func(ctx context.Context, data string) error {
		close(called)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	if err := service.Emit(context.Background(), testEvent, "data"); err != nil {
		t.Fatalf("Failed to emit: %v", err)
	}

	select {
	case <-called:
		// Success
	case <-time.After(time.Second):
		t.Fatal("Hook not called")
	}
}

func TestUnhook(t *testing.T) {
	service := New[string]()
	defer service.Close()

	called := make(chan struct{}, 1)
	hook, err := service.Hook("unhook.test", func(ctx context.Context, data string) error {
		called <- struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}

	// Unhook before emitting
	if err := hook.Unhook(); err != nil {
		t.Fatalf("Failed to unhook: %v", err)
	}

	// Double unhook should return error
	if err := hook.Unhook(); err == nil {
		t.Error("Expected error on double unhook")
	}

	// Emit should not call the unhooked handler
	if err := service.Emit(context.Background(), "unhook.test", "data"); err != nil {
		t.Fatalf("Failed to emit: %v", err)
	}

	select {
	case <-called:
		t.Error("Unhooked handler should not be called")
	case <-time.After(100 * time.Millisecond):
		// Success - handler not called
	}
}

func TestClearEvent(t *testing.T) {
	service := New[int]()
	defer service.Close()

	// Register multiple hooks for same event
	hooks := make([]Hook, 3)
	for i := 0; i < 3; i++ {
		hook, err := service.Hook("clear.test", func(ctx context.Context, data int) error {
			return nil
		})
		if err != nil {
			t.Fatalf("Failed to register hook %d: %v", i, err)
		}
		hooks[i] = hook
	}

	// Clear the event
	cleared := service.Clear("clear.test")
	if cleared != 3 {
		t.Errorf("Expected to clear 3 hooks, cleared %d", cleared)
	}

	// Hooks should now be invalid
	for i, hook := range hooks {
		if err := hook.Unhook(); err == nil {
			t.Errorf("Hook %d should be invalid after clear", i)
		}
	}

	// Clear non-existent event
	cleared = service.Clear("nonexistent")
	if cleared != 0 {
		t.Errorf("Expected 0 cleared for non-existent event, got %d", cleared)
	}
}

func TestClearAll(t *testing.T) {
	service := New[string]()
	defer service.Close()

	// Register hooks for different events
	var hooks []Hook
	events := []string{"event1", "event2", "event3"}

	for _, event := range events {
		hook, err := service.Hook(Key(event), func(ctx context.Context, data string) error {
			return nil
		})
		if err != nil {
			t.Fatalf("Failed to register hook for %s: %v", event, err)
		}
		hooks = append(hooks, hook)
	}

	// Clear all
	cleared := service.ClearAll()
	if cleared != len(events) {
		t.Errorf("Expected to clear %d hooks, cleared %d", len(events), cleared)
	}

	// All hooks should be invalid
	for i, hook := range hooks {
		if err := hook.Unhook(); err == nil {
			t.Errorf("Hook %d should be invalid after clear all", i)
		}
	}

	// Second clear should return 0
	cleared = service.ClearAll()
	if cleared != 0 {
		t.Errorf("Expected 0 on second clear all, got %d", cleared)
	}
}

func TestZeroTimeout(t *testing.T) {
	service := New[string](WithTimeout(0))
	defer service.Close()

	completed := make(chan struct{})
	hook, err := service.Hook("zero.timeout", func(ctx context.Context, data string) error {
		// Simulate work
		time.Sleep(10 * time.Millisecond)
		close(completed)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	if err := service.Emit(context.Background(), "zero.timeout", "data"); err != nil {
		t.Fatalf("Failed to emit: %v", err)
	}

	select {
	case <-completed:
		// Success - no timeout
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Hook should have completed without timeout")
	}
}

package hookz

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestErrorQueueFull(t *testing.T) {
	// Create service with minimal capacity
	service := New[int](WithWorkers(1), WithQueueSize(1))
	defer service.Close()

	// Block the worker
	blocked := make(chan struct{})
	release := make(chan struct{})

	hook, err := service.Hook("queue.full", func(ctx context.Context, n int) error {
		if n == 0 {
			close(blocked)
			<-release
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	// First emission blocks worker
	if err := service.Emit(context.Background(), "queue.full", 0); err != nil {
		t.Fatalf("Failed to emit blocking task: %v", err)
	}

	<-blocked // Wait for worker to block

	// Second emission fills queue
	if err := service.Emit(context.Background(), "queue.full", 1); err != nil {
		t.Fatalf("Failed to emit to queue: %v", err)
	}

	// Third emission should fail with queue full
	err = service.Emit(context.Background(), "queue.full", 2)
	if !errors.Is(err, ErrQueueFull) {
		t.Errorf("Expected ErrQueueFull, got %v", err)
	}

	close(release)
}

func TestErrorServiceClosed(t *testing.T) {
	service := New[string]()

	// Close the service
	if err := service.Close(); err != nil {
		t.Fatalf("Failed to close service: %v", err)
	}

	// All operations should return ErrServiceClosed
	t.Run("Hook", func(t *testing.T) {
		_, err := service.Hook("test", func(ctx context.Context, data string) error { return nil })
		if !errors.Is(err, ErrServiceClosed) {
			t.Errorf("Expected ErrServiceClosed, got %v", err)
		}
	})

	t.Run("Emit", func(t *testing.T) {
		err := service.Emit(context.Background(), "test", "data")
		if !errors.Is(err, ErrServiceClosed) {
			t.Errorf("Expected ErrServiceClosed, got %v", err)
		}
	})

	t.Run("DoubleClose", func(t *testing.T) {
		err := service.Close()
		if !errors.Is(err, ErrAlreadyClosed) {
			t.Errorf("Expected ErrAlreadyClosed, got %v", err)
		}
	})
}

func TestErrorHookNotFound(t *testing.T) {
	service := New[string]()
	defer service.Close()

	// Register and clear immediately
	hook, err := service.Hook("test", func(ctx context.Context, data string) error { return nil })
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}

	// Clear the event
	cleared := service.Clear("test")
	if cleared != 1 {
		t.Errorf("Expected to clear 1 hook, got %d", cleared)
	}

	// Unhook should now fail with not found
	err = hook.Unhook()
	if !errors.Is(err, ErrHookNotFound) {
		t.Errorf("Expected ErrHookNotFound, got %v", err)
	}
}

func TestErrorAlreadyUnhooked(t *testing.T) {
	service := New[string]()
	defer service.Close()

	hook, err := service.Hook("test", func(ctx context.Context, data string) error { return nil })
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}

	// First unhook succeeds
	if err := hook.Unhook(); err != nil {
		t.Fatalf("Failed to unhook: %v", err)
	}

	// Second unhook should fail
	err = hook.Unhook()
	if !errors.Is(err, ErrAlreadyUnhooked) {
		t.Errorf("Expected ErrAlreadyUnhooked, got %v", err)
	}

	// Further unhooks should also fail
	err = hook.Unhook()
	if !errors.Is(err, ErrAlreadyUnhooked) {
		t.Errorf("Expected ErrAlreadyUnhooked on third unhook, got %v", err)
	}
}

func TestErrorTooManyHooks(t *testing.T) {
	service := New[string]()
	defer service.Close()

	// Try to exceed per-event limit
	hooks := make([]Hook, 0, maxHooksPerEvent+10)

	for i := 0; i < maxHooksPerEvent+10; i++ {
		hook, err := service.Hook("max.test", func(ctx context.Context, s string) error {
			return nil
		})

		if i < maxHooksPerEvent {
			if err != nil {
				t.Errorf("Hook %d should succeed: %v", i, err)
			}
			hooks = append(hooks, hook)
		} else {
			if !errors.Is(err, ErrTooManyHooks) {
				t.Errorf("Hook %d: expected ErrTooManyHooks, got %v", i, err)
			}
			if hook.unhook != nil {
				t.Error("Failed hook should have nil unhook function")
			}
		}
	}

	// Cleanup
	for _, h := range hooks {
		h.Unhook()
	}
}

func TestErrorHandlerFailure(t *testing.T) {
	service := New[string]()
	defer service.Close()

	handlerErr := errors.New("handler error")
	called := make(chan struct{})

	hook, err := service.Hook("error.handler", func(ctx context.Context, data string) error {
		close(called)
		return handlerErr
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	// Emit should succeed even if handler fails
	if err := service.Emit(context.Background(), "error.handler", "data"); err != nil {
		t.Errorf("Emit should not fail when handler errors: %v", err)
	}

	// Handler should still be called
	select {
	case <-called:
		// Good
	case <-time.After(time.Second):
		t.Fatal("Handler not called")
	}
}

func TestErrorContextExpired(t *testing.T) {
	service := New[string]()
	defer service.Close()

	called := make(chan error, 1)
	hook, err := service.Hook("ctx.expired", func(ctx context.Context, data string) error {
		err := ctx.Err()
		called <- err
		return err
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	// Create already-canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Emit with canceled context
	if err := service.Emit(ctx, "ctx.expired", "data"); err != nil {
		t.Errorf("Emit should succeed even with canceled context: %v", err)
	}

	// Handler should receive the canceled context
	select {
	case err := <-called:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Handler not called with canceled context")
	}
}

func TestErrorConcurrentClose(t *testing.T) {
	service := New[string]()

	// Register some hooks
	for i := 0; i < 10; i++ {
		_, err := service.Hook("concurrent.close", func(ctx context.Context, data string) error {
			time.Sleep(time.Millisecond) // Simulate work
			return nil
		})
		if err != nil {
			t.Fatalf("Failed to register hook %d: %v", i, err)
		}
	}

	// Try to close from multiple goroutines
	var wg sync.WaitGroup
	closeErrors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			closeErrors <- service.Close()
		}()
	}

	wg.Wait()
	close(closeErrors)

	// Exactly one should succeed, others should get ErrAlreadyClosed
	successCount := 0
	alreadyClosedCount := 0

	for err := range closeErrors {
		if err == nil {
			successCount++
		} else if errors.Is(err, ErrAlreadyClosed) {
			alreadyClosedCount++
		} else {
			t.Errorf("Unexpected error: %v", err)
		}
	}

	if successCount != 1 {
		t.Errorf("Expected exactly 1 successful close, got %d", successCount)
	}

	if alreadyClosedCount != 9 {
		t.Errorf("Expected 9 ErrAlreadyClosed, got %d", alreadyClosedCount)
	}
}

func TestErrorRaceConditions(t *testing.T) {
	service := New[int]()
	defer service.Close()

	const concurrency = 20
	const operations = 50

	var wg sync.WaitGroup
	wg.Add(concurrency * 3)

	// Concurrent hooks
	for i := 0; i < concurrency; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < operations; j++ {
				hook, err := service.Hook("race.test", func(ctx context.Context, data int) error {
					return nil
				})
				if err == nil {
					hook.Unhook()
				}
			}
		}(i)
	}

	// Concurrent emits
	for i := 0; i < concurrency; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < operations; j++ {
				service.Emit(context.Background(), "race.test", id*operations+j)
			}
		}(i)
	}

	// Concurrent clears
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < operations; j++ {
				service.Clear("race.test")
				time.Sleep(time.Microsecond) // Small delay to vary timing
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
		t.Fatal("Race condition test timeout")
	}
}

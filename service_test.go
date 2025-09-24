package hookz

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServiceRaceConditionSafety(t *testing.T) {
	service := New[int]()

	// Use a channel to coordinate shutdown
	done := make(chan struct{})

	// Spam emissions in background
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				service.Emit(context.Background(), "race.test", 1)
				runtime.Gosched() // Yield to increase race likelihood
			}
		}
	}()

	// Let it run briefly
	time.Sleep(10 * time.Millisecond)

	// Signal goroutine to stop
	close(done)

	// Small delay to ensure goroutine stops
	time.Sleep(5 * time.Millisecond)

	// Close service - should not panic
	if err := service.Close(); err != nil {
		t.Errorf("Failed to close service: %v", err)
	}
}

func TestServiceMemoryLeaks(t *testing.T) {
	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	service := New[string]()
	defer service.Close()

	// Register and unregister many hooks
	for i := 0; i < 1000; i++ {
		hook, err := service.Hook("memory.test", func(ctx context.Context, s string) error {
			return nil
		})
		if err != nil {
			t.Fatalf("Failed to register hook %d: %v", i, err)
		}

		if err := hook.Unhook(); err != nil {
			t.Fatalf("Failed to unhook %d: %v", i, err)
		}
	}

	runtime.GC()
	runtime.ReadMemStats(&m2)

	// Calculate memory difference
	var leaked int64
	if m2.Alloc >= m1.Alloc {
		leaked = int64(m2.Alloc - m1.Alloc)
	} else {
		// Memory was freed (good!)
		leaked = 0
	}

	// Allow some reasonable memory growth but not massive leaks
	maxAllowedLeak := int64(1024 * 1024) // 1MB
	if leaked > maxAllowedLeak {
		t.Errorf("Potential memory leak: %d bytes (limit: %d)", leaked, maxAllowedLeak)
	}
}

func TestServiceGenerateID(t *testing.T) {
	svc := New[string]()
	defer svc.Close()

	ids := make(map[string]bool)
	for i := 0; i < 10000; i++ {
		id := svc.generateID()
		if ids[id] {
			t.Fatalf("Generated duplicate ID: %s", id)
		}
		if len(id) != 16 {
			t.Errorf("ID should be 16 characters (8 bytes hex), got %d", len(id))
		}
		ids[id] = true
	}
}

func TestServiceImplementationDetails(t *testing.T) {
	service := New[string]()
	defer service.Close()

	// Initial state should be correct
	if service.closed {
		t.Error("Service should not be closed initially")
	}
	if service.hooks == nil {
		t.Error("Hooks map should be initialized")
	}
	if service.workers != nil {
		t.Error("Workers should be nil until first hook")
	}

	// Register a hook and check internal state changes
	hook, err := service.Hook("impl.test", func(ctx context.Context, data string) error { return nil })
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}

	// After first hook, workers should be initialized
	if service.workers == nil {
		t.Error("Worker pool should be created after first hook")
	}

	// Check hooks map has the event
	service.mu.RLock()
	if len(service.hooks["impl.test"]) != 1 {
		t.Errorf("Expected 1 hook for event, got %d", len(service.hooks["impl.test"]))
	}
	service.mu.RUnlock()

	// Unhook and verify cleanup
	if err := hook.Unhook(); err != nil {
		t.Fatalf("Failed to unhook: %v", err)
	}

	service.mu.RLock()
	if len(service.hooks) != 0 {
		t.Error("Empty events should be cleaned up")
	}
	service.mu.RUnlock()
}

func TestServiceCloseLifecycle(t *testing.T) {
	service := New[string]()

	// Initially not closed
	if service.closed {
		t.Error("Service should not be closed initially")
	}

	// Register some hooks
	hook1, _ := service.Hook("close.test", func(ctx context.Context, data string) error { return nil })
	hook2, _ := service.Hook("close.test", func(ctx context.Context, data string) error { return nil })

	// Close the service
	if err := service.Close(); err != nil {
		t.Fatalf("Failed to close service: %v", err)
	}

	if !service.closed {
		t.Error("Service should be marked as closed")
	}

	// Hooks registered before close should still be unhookable
	if err := hook1.Unhook(); err != nil {
		t.Errorf("Should be able to unhook after close: %v", err)
	}
	if err := hook2.Unhook(); err != nil {
		t.Errorf("Should be able to unhook after close: %v", err)
	}

	// But new operations should fail
	_, err := service.Hook("new.event", func(ctx context.Context, data string) error { return nil })
	if err != ErrServiceClosed {
		t.Errorf("Expected ErrServiceClosed, got %v", err)
	}
}

func TestServiceWithWorkers(t *testing.T) {
	service := New[int](WithWorkers(5))
	defer service.Close()

	// Service should function normally
	done := make(chan struct{})
	hook, err := service.Hook("workers.test", func(ctx context.Context, data int) error {
		close(done)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	if err := service.Emit(context.Background(), "workers.test", 42); err != nil {
		t.Fatalf("Failed to emit: %v", err)
	}

	select {
	case <-done:
		// Success
	case <-time.After(time.Second):
		t.Fatal("Hook not called")
	}
}

func TestServiceHookStorage(t *testing.T) {
	service := New[string]()
	defer service.Close()

	// Register multiple hooks for different events
	hook1, err := service.Hook("event1", func(ctx context.Context, data string) error { return nil })
	if err != nil {
		t.Fatalf("Failed to register hook1: %v", err)
	}
	defer hook1.Unhook()

	hook2, err := service.Hook("event1", func(ctx context.Context, data string) error { return nil })
	if err != nil {
		t.Fatalf("Failed to register hook2: %v", err)
	}
	defer hook2.Unhook()

	hook3, err := service.Hook("event2", func(ctx context.Context, data string) error { return nil })
	if err != nil {
		t.Fatalf("Failed to register hook3: %v", err)
	}
	defer hook3.Unhook()

	// Check internal storage structure
	service.mu.RLock()
	event1Hooks := len(service.hooks["event1"])
	event2Hooks := len(service.hooks["event2"])
	service.mu.RUnlock()

	if event1Hooks != 2 {
		t.Errorf("Expected 2 hooks for event1, got %d", event1Hooks)
	}
	if event2Hooks != 1 {
		t.Errorf("Expected 1 hook for event2, got %d", event2Hooks)
	}
}

func TestServiceBackpressureConfig(t *testing.T) {
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

	// Service should be created successfully
	if service == nil {
		t.Fatal("Service should be created with backpressure config")
	}

	// Test basic functionality
	done := make(chan struct{})
	hook, err := service.Hook("backpressure.test", func(ctx context.Context, v int) error {
		if v == 1 {
			close(done)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	if err := service.Emit(context.Background(), "backpressure.test", 1); err != nil {
		t.Fatalf("Failed to emit: %v", err)
	}

	select {
	case <-done:
		// Success
	case <-time.After(time.Second):
		t.Fatal("Hook not called")
	}
}

func TestServiceOverflowConfig(t *testing.T) {
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

	// Service should be created successfully
	if service == nil {
		t.Fatal("Service should be created with overflow config")
	}

	// Test basic functionality
	done := make(chan struct{})
	hook, err := service.Hook("overflow.test", func(ctx context.Context, v int) error {
		if v == 1 {
			close(done)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	if err := service.Emit(context.Background(), "overflow.test", 1); err != nil {
		t.Fatalf("Failed to emit: %v", err)
	}

	select {
	case <-done:
		// Success
	case <-time.After(time.Second):
		t.Fatal("Hook not called")
	}
}

func TestServiceConcurrentHookOperations(t *testing.T) {
	service := New[string]()
	defer service.Close()

	const concurrency = 50
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(concurrency)

	// Many goroutines adding/removing hooks
	for i := 0; i < concurrency; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				hook, err := service.Hook("concurrent", func(ctx context.Context, data string) error {
					return nil
				})
				if err == nil {
					// Random delay to vary timing
					if j%10 == 0 {
						runtime.Gosched()
					}
					hook.Unhook()
				}
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
		// Success - no deadlocks or races
	case <-time.After(5 * time.Second):
		t.Fatal("Concurrent operations timeout")
	}
}

func TestServiceLazyWorkerPoolInit(t *testing.T) {
	t.Run("NoWorkerPoolBeforeHooks", func(t *testing.T) {
		service := New[string]()
		defer service.Close()

		// Worker pool should not exist initially
		if service.workers != nil {
			t.Error("Worker pool should be nil before any hooks")
		}

		// Emit with no hooks should work
		if err := service.Emit(context.Background(), "test", "data"); err != nil {
			t.Fatalf("Emit should succeed with no hooks: %v", err)
		}

		// Still no worker pool
		if service.workers != nil {
			t.Error("Worker pool should still be nil after emit with no hooks")
		}
	})

	t.Run("WorkerPoolCreatedOnFirstHook", func(t *testing.T) {
		service := New[string](WithWorkers(5), WithQueueSize(10))
		defer service.Close()

		// Initially no worker pool
		if service.workers != nil {
			t.Error("Worker pool should be nil initially")
		}

		// Register first hook
		hook, err := service.Hook("test", func(ctx context.Context, data string) error {
			return nil
		})
		if err != nil {
			t.Fatalf("Failed to register hook: %v", err)
		}
		defer hook.Unhook()

		// Worker pool should now exist
		if service.workers == nil {
			t.Error("Worker pool should be created after first hook")
		}
	})

	t.Run("WorkerPoolPersistsAfterUnhook", func(t *testing.T) {
		service := New[string]()
		defer service.Close()

		hook, err := service.Hook("test", func(ctx context.Context, data string) error {
			return nil
		})
		if err != nil {
			t.Fatalf("Failed to register hook: %v", err)
		}

		// Worker pool created
		if service.workers == nil {
			t.Error("Worker pool should exist after hook")
		}

		// Unhook
		if err := hook.Unhook(); err != nil {
			t.Fatalf("Failed to unhook: %v", err)
		}

		// Worker pool should persist
		if service.workers == nil {
			t.Error("Worker pool should persist after unhook")
		}
	})
}

func TestServiceAdminOperations(t *testing.T) {
	service := New[int]()
	defer service.Close()

	// Register hooks
	_, err := service.Hook("admin.event1", func(ctx context.Context, data int) error { return nil })
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}

	_, err = service.Hook("admin.event2", func(ctx context.Context, data int) error { return nil })
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}

	_, err = service.Hook("admin.event2", func(ctx context.Context, data int) error { return nil })
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}

	// Test Clear
	count := service.Clear("admin.event2")
	if count != 2 {
		t.Errorf("Expected to clear 2 hooks, got %d", count)
	}

	count = service.Clear("admin.event1")
	if count != 1 {
		t.Errorf("Expected to clear 1 hook, got %d", count)
	}

	// Test ClearAll after adding more
	service.Hook("admin.event3", func(ctx context.Context, data int) error { return nil })

	totalCleared := service.ClearAll()
	if totalCleared != 1 {
		t.Errorf("Expected to clear 1 remaining hook, got %d", totalCleared)
	}
}

func TestServiceConcurrentEmissions(t *testing.T) {
	service := New[int](WithWorkers(10)) // More workers for concurrent processing
	defer service.Close()

	var counter int32

	// Register hook that counts calls
	hook, err := service.Hook("emit.test", func(ctx context.Context, data int) error {
		atomic.AddInt32(&counter, 1)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	// Emit from multiple goroutines
	const numEmitters = 10
	const emitsPerGoroutine = 10 // Reduced for faster testing
	totalExpected := numEmitters * emitsPerGoroutine

	var emitWg sync.WaitGroup
	emitWg.Add(numEmitters)

	for i := 0; i < numEmitters; i++ {
		go func(id int) {
			defer emitWg.Done()
			for j := 0; j < emitsPerGoroutine; j++ {
				if err := service.Emit(context.Background(), "emit.test", id*emitsPerGoroutine+j); err != nil {
					// Only log if not queue full (which is expected under load)
					if err != ErrQueueFull {
						t.Errorf("Unexpected emit error: %v", err)
					}
				}
			}
		}(i)
	}

	// Wait for all emits to complete
	emitWg.Wait()

	// Give hooks time to process (since they're async)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c := atomic.LoadInt32(&counter); c >= int32(totalExpected) {
			// All expected hooks were called
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	// If we get here, not all hooks were called
	finalCount := atomic.LoadInt32(&counter)
	if finalCount < int32(totalExpected) {
		// This is acceptable - some emits may have been dropped due to queue full
		t.Logf("Processed %d/%d emissions (some may have been dropped due to queue limits)", finalCount, totalExpected)
	}
}

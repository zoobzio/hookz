package hookz

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// TestNoopBehaviorWithoutListeners verifies the service has minimal footprint when no hooks are registered
func TestNoopBehaviorWithoutListeners(t *testing.T) {
	t.Run("NoResourcesAllocated", func(t *testing.T) {
		service := New[string]()
		defer service.Close()

		// Check that no worker pool is created
		if service.workers != nil {
			t.Error("Worker pool should not be initialized without hooks")
		}

		// Check that metrics are not initialized
		if service.metrics != nil {
			t.Error("Metrics should not be initialized without hooks")
		}

		// Verify totalHooks is zero
		if service.totalHooks != 0 {
			t.Error("Total hooks should be zero")
		}
	})

	t.Run("EmitIsNoop", func(t *testing.T) {
		service := New[string]()
		defer service.Close()

		// Track goroutine count before emit
		initialGoroutines := runtime.NumGoroutine()

		// Emit should be essentially a no-op
		err := service.Emit(context.Background(), "test.event", "data")
		if err != nil {
			t.Fatalf("Emit should not error with no hooks: %v", err)
		}

		// Small delay to let any goroutines start (there shouldn't be any)
		time.Sleep(10 * time.Millisecond)

		// Verify no new goroutines were created
		finalGoroutines := runtime.NumGoroutine()
		if finalGoroutines > initialGoroutines {
			t.Errorf("Emit created goroutines when no hooks registered: before=%d, after=%d",
				initialGoroutines, finalGoroutines)
		}
	})

	t.Run("MetricsReturnsZeroValues", func(t *testing.T) {
		service := New[string]()
		defer service.Close()

		metrics := service.Metrics()

		// All metrics should be zero
		if metrics.QueueDepth != 0 {
			t.Errorf("QueueDepth should be 0, got %d", metrics.QueueDepth)
		}
		if metrics.QueueCapacity != 0 {
			t.Errorf("QueueCapacity should be 0, got %d", metrics.QueueCapacity)
		}
		if metrics.TasksProcessed != 0 {
			t.Errorf("TasksProcessed should be 0, got %d", metrics.TasksProcessed)
		}
		if metrics.TasksRejected != 0 {
			t.Errorf("TasksRejected should be 0, got %d", metrics.TasksRejected)
		}
		if metrics.TasksFailed != 0 {
			t.Errorf("TasksFailed should be 0, got %d", metrics.TasksFailed)
		}
		if metrics.TasksExpired != 0 {
			t.Errorf("TasksExpired should be 0, got %d", metrics.TasksExpired)
		}
		if metrics.RegisteredHooks != 0 {
			t.Errorf("RegisteredHooks should be 0, got %d", metrics.RegisteredHooks)
		}
	})

	t.Run("CloseIsNoop", func(t *testing.T) {
		service := New[string]()

		// Close should complete instantly with no resources to clean up
		start := time.Now()
		err := service.Close()
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("Close should not error with no resources: %v", err)
		}

		// Close should be essentially instant (< 1ms)
		if elapsed > time.Millisecond {
			t.Errorf("Close took too long with no resources: %v", elapsed)
		}
	})

	t.Run("LazyInitializationOnFirstHook", func(t *testing.T) {
		service := New[string](WithWorkers(5))
		defer service.Close()

		// Initially no resources
		if service.workers != nil {
			t.Error("Worker pool should not exist initially")
		}
		if service.metrics != nil {
			t.Error("Metrics should not exist initially")
		}

		// Register first hook
		hook, err := service.Hook("test", func(ctx context.Context, data string) error {
			return nil
		})
		if err != nil {
			t.Fatalf("Failed to register hook: %v", err)
		}

		// Now resources should be initialized
		if service.workers == nil {
			t.Error("Worker pool should be initialized after first hook")
		}
		if service.metrics == nil {
			t.Error("Metrics should be initialized after first hook")
		}

		// Unhook
		if err := hook.Unhook(); err != nil {
			t.Fatalf("Failed to unhook: %v", err)
		}

		// Resources remain allocated (for performance reasons)
		if service.workers == nil {
			t.Error("Worker pool should remain after unhooking")
		}
	})

	t.Run("MinimalMemoryFootprint", func(t *testing.T) {
		// Create multiple services without hooks
		services := make([]*Hooks[string], 100)
		for i := range services {
			services[i] = New[string]()
		}

		// All should have minimal memory usage
		for i, service := range services {
			if service.workers != nil {
				t.Errorf("Service %d has unnecessary worker pool", i)
			}
			if service.metrics != nil {
				t.Errorf("Service %d has unnecessary metrics", i)
			}
		}

		// Clean up
		for _, service := range services {
			service.Close()
		}
	})
}

// BenchmarkNoopEmit measures the overhead of Emit when no hooks are registered
func BenchmarkNoopEmit(b *testing.B) {
	service := New[string]()
	defer service.Close()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = service.Emit(ctx, "test.event", "data")
	}
}

// BenchmarkEmitWithHooks compares performance with hooks registered
func BenchmarkEmitWithHooks(b *testing.B) {
	service := New[string]()
	defer service.Close()

	// Register a simple hook
	service.Hook("test.event", func(ctx context.Context, data string) error {
		return nil
	})

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = service.Emit(ctx, "test.event", "data")
	}
}

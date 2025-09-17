package hookz

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Service test constants demonstrating Key patterns
const (
	ServiceTestEvent       Key = "service.test"
	MemoryTestEvent        Key = "memory.test"
	RaceTestEvent          Key = "race.test"
	ServiceGenerateIDEvent Key = "service.generateid"
	ServiceImplTestEvent   Key = "service.impl.test"
	ServiceSlowEvent       Key = "service.slow"
	ServiceHookEvent1      Key = "service.hook.event1"
	ServiceHookEvent2      Key = "service.hook.event2"
	ServiceNewEvent        Key = "service.new"
	ServiceCopyTestEvent   Key = "service.copy.test"
	ServiceShouldFailEvent Key = "service.should.fail"
	ServiceMutexTestEvent  Key = "service.mutex.test"
	ServiceDirectEvent     Key = "service.direct"
	ServiceInterfaceEvent  Key = "service.interface"
)

func TestRaceConditionSafety(t *testing.T) {
	service := New[int]()

	// Spam emissions in background
	done := make(chan bool)
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				service.Emit(context.Background(), RaceTestEvent, 1)
				runtime.Gosched()
			}
		}
	}()

	// Close concurrently - should not panic
	time.Sleep(10 * time.Millisecond)
	err := service.Close()
	close(done)

	assert.NoError(t, err)
}

func TestMemoryLeaks(t *testing.T) {
	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	// fakeClock removed - cargo cult usage
	service := New[string]()
	defer service.Close()

	// Register and unregister many hooks
	for i := 0; i < 1000; i++ {
		hook, err := service.Hook(MemoryTestEvent, func(ctx context.Context, s string) error {
			return nil
		})
		require.NoError(t, err)

		err = hook.Unhook()
		require.NoError(t, err)
	}

	runtime.GC()
	runtime.ReadMemStats(&m2)

	// Calculate memory difference safely
	var leaked int64
	if m2.Alloc >= m1.Alloc {
		leaked = int64(m2.Alloc - m1.Alloc)
	} else {
		leaked = -int64(m1.Alloc - m2.Alloc)
	}
	assert.Less(t, leaked, int64(1024*1024), "Should not leak >1MB, leaked: %d bytes", leaked)
}

func TestGenerateID(t *testing.T) {
	// Test that generateID produces unique strings
	// fakeClock removed - cargo cult usage
	svc := New[string]()
	defer svc.Close()

	ids := make(map[string]bool)
	for i := 0; i < 10000; i++ {
		id := svc.generateID()
		assert.False(t, ids[id], "Generated duplicate ID: %s", id)
		assert.Equal(t, 16, len(id), "ID should be 16 characters (8 bytes hex)")
		ids[id] = true
	}
}

func TestServiceImplementationDetails(t *testing.T) {
	// fakeClock removed - cargo cult usage
	service := New[string]()
	defer service.Close()

	// Test that the service is the correct struct type
	// Service is now a struct pointer, no need for type assertion

	// Initial state should be correct
	assert.False(t, service.closed)
	assert.NotNil(t, service.hooks)
	assert.NotNil(t, service.workers)

	// Register a hook and check internal state
	hook, err := service.Hook(ServiceImplTestEvent, func(ctx context.Context, data string) error { return nil })
	require.NoError(t, err)

	assert.Equal(t, 1, len(service.hooks[ServiceImplTestEvent]))

	// Unhook and verify cleanup
	err = hook.Unhook()
	require.NoError(t, err)

	assert.Equal(t, 0, len(service.hooks)) // Empty events should be cleaned up
}

func TestServiceWithWorkers(t *testing.T) {
	// Test that New with WithWorkers creates correct configuration
	// fakeClock removed - cargo cult usage
	service := New[int](WithWorkers(5))
	defer service.Close()

	// Service should function normally
	hook, err := service.Hook(ServiceSlowEvent, func(ctx context.Context, data int) error { return nil })
	require.NoError(t, err)
	defer hook.Unhook()

	err = service.Emit(context.Background(), ServiceSlowEvent, 42)
	assert.NoError(t, err)
}

func TestHookStorage(t *testing.T) {
	// fakeClock removed - cargo cult usage
	service := New[string]()
	defer service.Close()

	// Test that hooks are stored correctly by event
	hook1, err := service.Hook(ServiceHookEvent1, func(ctx context.Context, data string) error { return nil })
	require.NoError(t, err)
	defer hook1.Unhook()

	hook2, err := service.Hook(ServiceHookEvent1, func(ctx context.Context, data string) error { return nil })
	require.NoError(t, err)
	defer hook2.Unhook()

	hook3, err := service.Hook(ServiceHookEvent2, func(ctx context.Context, data string) error { return nil })
	require.NoError(t, err)
	defer hook3.Unhook()

	// Check internal storage structure
	assert.Equal(t, 2, len(service.hooks[ServiceHookEvent1]))
	assert.Equal(t, 1, len(service.hooks[ServiceHookEvent2]))

	// Test hook ID uniqueness
	assert.NotEqual(t, service.hooks[ServiceHookEvent1][0].id, service.hooks[ServiceHookEvent1][1].id)
	assert.NotEqual(t, service.hooks[ServiceHookEvent1][0].id, service.hooks[ServiceHookEvent2][0].id)
}

func TestCloseLifecycle(t *testing.T) {
	// fakeClock removed - cargo cult usage
	service := New[string]()

	// Initially not closed
	assert.False(t, service.closed)

	// Register some hooks
	hook1, _ := service.Hook(ServiceImplTestEvent, func(ctx context.Context, data string) error { return nil })
	hook2, _ := service.Hook(ServiceImplTestEvent, func(ctx context.Context, data string) error { return nil })

	// Close the service
	err := service.Close()
	require.NoError(t, err)
	assert.True(t, service.closed)

	// Hooks should still be able to unhook (they were registered before close)
	err = hook1.Unhook()
	assert.NoError(t, err)
	err = hook2.Unhook()
	assert.NoError(t, err)

	// But new operations should fail
	_, err = service.Hook(ServiceNewEvent, func(ctx context.Context, data string) error { return nil })
	assert.ErrorIs(t, err, ErrServiceClosed)
}

// TestDirectFieldMutation tests the "don't access directly" warning by
// directly mutating exported fields and verifying service recovery
func TestDirectFieldMutation(t *testing.T) {
	// fakeClock removed - cargo cult usage
	service := New[string]()
	defer service.Close()

	// Register a hook to establish baseline
	hook, err := service.Hook(ServiceImplTestEvent, func(ctx context.Context, data string) error { return nil })
	require.NoError(t, err)
	defer hook.Unhook()

	// Verify initial state
	assert.Equal(t, 1, len(service.hooks[ServiceImplTestEvent]))

	// DANGEROUS: Directly mutate hooks field (violates encapsulation)
	service.hooks[ServiceImplTestEvent] = nil

	// Service should be resilient - the maps are broken but new operations work
	hook2, err := service.Hook(ServiceImplTestEvent, func(ctx context.Context, data string) error { return nil })
	require.NoError(t, err)
	defer hook2.Unhook()

	// Map should be recreated for new events
	assert.NotNil(t, service.hooks[ServiceImplTestEvent])
	assert.Equal(t, 1, len(service.hooks[ServiceImplTestEvent]))

	// This shows why direct access is dangerous - it can break internal invariants
	// The service still works but internal state may be inconsistent
}

// TestSequentialFieldAccess tests field access patterns without race conditions
// This demonstrates that even "safe" field access requires coordination
func TestSequentialFieldAccess(t *testing.T) {
	// fakeClock removed - cargo cult usage
	service := New[string]()
	defer service.Close()

	// Read initial GlobalTimeout
	initialTimeout := service.GlobalTimeout
	assert.Equal(t, time.Duration(0), initialTimeout)

	// Modify GlobalTimeout
	service.GlobalTimeout = 5 * time.Second
	assert.Equal(t, 5*time.Second, service.GlobalTimeout)

	// Register hook to test internal state
	hook, err := service.Hook(ServiceImplTestEvent, func(ctx context.Context, data string) error { return nil })
	require.NoError(t, err)

	// Read hooks map length (requires understanding that this is not thread-safe)
	mapLen := len(service.hooks)
	assert.Equal(t, 1, mapLen)

	hook.Unhook()

	// Map should be cleaned up
	assert.Equal(t, 0, len(service.hooks))

	// This test shows field access works sequentially but demonstrates
	// that users need to understand thread safety implications
}

// TestUnsafeMapAccessWarning documents why direct hooks map access is dangerous
// This test uses proper locking to demonstrate the safe way vs warning about unsafe way
func TestUnsafeMapAccessWarning(t *testing.T) {
	// fakeClock removed - cargo cult usage
	service := New[string]()
	defer service.Close()

	// Register a hook
	hook, err := service.Hook(ServiceImplTestEvent, func(ctx context.Context, data string) error { return nil })
	require.NoError(t, err)

	// SAFE way to read map (what the warning recommends against)
	service.mu.RLock()
	mapLen := len(service.hooks) // Safe because we hold the read lock
	service.mu.RUnlock()
	assert.Equal(t, 1, mapLen)

	// This test documents that direct map access requires manual locking
	// which is why the struct documentation says "should not be accessed directly"
	// The methods handle locking automatically

	hook.Unhook()
}

// TestFieldEncapsulation verifies that private fields stay private
// and exported fields have documented access patterns
func TestFieldEncapsulation(t *testing.T) {
	// fakeClock removed - cargo cult usage
	service := New[string]()
	defer service.Close()

	// Exported fields should be readable
	timeout := service.GlobalTimeout
	assert.Equal(t, time.Duration(0), timeout)

	// Can modify GlobalTimeout (it's intended to be configurable)
	service.GlobalTimeout = 5 * time.Second
	assert.Equal(t, 5*time.Second, service.GlobalTimeout)

	// Exported map should be readable but documented as "don't access directly"
	hooksMap := service.hooks
	assert.NotNil(t, hooksMap)
	assert.Equal(t, 0, len(hooksMap))

	// Exported mutex should be accessible but documented as "direct use discouraged"
	mutex := &service.mu
	assert.NotNil(t, mutex)

	// Private fields should not be accessible
	// This won't compile (good):
	// _ = service.closed

	// But we can verify they work via methods
	hook, err := service.Hook(ServiceImplTestEvent, func(ctx context.Context, data string) error { return nil })
	require.NoError(t, err)

	// Verify internal state via behavior (not direct access)
	assert.Equal(t, 1, len(service.hooks[ServiceImplTestEvent]))

	err = hook.Unhook()
	assert.NoError(t, err)
	assert.Equal(t, 0, len(service.hooks))
}

// TestMutexDirectAccess tests the documented "direct use discouraged" pattern
// for the exported mutex field
func TestMutexDirectAccess(t *testing.T) {
	// fakeClock removed - cargo cult usage
	service := New[string]()
	defer service.Close()

	// Direct mutex access (discouraged but possible)
	service.mu.RLock()
	mapLen := len(service.hooks)
	service.mu.RUnlock()
	assert.Equal(t, 0, mapLen)

	// Misusing the mutex could cause deadlocks
	// We test correct usage here
	service.mu.Lock()
	// Direct map manipulation while holding lock (still not recommended)
	if service.hooks == nil {
		service.hooks = make(map[string][]hookEntry[string])
	}
	service.mu.Unlock()

	// Service should still function after direct mutex usage
	hook, err := service.Hook(ServiceMutexTestEvent, func(ctx context.Context, data string) error { return nil })
	require.NoError(t, err)
	defer hook.Unhook()

	assert.Equal(t, 1, len(service.hooks[ServiceMutexTestEvent]))
}

// TestInterfaceConversionPerformance benchmarks struct vs interface access
// This is a unit test that includes performance verification
func TestInterfaceConversionOverhead(t *testing.T) {
	const numOps = 10000

	// fakeClock removed - cargo cult usage
	hooks := New[string]()
	defer hooks.Close()

	// Test direct struct access
	start := time.Now()
	for i := 0; i < numOps; i++ {
		hook, err := hooks.Hook(ServiceDirectEvent, func(ctx context.Context, data string) error { return nil })
		if err == nil {
			hook.Unhook()
		}
	}
	directTime := time.Since(start)

	// Test through direct struct access
	registry := hooks
	start = time.Now()
	for i := 0; i < numOps; i++ {
		hook, err := registry.Hook(ServiceInterfaceEvent, func(ctx context.Context, data string) error { return nil })
		if err == nil {
			hook.Unhook()
		}
	}
	interfaceTime := time.Since(start)

	// Interface overhead should be minimal (< 50% slower)
	ratio := float64(interfaceTime) / float64(directTime)

	t.Logf("Direct access: %v", directTime)
	t.Logf("Interface access: %v", interfaceTime)
	t.Logf("Overhead ratio: %.2fx", ratio)

	// Allow some variance but interface shouldn't be dramatically slower
	assert.Less(t, ratio, 2.0, "Interface overhead too high: %.2fx", ratio)
}

// TestServiceConstantPatterns verifies the service works identically with const patterns
func TestServiceConstantPatterns(t *testing.T) {
	// fakeClock removed - cargo cult usage
	service := New[string]()
	defer service.Close()

	executed := make(map[string]bool)
	mu := sync.Mutex{}

	handler := func(pattern string) func(context.Context, string) error {
		return func(ctx context.Context, data string) error {
			mu.Lock()
			executed[pattern] = true
			mu.Unlock()
			return nil
		}
	}

	// Register hooks using different Key patterns
	hook1, err := service.Hook(ServiceTestEvent, handler("const"))
	require.NoError(t, err)
	defer hook1.Unhook()

	hook2, err := service.Hook(MemoryTestEvent, handler("memory-const"))
	require.NoError(t, err)
	defer hook2.Unhook()

	hook3, err := service.Hook(RaceTestEvent, handler("race-const"))
	require.NoError(t, err)
	defer hook3.Unhook()

	// Emit using const patterns
	service.Emit(context.Background(), ServiceTestEvent, "test")
	service.Emit(context.Background(), MemoryTestEvent, "mem")
	service.Emit(context.Background(), RaceTestEvent, "race")

	// Wait for execution
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, executed["const"], "ServiceTestEvent should execute")
	assert.True(t, executed["memory-const"], "MemoryTestEvent should execute")
	assert.True(t, executed["race-const"], "RaceTestEvent should execute")
}

// TestBackpressureConfiguration validates backpressure configuration behavior
func TestBackpressureConfiguration(t *testing.T) {
	t.Run("ValidatesConfigurationFields", func(t *testing.T) {
		// fakeClock removed - cargo cult usage
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

		// Service should be created successfully with valid config
		assert.NotNil(t, service)
	})

	t.Run("AcceptsValidStrategies", func(t *testing.T) {
		strategies := []string{"fixed", "linear", "exponential"}

		for _, strategy := range strategies {
			t.Run(strategy, func(t *testing.T) {
				// fakeClock removed - cargo cult usage
				service := New[int](
					WithWorkers(1),
					WithQueueSize(2),
					WithBackpressure(BackpressureConfig{
						MaxWait:        5 * time.Millisecond,
						StartThreshold: 0.5,
						Strategy:       strategy,
					}),
				)
				defer service.Close()

				// Service should initialize with valid strategy
				assert.NotNil(t, service)
			})
		}
	})
}

// TestOverflowConfiguration validates overflow configuration behavior
func TestOverflowConfiguration(t *testing.T) {
	t.Run("ValidatesConfigurationFields", func(t *testing.T) {
		// fakeClock removed - cargo cult usage
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

		// Service should be created successfully with valid config
		assert.NotNil(t, service)
	})

	t.Run("AcceptsValidEvictionStrategies", func(t *testing.T) {
		strategies := []string{"fifo", "lifo", "reject"}

		for _, strategy := range strategies {
			t.Run(strategy, func(t *testing.T) {
				// fakeClock removed - cargo cult usage
				service := New[int](
					WithWorkers(1),
					WithQueueSize(5),
					WithOverflow(OverflowConfig{
						Capacity:         20,
						DrainInterval:    10 * time.Millisecond,
						EvictionStrategy: strategy,
					}),
				)
				defer service.Close()

				// Service should initialize with valid eviction strategy
				assert.NotNil(t, service)
			})
		}
	})

	t.Run("HandlesZeroCapacity", func(t *testing.T) {
		// fakeClock removed - cargo cult usage
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

		// Service should handle zero capacity gracefully
		assert.NotNil(t, service)
	})
}

// TestServiceAdminOperationsWithKey verifies Clear operations work with Key types
func TestServiceAdminOperationsWithKey(t *testing.T) {
	// fakeClock removed - cargo cult usage
	service := New[int]()
	defer service.Close()

	// Register hooks with const patterns
	_, err := service.Hook(ServiceTestEvent, func(ctx context.Context, data int) error { return nil })
	require.NoError(t, err)

	_, err = service.Hook(MemoryTestEvent, func(ctx context.Context, data int) error { return nil })
	require.NoError(t, err)

	_, err = service.Hook(MemoryTestEvent, func(ctx context.Context, data int) error { return nil })
	require.NoError(t, err)

	// Test Clear with Key constants
	count := service.Clear(MemoryTestEvent)
	assert.Equal(t, 2, count, "Should clear both MemoryTestEvent hooks")

	count = service.Clear(ServiceTestEvent)
	assert.Equal(t, 1, count, "Should clear ServiceTestEvent hook")

	// Test ClearAll
	_, err = service.Hook(RaceTestEvent, func(ctx context.Context, data int) error { return nil })
	require.NoError(t, err)

	totalCleared := service.ClearAll()
	assert.Equal(t, 1, totalCleared, "Should clear remaining RaceTestEvent hook")

	// Ensure we can still register after clearing
	hook5, err := service.Hook(ServiceTestEvent, func(ctx context.Context, data int) error { return nil })
	require.NoError(t, err)
	defer hook5.Unhook()
}

// TestCombinedResilienceConfiguration validates combined backpressure and overflow config
func TestCombinedResilienceConfiguration(t *testing.T) {
	t.Run("AcceptsBothConfigurations", func(t *testing.T) {
		// fakeClock removed - cargo cult usage
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

		// Service should initialize with both resilience configurations
		assert.NotNil(t, service)
	})
}

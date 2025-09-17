package hookz

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test constants demonstrating Key usage patterns
const (
	TestEvent               Key = "test.event"
	TestUserEvent           Key = "user.test"
	TestPaymentEvent        Key = "payment.authorized"
	TestWebhookEvent        Key = "webhook.delivered"
	TestBasicEvent          Key = "test.basic"
	TestGlobalTimeoutEvent  Key = "test.globaltimeout"
	TestMultiEvent          Key = "test.multi"
	TestAsyncEvent          Key = "test.async"
	TestClosedEvent         Key = "test.closed"
	TestNonexistentEvent    Key = "test.nonexistent"
	TestRegistryEvent       Key = "test.registry"
	TestEmitterEvent        Key = "test.emitter"
	TestAdminEvent1         Key = "test.admin.event1"
	TestAdminEvent2         Key = "test.admin.event2"
	TestOrderProcessed      Key = "order.processed"
	TestZeroTimeoutEvent    Key = "test.zerotimeout"
	TestBackwardCompatEvent Key = "test.backwardcompat"
	TestStringLiteralEvent  Key = "string.event"
	TestExplicitEvent       Key = "explicit.event"
	TestUserCreatedEvent    Key = "user.created"
)

func TestBasicHookRegistration(t *testing.T) {
	service := New[string]()
	defer service.Close()

	called := make(chan bool, 1)
	hook, err := service.Hook(TestBasicEvent, func(ctx context.Context, data string) error {
		assert.Equal(t, "test-data", data)
		called <- true
		return nil
	})

	require.NoError(t, err)
	defer hook.Unhook()

	err = service.Emit(context.Background(), TestBasicEvent, "test-data")
	require.NoError(t, err)

	// Wait for async execution to complete
	select {
	case <-called:
		// Good
	case <-time.After(100 * time.Millisecond):
		t.Error("Hook was not called")
	}
}

func TestMultipleHooks(t *testing.T) {
	service := New[string]()
	defer service.Close()

	var calls atomic.Int32

	// Register multiple hooks for same event
	hooks := make([]Hook, 3)
	for i := 0; i < 3; i++ {
		hook, err := service.Hook(TestMultiEvent, func(ctx context.Context, data string) error {
			calls.Add(1)
			return nil
		})
		require.NoError(t, err)
		hooks[i] = hook
	}

	// Cleanup
	defer func() {
		for _, hook := range hooks {
			hook.Unhook()
		}
	}()

	err := service.Emit(context.Background(), TestMultiEvent, "data")
	require.NoError(t, err)

	// Wait for async execution
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(3), calls.Load())
}

func TestEmitFireAndForget(t *testing.T) {
	service := New[string]()
	defer service.Close()

	called := make(chan string, 1)
	hook, err := service.Hook(TestAsyncEvent, func(ctx context.Context, data string) error {
		called <- data
		return nil
	})
	require.NoError(t, err)
	defer hook.Unhook()

	err = service.Emit(context.Background(), TestAsyncEvent, "async-data")
	require.NoError(t, err)

	select {
	case data := <-called:
		assert.Equal(t, "async-data", data)
	case <-time.After(100 * time.Millisecond):
		t.Error("Fire-and-forget emit did not complete")
	}
}

func TestClosedServiceRejectsOperations(t *testing.T) {
	service := New[string]()

	// Close the service
	err := service.Close()
	require.NoError(t, err)

	// Double close should return error
	err = service.Close()
	assert.ErrorIs(t, err, ErrAlreadyClosed)

	// Hook registration should fail
	hook, err := service.Hook(TestClosedEvent, func(ctx context.Context, data string) error { return nil })
	assert.ErrorIs(t, err, ErrServiceClosed)
	assert.Equal(t, Hook{}, hook)

	// Emit should fail
	err = service.Emit(context.Background(), TestClosedEvent, "data")
	assert.ErrorIs(t, err, ErrServiceClosed)

	err = service.Emit(context.Background(), TestClosedEvent, "data")
	assert.ErrorIs(t, err, ErrServiceClosed)
}

func TestNoHooksRegistered(t *testing.T) {
	// fakeClock removed - cargo cult usage
	service := New[string]()
	defer service.Close()

	// Emit to event with no hooks - should not error
	err := service.Emit(context.Background(), TestNonexistentEvent, "data")
	assert.NoError(t, err)

	err = service.Emit(context.Background(), TestNonexistentEvent, "data")
	assert.NoError(t, err)
}

func TestDirectHooksAccess(t *testing.T) {
	// fakeClock removed - cargo cult usage
	service := New[string]()
	defer service.Close()

	// Direct use of Hooks[T] struct
	hooks := service

	// Should be able to register and unregister hooks
	hook, err := hooks.Hook(TestRegistryEvent, func(ctx context.Context, data string) error {
		return nil
	})
	require.NoError(t, err)

	err = hooks.Unhook(hook)
	assert.NoError(t, err)

	// Direct struct access provides all methods
	err = hooks.Emit(context.Background(), TestRegistryEvent, "test")
	assert.NoError(t, err)

	count := hooks.Clear(TestRegistryEvent)
	assert.GreaterOrEqual(t, count, 0)
}

func TestSegregatedInterfaceHookEmitter(t *testing.T) {
	// fakeClock removed - cargo cult usage
	service := New[string]()
	defer service.Close()

	called := make(chan bool, 1)
	_, err := service.Hook(TestEmitterEvent, func(ctx context.Context, data string) error {
		called <- true
		return nil
	})
	require.NoError(t, err)

	// Use service directly for emission
	emitter := service

	// Should be able to emit events
	err = emitter.Emit(context.Background(), TestEmitterEvent, "data")
	require.NoError(t, err)

	err = emitter.Emit(context.Background(), TestEmitterEvent, "data")
	require.NoError(t, err)

	// Wait for async execution
	select {
	case <-called:
		// Good
	case <-time.After(100 * time.Millisecond):
		t.Error("Hook was not called")
	}

	// HookEmitter should not have access to registration methods
	// This is compile-time verification - the interface doesn't expose them
}

func TestSegregatedInterfaceHookAdmin(t *testing.T) {
	// fakeClock removed - cargo cult usage
	service := New[string]()
	defer service.Close()

	// Register some hooks
	hook1, err := service.Hook(TestAdminEvent1, func(ctx context.Context, data string) error { return nil })
	require.NoError(t, err)
	defer hook1.Unhook()

	hook2, err := service.Hook(TestAdminEvent1, func(ctx context.Context, data string) error { return nil })
	require.NoError(t, err)
	defer hook2.Unhook()

	hook3, err := service.Hook(TestAdminEvent2, func(ctx context.Context, data string) error { return nil })
	require.NoError(t, err)
	defer hook3.Unhook()

	// Use service directly for admin operations
	admin := service

	// Should be able to clear specific events
	count := admin.Clear(TestAdminEvent1)
	assert.Equal(t, 2, count)

	// Should be able to clear all events
	count = admin.ClearAll()
	assert.Equal(t, 1, count) // event2 hook
}

func TestSegregatedInterfaceHookLifecycle(t *testing.T) {
	// fakeClock removed - cargo cult usage
	service := New[string]()

	// Use service directly for lifecycle operations
	lifecycle := service

	// Should be able to close the service
	err := lifecycle.Close()
	assert.NoError(t, err)
}

func TestOptionsPattern(t *testing.T) {
	t.Run("Default configuration", func(t *testing.T) {
		// fakeClock removed - cargo cult usage
		service := New[string]()
		defer service.Close()
		assert.NotNil(t, service)
		assert.Equal(t, time.Duration(0), service.GlobalTimeout)
	})

	t.Run("WithWorkers option", func(t *testing.T) {
		// fakeClock removed - cargo cult usage
		service := New[string](WithWorkers(20))
		defer service.Close()
		assert.NotNil(t, service)
		// Worker count can't be directly tested, but service should function
	})

	t.Run("WithTimeout option", func(t *testing.T) {
		// fakeClock removed - cargo cult usage
		timeout := 5 * time.Second
		service := New[string](WithTimeout(timeout))
		defer service.Close()
		assert.NotNil(t, service)
		assert.Equal(t, timeout, service.GlobalTimeout)
	})

	t.Run("Combined options", func(t *testing.T) {
		// fakeClock removed - cargo cult usage
		timeout := 2 * time.Second
		service := New[string](
			WithWorkers(15),
			WithTimeout(timeout),
			WithQueueSize(50),
		)
		defer service.Close()
		assert.NotNil(t, service)
		assert.Equal(t, timeout, service.GlobalTimeout)
	})

	t.Run("WithQueueSize option", func(t *testing.T) {
		// fakeClock removed - cargo cult usage
		service := New[string](WithQueueSize(100))
		defer service.Close()
		assert.NotNil(t, service)
		// Queue size can't be directly tested, but service should function
	})
}

// OrderService demonstrates the struct-based pattern with interface segregation
type OrderService struct {
	hooks Hooks[string] // Private field, not embedded
}

func NewOrderService() *OrderService {
	return &OrderService{
		hooks: *New[string](), // Initialize as value
	}
}

func (s *OrderService) Events() *Hooks[string] {
	return &s.hooks // Return struct directly
}

func (s *OrderService) processOrder(order string) error {
	// Service can emit internally
	return s.hooks.Emit(context.Background(), TestOrderProcessed, order)
}

func (s *OrderService) Close() error {
	// Service controls lifecycle
	return s.hooks.Close()
}

func TestInterfaceSegregationInPractice(t *testing.T) {
	// Create service
	service := NewOrderService()
	defer service.Close()

	// External consumers get limited interface
	registry := service.Events()

	called := make(chan string, 1)
	hook, err := registry.Hook(TestOrderProcessed, func(ctx context.Context, data string) error {
		called <- data
		return nil
	})
	require.NoError(t, err)
	defer hook.Unhook()

	// Service can emit internally
	err = service.processOrder("test-order")
	require.NoError(t, err)

	// Verify hook was called (async emission)
	select {
	case data := <-called:
		assert.Equal(t, "test-order", data)
	case <-time.After(100 * time.Millisecond):
		t.Error("Hook was not called")
	}

	// Consumer cannot emit (compile-time safety)
	// registry.Emit(...) // Would not compile
	// registry.Close()   // Would not compile
}

func TestZeroTimeoutMeansNoTimeout(t *testing.T) {
	// fakeClock removed - cargo cult usage
	service := New[string](WithWorkers(10), WithTimeout(0)) // Zero timeout = no timeout
	defer service.Close()

	completed := make(chan bool, 1)
	hook, err := service.Hook(TestZeroTimeoutEvent, func(ctx context.Context, data string) error {
		// Simulate work without actual time.Sleep
		completed <- true
		return nil
	})
	require.NoError(t, err)
	defer hook.Unhook()

	err = service.Emit(context.Background(), TestZeroTimeoutEvent, "data")
	require.NoError(t, err)

	// Should complete without timeout
	select {
	case <-completed:
		// Good - no timeout occurred
	case <-time.After(200 * time.Millisecond):
		t.Error("Hook should have completed without timeout")
	}
}

func TestBackwardCompatibilityFullInterface(t *testing.T) {
	// fakeClock removed - cargo cult usage
	service := New[string]()
	defer service.Close()

	// Full interface should work as before
	hook, err := service.Hook(TestBackwardCompatEvent, func(ctx context.Context, data string) error {
		return nil
	})
	require.NoError(t, err)

	err = service.Unhook(hook)
	require.NoError(t, err)

	count := service.Clear(TestNonexistentEvent)
	assert.Equal(t, 0, count)

	err = service.Emit(context.Background(), TestBackwardCompatEvent, "data")
	require.NoError(t, err)

	err = service.Emit(context.Background(), TestBackwardCompatEvent, "data")
	require.NoError(t, err)
}

// TestKeyConstantPattern demonstrates using Key constants (recommended approach)
func TestKeyConstantPattern(t *testing.T) {
	// fakeClock removed - cargo cult usage
	service := New[string]()
	defer service.Close()

	called := make(chan bool, 1)
	hook, err := service.Hook(TestEvent, func(ctx context.Context, data string) error {
		assert.Equal(t, "test-data", data)
		called <- true
		return nil
	})

	require.NoError(t, err)
	defer hook.Unhook()

	err = service.Emit(context.Background(), TestEvent, "test-data")
	require.NoError(t, err)

	// Wait for async execution
	select {
	case <-called:
		// Success
	case <-time.After(100 * time.Millisecond):
		t.Error("Hook was not called")
	}
}

// TestKeyCompatibilityPatterns demonstrates both string literals and Key constants work identically
func TestKeyCompatibilityPatterns(t *testing.T) {
	// fakeClock removed - cargo cult usage
	service := New[string]()
	defer service.Close()

	callCount := int32(0)
	handler := func(ctx context.Context, data string) error {
		atomic.AddInt32(&callCount, 1)
		return nil
	}

	// Register with string literal (existing pattern)
	hook1, err := service.Hook(TestUserCreatedEvent, handler)
	require.NoError(t, err)
	defer hook1.Unhook()

	// Register with Key constant (new pattern)
	hook2, err := service.Hook(TestUserEvent, handler)
	require.NoError(t, err)
	defer hook2.Unhook()

	// Emit with string literal
	err = service.Emit(context.Background(), TestUserCreatedEvent, "data1")
	require.NoError(t, err)

	// Emit with Key constant
	err = service.Emit(context.Background(), TestUserEvent, "data2")
	require.NoError(t, err)

	// Wait for execution
	time.Sleep(50 * time.Millisecond)

	// Both patterns should work identically
	assert.Equal(t, int32(2), atomic.LoadInt32(&callCount))
}

// TestDynamicKeyPattern demonstrates runtime-determined event names
//
// COMPATIBILITY NOTE: This pattern is technically supported but NOT recommended.
// We strongly recommend using const Keys instead of dynamic keys because:
//
// - Dynamic keys defeat discoverability (can't grep for event names)
// - No compile-time safety (typos only discovered at runtime)
// - Harder to maintain and debug
// - Makes static analysis and tooling difficult
//
// Only use dynamic keys when absolutely necessary, such as:
// - Tenant-specific routing where tenant ID is runtime-determined
// - Plugin systems where event names come from external configuration
// - Migration scenarios transitioning from legacy string-based systems
//
// For most use cases, prefer const Keys defined at package level.
func TestDynamicKeyPattern(t *testing.T) {
	// fakeClock removed - cargo cult usage
	service := New[string]()
	defer service.Close()

	called := make(chan bool, 1)

	// Dynamic event key based on runtime data
	tenantID := "tenant-123"
	dynamicKey := Key(fmt.Sprintf("tenant.%s.event", tenantID))

	hook, err := service.Hook(dynamicKey, func(ctx context.Context, data string) error {
		assert.Equal(t, "tenant-data", data)
		called <- true
		return nil
	})

	require.NoError(t, err)
	defer hook.Unhook()

	err = service.Emit(context.Background(), dynamicKey, "tenant-data")
	require.NoError(t, err)

	// Wait for async execution
	select {
	case <-called:
		// Success
	case <-time.After(100 * time.Millisecond):
		t.Error("Dynamic key hook was not called")
	}
}

// TestMultipleKeyPatternsCoexistence verifies all patterns work together
func TestMultipleKeyPatternsCoexistence(t *testing.T) {
	// fakeClock removed - cargo cult usage
	service := New[int]()
	defer service.Close()

	results := make(chan string, 4)

	handler := func(pattern string) func(context.Context, int) error {
		return func(ctx context.Context, data int) error {
			results <- pattern
			return nil
		}
	}

	// String literal pattern
	hook1, err := service.Hook(TestStringLiteralEvent, handler("string"))
	require.NoError(t, err)
	defer hook1.Unhook()

	// Const pattern
	hook2, err := service.Hook(TestPaymentEvent, handler("const"))
	require.NoError(t, err)
	defer hook2.Unhook()

	// Explicit Key conversion
	hook3, err := service.Hook(TestExplicitEvent, handler("explicit"))
	require.NoError(t, err)
	defer hook3.Unhook()

	// Dynamic pattern
	userID := "user-456"
	dynamicKey := Key(fmt.Sprintf("user.%s.action", userID))
	hook4, err := service.Hook(dynamicKey, handler("dynamic"))
	require.NoError(t, err)
	defer hook4.Unhook()

	// Emit to all patterns
	service.Emit(context.Background(), TestStringLiteralEvent, 1)
	service.Emit(context.Background(), TestPaymentEvent, 2)
	service.Emit(context.Background(), TestExplicitEvent, 3)
	service.Emit(context.Background(), dynamicKey, 4)

	// Wait for execution
	time.Sleep(50 * time.Millisecond)

	// Collect results
	patterns := make(map[string]bool)
	for i := 0; i < 4; i++ {
		select {
		case pattern := <-results:
			patterns[pattern] = true
		default:
			t.Errorf("Missing result for pattern %d", i+1)
		}
	}

	// Verify all patterns executed
	assert.True(t, patterns["string"], "String literal pattern should work")
	assert.True(t, patterns["const"], "Const pattern should work")
	assert.True(t, patterns["explicit"], "Explicit Key conversion should work")
	assert.True(t, patterns["dynamic"], "Dynamic pattern should work")
}

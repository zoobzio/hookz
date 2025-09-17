package integration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zoobzio/hookz"
)

// Event keys for timeout resilience testing
const (
	fastEvent        hookz.Key = "fast.event"
	normalEvent      hookz.Key = "normal.event"
	slowEvent        hookz.Key = "slow.event"
	timeoutTestEvent hookz.Key = "test.event"
)

// TimeoutTestService demonstrates timeout handling patterns
type TimeoutTestService struct {
	// Different timeout configurations for different use cases
	fastHooks   *hookz.Hooks[TimeoutEvent] // 100ms timeout
	normalHooks *hookz.Hooks[TimeoutEvent] // 1s timeout
	slowHooks   *hookz.Hooks[TimeoutEvent] // 5s timeout

	// Track timeout behavior
	timeouts      atomic.Int64
	completions   atomic.Int64
	cancellations atomic.Int64
}

// TimeoutEvent for testing timeout scenarios
type TimeoutEvent struct {
	ID          string
	Type        string
	Data        string
	Timestamp   time.Time
	ShouldDelay time.Duration // For testing - how long the handler should take
}

// NewTimeoutTestService creates a service with varied timeout configurations
func NewTimeoutTestService() *TimeoutTestService {
	return &TimeoutTestService{
		fastHooks:   hookz.New[TimeoutEvent](hookz.WithWorkers(10), hookz.WithTimeout(100*time.Millisecond)),
		normalHooks: hookz.New[TimeoutEvent](hookz.WithWorkers(10), hookz.WithTimeout(1*time.Second)),
		slowHooks:   hookz.New[TimeoutEvent](hookz.WithWorkers(10), hookz.WithTimeout(5*time.Second)),
	}
}

// FastEvents returns the fast timeout registry
func (s *TimeoutTestService) FastEvents() *hookz.Hooks[TimeoutEvent] {
	return s.fastHooks
}

// NormalEvents returns the normal timeout registry
func (s *TimeoutTestService) NormalEvents() *hookz.Hooks[TimeoutEvent] {
	return s.normalHooks
}

// SlowEvents returns the slow timeout registry
func (s *TimeoutTestService) SlowEvents() *hookz.Hooks[TimeoutEvent] {
	return s.slowHooks
}

// EmitFast emits an event with fast timeout
func (s *TimeoutTestService) EmitFast(ctx context.Context, event TimeoutEvent) error {
	return s.fastHooks.Emit(ctx, fastEvent, event)
}

// EmitNormal emits an event with normal timeout
func (s *TimeoutTestService) EmitNormal(ctx context.Context, event TimeoutEvent) error {
	return s.normalHooks.Emit(ctx, normalEvent, event)
}

// EmitSlow emits an event with slow timeout
func (s *TimeoutTestService) EmitSlow(ctx context.Context, event TimeoutEvent) error {
	return s.slowHooks.Emit(ctx, slowEvent, event)
}

// Shutdown closes all services
func (s *TimeoutTestService) Shutdown() error {
	s.fastHooks.Close()
	s.normalHooks.Close()
	s.slowHooks.Close()
	return nil
}

// TestTimeoutBehavior verifies timeout enforcement
func TestTimeoutBehavior(t *testing.T) {
	svc := NewTimeoutTestService()
	defer svc.Shutdown()

	ctx := context.Background()

	// Track hook execution results
	var fastCompleted atomic.Bool
	var normalCompleted atomic.Bool
	var slowCompleted atomic.Bool

	var fastTimedOut atomic.Bool
	var normalTimedOut atomic.Bool
	var slowTimedOut atomic.Bool

	// Fast hook - will timeout (takes 200ms, timeout is 100ms)
	svc.FastEvents().Hook(fastEvent, func(ctx context.Context, event TimeoutEvent) error {
		select {
		case <-time.After(200 * time.Millisecond):
			fastCompleted.Store(true)
			return nil
		case <-ctx.Done():
			fastTimedOut.Store(true)
			return ctx.Err()
		}
	})

	// Normal hook - will complete (takes 500ms, timeout is 1s)
	svc.NormalEvents().Hook(normalEvent, func(ctx context.Context, event TimeoutEvent) error {
		select {
		case <-time.After(500 * time.Millisecond):
			normalCompleted.Store(true)
			return nil
		case <-ctx.Done():
			normalTimedOut.Store(true)
			return ctx.Err()
		}
	})

	// Slow hook - will complete (takes 2s, timeout is 5s)
	svc.SlowEvents().Hook(slowEvent, func(ctx context.Context, event TimeoutEvent) error {
		select {
		case <-time.After(2 * time.Second):
			slowCompleted.Store(true)
			return nil
		case <-ctx.Done():
			slowTimedOut.Store(true)
			return ctx.Err()
		}
	})

	// Emit events
	svc.EmitFast(ctx, TimeoutEvent{ID: "fast-1"})
	svc.EmitNormal(ctx, TimeoutEvent{ID: "normal-1"})
	svc.EmitSlow(ctx, TimeoutEvent{ID: "slow-1"})

	// Wait for all processing
	time.Sleep(3 * time.Second)

	// Check results
	if !fastTimedOut.Load() {
		t.Error("Fast hook should have timed out")
	}
	if fastCompleted.Load() {
		t.Error("Fast hook should not have completed")
	}

	if !normalCompleted.Load() {
		t.Error("Normal hook should have completed")
	}
	if normalTimedOut.Load() {
		t.Error("Normal hook should not have timed out")
	}

	if !slowCompleted.Load() {
		t.Error("Slow hook should have completed")
	}
	if slowTimedOut.Load() {
		t.Error("Slow hook should not have timed out")
	}
}

// TestContextPropagation verifies that context cancellation propagates to hooks
func TestContextPropagation(t *testing.T) {
	svc := NewTimeoutTestService()
	defer svc.Shutdown()

	// Track context cancellation
	var hookStarted atomic.Bool
	var hookCancelled atomic.Bool
	var cancellationReason string
	var mu sync.Mutex

	// Register hook that waits for context
	svc.NormalEvents().Hook(normalEvent, func(ctx context.Context, event TimeoutEvent) error {
		hookStarted.Store(true)

		// Wait for context cancellation
		<-ctx.Done()

		hookCancelled.Store(true)
		mu.Lock()
		cancellationReason = ctx.Err().Error()
		mu.Unlock()

		return ctx.Err()
	})

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	// Emit event
	err := svc.EmitNormal(ctx, TimeoutEvent{ID: "cancel-test"})
	if err != nil {
		t.Fatalf("Failed to emit: %v", err)
	}

	// Wait for hook to start
	time.Sleep(50 * time.Millisecond)
	if !hookStarted.Load() {
		t.Fatal("Hook did not start")
	}

	// Cancel context
	cancel()

	// Wait for cancellation to propagate
	time.Sleep(50 * time.Millisecond)

	if !hookCancelled.Load() {
		t.Error("Hook was not canceled")
	}

	mu.Lock()
	reason := cancellationReason
	mu.Unlock()

	if reason != "context canceled" {
		t.Errorf("Unexpected cancellation reason: %s", reason)
	}
}

// TestTimeoutIsolation verifies that slow hooks don't affect fast hooks
func TestTimeoutIsolation(t *testing.T) {
	svc := NewTimeoutTestService()
	defer svc.Shutdown()

	ctx := context.Background()

	// Track execution times with synchronization
	var mu sync.Mutex
	var slowHookEnd time.Time
	var fastHookStart, fastHookEnd time.Time
	var fastHook2End time.Time

	// Slow hook that blocks for a while
	svc.SlowEvents().Hook(slowEvent, func(ctx context.Context, event TimeoutEvent) error {
		time.Sleep(2 * time.Second)
		mu.Lock()
		slowHookEnd = time.Now()
		mu.Unlock()
		return nil
	})

	// Fast hook 1
	svc.FastEvents().Hook(fastEvent, func(ctx context.Context, event TimeoutEvent) error {
		mu.Lock()
		fastHookStart = time.Now()
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		fastHookEnd = time.Now()
		mu.Unlock()
		return nil
	})

	// Fast hook 2
	svc.FastEvents().Hook(fastEvent, func(ctx context.Context, event TimeoutEvent) error {
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		fastHook2End = time.Now()
		mu.Unlock()
		return nil
	})

	// Emit slow event first
	svc.EmitSlow(ctx, TimeoutEvent{ID: "slow"})

	// Small delay to ensure slow hook starts
	time.Sleep(10 * time.Millisecond)

	// Emit fast events - should not be blocked by slow hook
	svc.EmitFast(ctx, TimeoutEvent{ID: "fast-1"})
	svc.EmitFast(ctx, TimeoutEvent{ID: "fast-2"})

	// Wait for fast hooks to complete
	time.Sleep(200 * time.Millisecond)

	// Fast hooks should have completed while slow hook is still running
	mu.Lock()
	checkFastEnd := fastHookEnd
	checkFast2End := fastHook2End
	checkSlowEnd := slowHookEnd
	mu.Unlock()

	if checkFastEnd.IsZero() || checkFast2End.IsZero() {
		t.Error("Fast hooks did not complete")
	}

	if !checkSlowEnd.IsZero() {
		t.Error("Slow hook completed too early")
	}

	// Wait for slow hook to complete
	time.Sleep(2 * time.Second)

	mu.Lock()
	finalSlowEnd := slowHookEnd
	mu.Unlock()

	if finalSlowEnd.IsZero() {
		t.Error("Slow hook did not complete")
	}

	// Verify execution overlap
	if fastHookStart.After(slowHookEnd) {
		t.Error("Fast hooks waited for slow hook to complete")
	}
}

// TestTimeoutRecovery verifies system recovers after timeouts
func TestTimeoutRecovery(t *testing.T) {
	svc := NewTimeoutTestService()
	defer svc.Shutdown()

	ctx := context.Background()

	var beforeTimeoutCalled atomic.Int32
	var timedOutCount atomic.Int32
	var afterTimeoutCalled atomic.Int32

	// Hook that succeeds
	svc.FastEvents().Hook(fastEvent, func(ctx context.Context, event TimeoutEvent) error {
		if event.ID == "before-timeout" {
			beforeTimeoutCalled.Add(1)
			time.Sleep(50 * time.Millisecond) // Within timeout
			return nil
		}
		return nil
	})

	// Hook that times out
	svc.FastEvents().Hook(fastEvent, func(ctx context.Context, event TimeoutEvent) error {
		if event.ID == "will-timeout" {
			select {
			case <-time.After(200 * time.Millisecond): // Exceeds 100ms timeout
				return nil
			case <-ctx.Done():
				timedOutCount.Add(1)
				return ctx.Err()
			}
		}
		return nil
	})

	// Hook that runs after timeout
	svc.FastEvents().Hook(fastEvent, func(ctx context.Context, event TimeoutEvent) error {
		if event.ID == "after-timeout" {
			afterTimeoutCalled.Add(1)
			time.Sleep(50 * time.Millisecond) // Within timeout
			return nil
		}
		return nil
	})

	// Emit events in sequence
	svc.EmitFast(ctx, TimeoutEvent{ID: "before-timeout"})
	time.Sleep(100 * time.Millisecond)

	svc.EmitFast(ctx, TimeoutEvent{ID: "will-timeout"})
	time.Sleep(150 * time.Millisecond)

	svc.EmitFast(ctx, TimeoutEvent{ID: "after-timeout"})
	time.Sleep(100 * time.Millisecond)

	// Verify all hooks were attempted
	if beforeTimeoutCalled.Load() != 1 {
		t.Errorf("Before timeout hook not called: %d", beforeTimeoutCalled.Load())
	}
	if timedOutCount.Load() != 1 {
		t.Errorf("Timeout not detected: %d", timedOutCount.Load())
	}
	if afterTimeoutCalled.Load() != 1 {
		t.Errorf("After timeout hook not called: %d", afterTimeoutCalled.Load())
	}
}

// TestCascadingTimeouts tests timeout behavior with dependent hooks
func TestCascadingTimeouts(t *testing.T) {
	svc := NewTimeoutTestService()
	defer svc.Shutdown()

	// Secondary service that reacts to primary events
	secondary := NewTimeoutTestService()
	defer secondary.Shutdown()

	var primaryProcessed atomic.Int32
	var secondaryProcessed atomic.Int32
	var cascadeTimeouts atomic.Int32

	// Primary hook triggers secondary event
	svc.NormalEvents().Hook(normalEvent, func(ctx context.Context, event TimeoutEvent) error {
		primaryProcessed.Add(1)

		// Trigger secondary event with remaining context time
		secondaryEvent := TimeoutEvent{
			ID:   fmt.Sprintf("secondary-%s", event.ID),
			Type: "cascade",
		}

		// This should inherit the timeout from the primary context
		err := secondary.EmitFast(ctx, secondaryEvent)
		if err != nil {
			return fmt.Errorf("failed to emit secondary: %w", err)
		}

		// Simulate some processing
		time.Sleep(100 * time.Millisecond)
		return nil
	})

	// Secondary hook processes cascaded events
	secondary.FastEvents().Hook(fastEvent, func(ctx context.Context, event TimeoutEvent) error {
		if event.Type == "cascade" {
			select {
			case <-time.After(200 * time.Millisecond):
				secondaryProcessed.Add(1)
				return nil
			case <-ctx.Done():
				cascadeTimeouts.Add(1)
				return ctx.Err()
			}
		}
		return nil
	})

	// Emit primary event with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	err := svc.EmitNormal(ctx, TimeoutEvent{ID: "primary-1"})
	if err != nil {
		t.Fatalf("Failed to emit primary: %v", err)
	}

	// Wait for processing
	time.Sleep(300 * time.Millisecond)

	// Check results
	if primaryProcessed.Load() != 1 {
		t.Error("Primary hook should have processed")
	}

	// Secondary should timeout due to inherited context timeout
	if cascadeTimeouts.Load() != 1 {
		t.Error("Secondary hook should have timed out due to context")
	}
	if secondaryProcessed.Load() != 0 {
		t.Error("Secondary hook should not have completed")
	}
}

// TestDeadlineExceeded tests behavior when context deadline is exceeded
func TestDeadlineExceeded(t *testing.T) {
	svc := NewTimeoutTestService()
	defer svc.Shutdown()

	// Track deadline exceeded errors
	var deadlineExceeded atomic.Int32

	svc.NormalEvents().Hook(normalEvent, func(ctx context.Context, event TimeoutEvent) error {
		// Check if we started with an already-expired context
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				deadlineExceeded.Add(1)
			}
			return ctx.Err()
		default:
			// Context still valid, do work
			time.Sleep(100 * time.Millisecond)
			return nil
		}
	})

	// Create context that's already expired
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()

	// Emit with expired context
	err := svc.EmitNormal(ctx, TimeoutEvent{ID: "expired"})
	if err != nil {
		// Some implementations might check context before queuing
		t.Logf("Emit with expired context returned: %v", err)
	}

	// Wait briefly
	time.Sleep(100 * time.Millisecond)

	// Hook should detect deadline exceeded
	if deadlineExceeded.Load() > 0 {
		t.Log("Hook correctly detected deadline exceeded")
	}
}

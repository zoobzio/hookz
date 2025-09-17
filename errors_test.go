package hookz

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test constants for error condition testing
const (
	TestSlowQueueEvent  Key = "test.queue.slow"
	TestEventLimitEvent Key = "test.limits.event"
	TestClosedEvent1    Key = "test.closed.event1"
	TestClosedEvent2    Key = "test.closed.event2"
	TestHookNotFound    Key = "test.hooknotfound"
	TestAlreadyUnhooked Key = "test.already.unhooked"
	TestOverflowEvent   Key = "test.overflow"
)

func TestQueueFullHandling(t *testing.T) {
	service := New[int](WithWorkers(1)) // Single worker
	defer service.Close()

	// Slow handler to saturate worker
	hook, err := service.Hook(TestSlowQueueEvent, func(ctx context.Context, n int) error {
		time.Sleep(100 * time.Millisecond)
		return nil
	})
	require.NoError(t, err)
	defer hook.Unhook()

	// Fill queue beyond capacity
	queueFullErrors := 0
	for i := 0; i < 100; i++ {
		if err := service.Emit(context.Background(), TestSlowQueueEvent, i); err != nil {
			if errors.Is(err, ErrQueueFull) {
				queueFullErrors++
			} else {
				t.Errorf("Unexpected error: %v", err)
			}
		}
	}

	assert.Greater(t, queueFullErrors, 0, "Should report queue full errors")
}

func TestResourceLimits(t *testing.T) {
	service := New[string]()
	defer service.Close()

	// Hit per-event limit
	hooks := make([]Hook, 0)
	for i := 0; i < 150; i++ { // Above maxHooksPerEvent (100)
		hook, err := service.Hook(TestEventLimitEvent, func(ctx context.Context, s string) error {
			return nil
		})

		if i < maxHooksPerEvent {
			assert.NoError(t, err)
			hooks = append(hooks, hook)
		} else {
			assert.ErrorIs(t, err, ErrTooManyHooks)
		}
	}

	// Test total hooks limit
	// Register hooks for different events until we hit total limit
	totalRegistered := len(hooks)
	eventNum := 1

	for totalRegistered < maxTotalHooks {
		hook, err := service.Hook(Key(string(TestEventLimitEvent)+"-"+string(rune(eventNum))), func(ctx context.Context, s string) error {
			return nil
		})
		if err != nil {
			assert.ErrorIs(t, err, ErrTooManyHooks)
			break
		}
		hooks = append(hooks, hook)
		totalRegistered++

		// Move to next event when we hit per-event limit
		if totalRegistered%maxHooksPerEvent == 0 {
			eventNum++
		}
	}

	// Should now be at total limit
	_, err := service.Hook(TestOverflowEvent, func(ctx context.Context, s string) error {
		return nil
	})
	assert.ErrorIs(t, err, ErrTooManyHooks)

	// Cleanup
	for _, hook := range hooks {
		hook.Unhook()
	}
}

func TestServiceClosedErrors(t *testing.T) {
	service := New[string]()

	// Close the service first
	err := service.Close()
	require.NoError(t, err)

	// All operations should return ErrServiceClosed
	t.Run("Hook registration fails", func(t *testing.T) {
		_, err := service.Hook(TestClosedEvent1, func(ctx context.Context, data string) error { return nil })
		assert.ErrorIs(t, err, ErrServiceClosed)
	})

	t.Run("Hook registration fails on closed service", func(t *testing.T) {
		_, err := service.Hook(TestClosedEvent2, func(ctx context.Context, data string) error { return nil })
		assert.ErrorIs(t, err, ErrServiceClosed)
	})

	t.Run("Emit fails", func(t *testing.T) {
		err := service.Emit(context.Background(), TestClosedEvent1, "data")
		assert.ErrorIs(t, err, ErrServiceClosed)
	})

	t.Run("Emit with background context fails", func(t *testing.T) {
		err := service.Emit(context.Background(), TestClosedEvent2, "data")
		assert.ErrorIs(t, err, ErrServiceClosed)
	})

	t.Run("Double close fails", func(t *testing.T) {
		err := service.Close()
		assert.ErrorIs(t, err, ErrAlreadyClosed)
	})
}

func TestHookNotFoundError(t *testing.T) {
	service := New[string]()
	defer service.Close()

	// Register and immediately clear the event
	hook, err := service.Hook(TestHookNotFound, func(ctx context.Context, data string) error { return nil })
	require.NoError(t, err)

	// Clear the event, making the hook invalid
	cleared := service.Clear(TestHookNotFound)
	assert.Equal(t, 1, cleared)

	// Now trying to unhook should return ErrHookNotFound
	err = hook.Unhook()
	assert.ErrorIs(t, err, ErrHookNotFound)
}

func TestAlreadyUnhookedError(t *testing.T) {
	service := New[string]()
	defer service.Close()

	hook, err := service.Hook(TestAlreadyUnhooked, func(ctx context.Context, data string) error { return nil })
	require.NoError(t, err)

	// First unhook should succeed
	err = hook.Unhook()
	require.NoError(t, err)

	// Second unhook should return ErrAlreadyUnhooked
	err = hook.Unhook()
	assert.ErrorIs(t, err, ErrAlreadyUnhooked)

	// Subsequent unhooks should also return ErrAlreadyUnhooked
	err = hook.Unhook()
	assert.ErrorIs(t, err, ErrAlreadyUnhooked)
}

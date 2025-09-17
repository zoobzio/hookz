package hookz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test constants for hook lifecycle operations
const (
	TestUnhookEvent    Key = "test.unhook"
	TestClearEvent1    Key = "test.clear.event1"
	TestClearEvent2    Key = "test.clear.event2"
	TestClearNonExist  Key = "test.clear.nonexistent"
	TestClearAllEvent1 Key = "test.clearall.event1"
	TestClearAllEvent2 Key = "test.clearall.event2"
	TestClearAllEvent3 Key = "test.clearall.event3"
)

func TestUnhook(t *testing.T) {
	service := New[string]()
	defer service.Close()

	called := make(chan bool, 1)
	hook, err := service.Hook(TestUnhookEvent, func(ctx context.Context, data string) error {
		called <- true
		return nil
	})
	require.NoError(t, err)

	// Unhook immediately
	err = hook.Unhook()
	require.NoError(t, err)

	// Double unhook should return error
	err = hook.Unhook()
	assert.ErrorIs(t, err, ErrAlreadyUnhooked)

	// Emit should not call the unhooked handler
	err = service.Emit(context.Background(), TestUnhookEvent, "data")
	require.NoError(t, err)

	// Should not be called (with small delay to allow any async processing)
	time.Sleep(10 * time.Millisecond)
	select {
	case <-called:
		t.Error("Hook should not have been called after unhook")
	default:
		// Good - not called
	}
}

func TestClear(t *testing.T) {
	service := New[int]()
	defer service.Close()

	// Register hooks for different events
	hook1, err := service.Hook(TestClearEvent1, func(ctx context.Context, data int) error { return nil })
	require.NoError(t, err)
	hook2, err := service.Hook(TestClearEvent1, func(ctx context.Context, data int) error { return nil })
	require.NoError(t, err)
	hook3, err := service.Hook(TestClearEvent2, func(ctx context.Context, data int) error { return nil })
	require.NoError(t, err)

	defer hook3.Unhook() // Only hook3 should remain

	// Clear event1 (should remove 2 hooks)
	cleared := service.Clear(TestClearEvent1)
	assert.Equal(t, 2, cleared)

	// Clear non-existent event
	cleared = service.Clear(TestClearNonExist)
	assert.Equal(t, 0, cleared)

	// hook1 and hook2 should now be invalid
	err = hook1.Unhook()
	assert.ErrorIs(t, err, ErrHookNotFound)
	err = hook2.Unhook()
	assert.ErrorIs(t, err, ErrHookNotFound)
}

func TestClearAll(t *testing.T) {
	service := New[string]()
	defer service.Close()

	// Register multiple hooks
	hook1, _ := service.Hook(TestClearAllEvent1, func(ctx context.Context, data string) error { return nil })
	hook2, _ := service.Hook(TestClearAllEvent2, func(ctx context.Context, data string) error { return nil })
	hook3, _ := service.Hook(TestClearAllEvent3, func(ctx context.Context, data string) error { return nil })

	// Clear all
	cleared := service.ClearAll()
	assert.Equal(t, 3, cleared)

	// All hooks should be invalid
	assert.ErrorIs(t, hook1.Unhook(), ErrHookNotFound)
	assert.ErrorIs(t, hook2.Unhook(), ErrHookNotFound)
	assert.ErrorIs(t, hook3.Unhook(), ErrHookNotFound)

	// Second clear should return 0
	cleared = service.ClearAll()
	assert.Equal(t, 0, cleared)
}

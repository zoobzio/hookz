package hookz

import (
	"context"
	"testing"
	"time"
)

func TestHookUnhook(t *testing.T) {
	service := New[string]()
	defer service.Close()

	called := make(chan struct{}, 1)
	hook, err := service.Hook("test.unhook", func(ctx context.Context, data string) error {
		called <- struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}

	// Unhook immediately
	if err := hook.Unhook(); err != nil {
		t.Fatalf("Failed to unhook: %v", err)
	}

	// Double unhook should return error
	if err := hook.Unhook(); !isAlreadyUnhookedError(err) {
		t.Errorf("Expected already unhooked error, got %v", err)
	}

	// Emit should not call the unhooked handler
	if err := service.Emit(context.Background(), "test.unhook", "data"); err != nil {
		t.Fatalf("Failed to emit: %v", err)
	}

	select {
	case <-called:
		t.Error("Hook should not have been called after unhook")
	case <-time.After(50 * time.Millisecond):
		// Success - not called
	}
}

func TestHookClear(t *testing.T) {
	service := New[int]()
	defer service.Close()

	// Register hooks for different events
	hook1, err := service.Hook("test.clear.event1", func(ctx context.Context, data int) error { return nil })
	if err != nil {
		t.Fatalf("Failed to register hook1: %v", err)
	}

	hook2, err := service.Hook("test.clear.event1", func(ctx context.Context, data int) error { return nil })
	if err != nil {
		t.Fatalf("Failed to register hook2: %v", err)
	}

	hook3, err := service.Hook("test.clear.event2", func(ctx context.Context, data int) error { return nil })
	if err != nil {
		t.Fatalf("Failed to register hook3: %v", err)
	}
	defer hook3.Unhook() // Only hook3 should remain valid

	// Clear event1 (should remove 2 hooks)
	cleared := service.Clear("test.clear.event1")
	if cleared != 2 {
		t.Errorf("Expected to clear 2 hooks, got %d", cleared)
	}

	// Clear non-existent event
	cleared = service.Clear("test.clear.nonexistent")
	if cleared != 0 {
		t.Errorf("Expected to clear 0 hooks for non-existent event, got %d", cleared)
	}

	// hook1 and hook2 should now be invalid
	if err := hook1.Unhook(); !isHookNotFoundError(err) {
		t.Errorf("Expected hook not found error for hook1, got %v", err)
	}

	if err := hook2.Unhook(); !isHookNotFoundError(err) {
		t.Errorf("Expected hook not found error for hook2, got %v", err)
	}
}

func TestHookClearAll(t *testing.T) {
	service := New[string]()
	defer service.Close()

	// Register multiple hooks
	hook1, err := service.Hook("test.clearall.event1", func(ctx context.Context, data string) error { return nil })
	if err != nil {
		t.Fatalf("Failed to register hook1: %v", err)
	}

	hook2, err := service.Hook("test.clearall.event2", func(ctx context.Context, data string) error { return nil })
	if err != nil {
		t.Fatalf("Failed to register hook2: %v", err)
	}

	hook3, err := service.Hook("test.clearall.event3", func(ctx context.Context, data string) error { return nil })
	if err != nil {
		t.Fatalf("Failed to register hook3: %v", err)
	}

	// Clear all
	cleared := service.ClearAll()
	if cleared != 3 {
		t.Errorf("Expected to clear 3 hooks, got %d", cleared)
	}

	// All hooks should be invalid
	if err := hook1.Unhook(); !isHookNotFoundError(err) {
		t.Errorf("Expected hook not found error for hook1, got %v", err)
	}

	if err := hook2.Unhook(); !isHookNotFoundError(err) {
		t.Errorf("Expected hook not found error for hook2, got %v", err)
	}

	if err := hook3.Unhook(); !isHookNotFoundError(err) {
		t.Errorf("Expected hook not found error for hook3, got %v", err)
	}

	// Second clear should return 0
	cleared = service.ClearAll()
	if cleared != 0 {
		t.Errorf("Expected 0 on second clear, got %d", cleared)
	}
}

func TestHookStructUsage(t *testing.T) {
	service := New[string]()
	defer service.Close()

	// Test Hook struct is properly created
	hook, err := service.Hook("test.struct", func(ctx context.Context, data string) error {
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}

	// Verify hook has unhook function (not nil)
	// We can't directly check the internal unhook field, but we can verify it works

	// Verify hook can be unhooked
	if err := hook.Unhook(); err != nil {
		t.Fatalf("Failed to unhook: %v", err)
	}

	// After unhooking, should get error on second unhook
	if err := hook.Unhook(); !isAlreadyUnhookedError(err) {
		t.Errorf("Expected already unhooked error, got %v", err)
	}
}

func TestHookAfterServiceClose(t *testing.T) {
	service := New[string]()

	// Register hook before close
	hook, err := service.Hook("test.beforeclose", func(ctx context.Context, data string) error {
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}

	// Close service
	if err := service.Close(); err != nil {
		t.Fatalf("Failed to close service: %v", err)
	}

	// Hook registered before close should still be unhookable
	if err := hook.Unhook(); err != nil {
		t.Errorf("Hook registered before close should be unhookable, got error: %v", err)
	}

	// But new hook registration should fail
	_, err = service.Hook("test.afterclose", func(ctx context.Context, data string) error {
		return nil
	})
	if !isServiceClosedError(err) {
		t.Errorf("Expected service closed error, got %v", err)
	}
}

// Helper functions to check error types without importing testify
func isAlreadyUnhookedError(err error) bool {
	if err == nil {
		return false
	}
	return err == ErrAlreadyUnhooked
}

func isHookNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	return err == ErrHookNotFound
}

func isServiceClosedError(err error) bool {
	if err == nil {
		return false
	}
	return err == ErrServiceClosed
}

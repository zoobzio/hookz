package main

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestInstantUIUpdate verifies UI updates happen within 50ms
func TestInstantUIUpdate(t *testing.T) {
	canvas := NewCanvas("test-canvas")
	defer canvas.Shutdown()
	
	action := Action{
		ID:        "test1",
		UserID:    "user1",
		Type:      ActionDraw,
		Target:    "shape1",
		Timestamp: time.Now(),
	}
	
	start := time.Now()
	err := canvas.HandleUserAction(context.Background(), action)
	elapsed := time.Since(start)
	
	if err != nil {
		t.Fatalf("Action failed: %v", err)
	}
	
	if elapsed > 50*time.Millisecond {
		t.Errorf("UI update took %v, expected < 50ms", elapsed)
	}
	
	// Verify action was processed
	if canvas.actionsProcessed.Load() != 1 {
		t.Error("Action was not counted")
	}
}

// TestAsyncOperations verifies background operations don't block UI
func TestAsyncOperations(t *testing.T) {
	canvas := NewCanvas("test-async")
	defer canvas.Shutdown()
	
	// Make server artificially slow
	canvas.server.latency = 2 * time.Second
	
	action := Action{
		ID:        "test2",
		UserID:    "user1",
		Type:      ActionMove,
		Target:    "shape1",
		Data:      Position{X: 100, Y: 200},
		Timestamp: time.Now(),
	}
	
	start := time.Now()
	err := canvas.HandleUserAction(context.Background(), action)
	elapsed := time.Since(start)
	
	if err != nil {
		t.Fatalf("Action failed: %v", err)
	}
	
	// UI should still update quickly despite slow server
	if elapsed > 100*time.Millisecond {
		t.Errorf("UI blocked by slow server: took %v", elapsed)
	}
	
	// Wait a bit and verify server sync happened
	time.Sleep(2100 * time.Millisecond)
	syncs, _ := canvas.server.GetStats()
	if syncs != 1 {
		t.Error("Server sync didn't complete")
	}
}

// TestConcurrentUsers simulates multiple users editing simultaneously
func TestConcurrentUsers(t *testing.T) {
	canvas := NewCanvas("test-concurrent")
	defer canvas.Shutdown()
	
	numUsers := 50
	actionsPerUser := 20
	
	var wg sync.WaitGroup
	var successCount atomic.Int32
	var maxLatency atomic.Int64
	
	for i := 0; i < numUsers; i++ {
		wg.Add(1)
		userID := fmt.Sprintf("user%d", i)
		
		go func(uid string) {
			defer wg.Done()
			
			for j := 0; j < actionsPerUser; j++ {
				action := generateRandomAction(uid)
				
				start := time.Now()
				err := canvas.HandleUserAction(context.Background(), action)
				latency := time.Since(start).Milliseconds()
				
				if err == nil {
					successCount.Add(1)
					
					// Track max latency
					for {
						current := maxLatency.Load()
						if latency <= current || maxLatency.CompareAndSwap(current, latency) {
							break
						}
					}
				}
			}
		}(userID)
	}
	
	wg.Wait()
	
	expectedActions := numUsers * actionsPerUser
	if int(successCount.Load()) != expectedActions {
		t.Errorf("Expected %d successful actions, got %d", expectedActions, successCount.Load())
	}
	
	// Check max latency
	if maxLatency.Load() > 200 {
		t.Errorf("Max latency %dms exceeds 200ms threshold", maxLatency.Load())
	}
	
	// Verify metrics
	metrics := canvas.GetMetrics()
	if metrics.ActionsProcessed != int64(expectedActions) {
		t.Errorf("Metrics show %d actions, expected %d", metrics.ActionsProcessed, expectedActions)
	}
}

// TestBroadcastExclusion verifies users don't receive their own events
func TestBroadcastExclusion(t *testing.T) {
	canvas := NewCanvas("test-broadcast")
	defer canvas.Shutdown()
	
	// Connect two users
	user1Ch := canvas.broadcast.Connect("user1")
	user2Ch := canvas.broadcast.Connect("user2")
	defer canvas.broadcast.Disconnect("user1")
	defer canvas.broadcast.Disconnect("user2")
	
	// User1 performs an action
	action := Action{
		ID:        "test3",
		UserID:    "user1",
		Type:      ActionDraw,
		Target:    "shape1",
		Timestamp: time.Now(),
	}
	
	canvas.HandleUserAction(context.Background(), action)
	
	// Wait for broadcasts
	time.Sleep(200 * time.Millisecond)
	
	// User1 should NOT receive their own event
	select {
	case event := <-user1Ch:
		t.Errorf("User1 received their own event: %v", event)
	default:
		// Good, no event
	}
	
	// User2 SHOULD receive the event
	select {
	case event := <-user2Ch:
		// Verify it's the right event
		if actionData, ok := event.Data.(Action); ok {
			if actionData.ID != action.ID {
				t.Errorf("Wrong action received: got %s, want %s", actionData.ID, action.ID)
			}
		}
	default:
		t.Error("User2 didn't receive broadcast")
	}
}

// TestHistoryBatching verifies history saves are batched efficiently
func TestHistoryBatching(t *testing.T) {
	canvas := NewCanvas("test-history")
	defer canvas.Shutdown()
	
	// Perform many actions quickly
	for i := 0; i < 100; i++ {
		action := Action{
			ID:        fmt.Sprintf("hist%d", i),
			UserID:    "user1",
			Type:      ActionDraw,
			Target:    fmt.Sprintf("shape%d", i),
			Timestamp: time.Now(),
		}
		canvas.HandleUserAction(context.Background(), action)
	}
	
	// History should batch these, not save individually
	// Wait for batch processing
	time.Sleep(1 * time.Second)
	
	saves := canvas.history.saves.Load()
	if saves >= 100 {
		t.Errorf("History saved %d times, should batch (expect < 100)", saves)
	}
}

// TestPermissionCaching verifies permissions are cached
func TestPermissionCaching(t *testing.T) {
	canvas := NewCanvas("test-perms")
	defer canvas.Shutdown()
	
	// Perform same action type multiple times
	for i := 0; i < 10; i++ {
		action := Action{
			ID:        fmt.Sprintf("perm%d", i),
			UserID:    "user1",
			Type:      ActionDelete,
			Target:    fmt.Sprintf("shape%d", i),
			Timestamp: time.Now(),
		}
		canvas.HandleUserAction(context.Background(), action)
	}
	
	// Wait for async processing
	time.Sleep(500 * time.Millisecond)
	
	// Should have cache hits
	checks := canvas.perms.checks.Load()
	hits := canvas.perms.cacheHits.Load()
	
	if checks < 10 {
		t.Errorf("Expected at least 10 permission checks, got %d", checks)
	}
	
	if hits < 5 {
		t.Errorf("Expected cache hits, got %d", hits)
	}
}

// TestGracefulShutdown ensures clean shutdown
func TestGracefulShutdown(t *testing.T) {
	canvas := NewCanvas("test-shutdown")
	
	// Start actions
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			action := generateRandomAction(fmt.Sprintf("user%d", id))
			canvas.HandleUserAction(context.Background(), action)
		}(i)
	}
	
	// Give time for processing to start
	time.Sleep(50 * time.Millisecond)
	
	// Shutdown while operations are in flight
	shutdownDone := make(chan struct{})
	go func() {
		canvas.Shutdown()
		close(shutdownDone)
	}()
	
	wg.Wait()
	
	// Shutdown should complete quickly
	select {
	case <-shutdownDone:
		// Success
	case <-time.After(3 * time.Second):
		t.Error("Shutdown took too long")
	}
}

// Benchmarks

func BenchmarkUIUpdate(b *testing.B) {
	canvas := NewCanvas("bench-ui")
	defer canvas.Shutdown()
	
	action := Action{
		ID:        "bench",
		UserID:    "user1",
		Type:      ActionDraw,
		Target:    "shape1",
		Timestamp: time.Now(),
	}
	
	ctx := context.Background()
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		canvas.HandleUserAction(ctx, action)
	}
}

func BenchmarkConcurrentActions(b *testing.B) {
	canvas := NewCanvas("bench-concurrent")
	defer canvas.Shutdown()
	
	ctx := context.Background()
	
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		id := 0
		for pb.Next() {
			action := generateRandomAction("bench-user")
			action.ID = fmt.Sprintf("bench%d", id)
			canvas.HandleUserAction(ctx, action)
			id++
		}
	})
}

func BenchmarkBroadcast(b *testing.B) {
	canvas := NewCanvas("bench-broadcast")
	defer canvas.Shutdown()
	
	// Connect 100 users
	for i := 0; i < 100; i++ {
		canvas.broadcast.Connect(fmt.Sprintf("user%d", i))
		defer canvas.broadcast.Disconnect(fmt.Sprintf("user%d", i))
	}
	
	broadcast := Broadcast{
		RoomID:    "bench-room",
		Event:     "test",
		Data:      "test data",
		Timestamp: time.Now(),
	}
	
	ctx := context.Background()
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		canvas.broadcast.Send(ctx, broadcast)
	}
}
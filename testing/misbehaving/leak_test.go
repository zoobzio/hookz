package main

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoobzio/hookz"
)

// TestHookAccumulationLeaks tests whether unused hooks accumulate without cleanup
func TestHookAccumulationLeaks(t *testing.T) {
	var m1, m2 runtime.MemStats
	
	// Baseline memory measurement
	runtime.GC()
	runtime.ReadMemStats(&m1)
	
	service := hookz.New[string]()
	
	// Simulate long-running service that accumulates hooks without cleanup
	// Stay within limits: maxHooksPerEvent = 100, maxTotalHooks = 10000
	var accumulatedHooks []hookz.Hook
	
	// Register hooks across multiple events to stay within per-event limit
	for i := 0; i < 5000; i++ {
		eventName := "user.event." + string(rune('A'+(i%50))) // 50 different events
		hook, err := service.Hook(eventName, func(ctx context.Context, data string) error {
			time.Sleep(1 * time.Millisecond) // Simulate work
			return nil
		})
		require.NoError(t, err)
		
		// CRITICAL: Not calling hook.Unhook() - simulating "unused" hooks that accumulate
		accumulatedHooks = append(accumulatedHooks, hook) // Store but never unhook
	}
	
	t.Logf("Successfully accumulated %d hooks across multiple events", len(accumulatedHooks))
	
	// Measure memory after hook accumulation  
	runtime.GC()
	runtime.ReadMemStats(&m2)
	
	service.Close()
	
	leaked := int64(m2.Alloc) - int64(m1.Alloc)
	t.Logf("Memory used by 5,000 accumulated hooks: %d bytes (%.2f MB)", leaked, float64(leaked)/(1024*1024))
	
	// Question: Is this a "leak" or expected behavior?
	// - Hooks are registered and stored in service.hooks map
	// - No automatic cleanup mechanism exists
	// - Memory usage grows linearly with hook count
	// - Only cleaned up when service.Close() is called
}

// TestHookMapGrowth examines internal hook storage patterns
func TestHookMapGrowth(t *testing.T) {
	service := hookz.New[string]()
	defer service.Close()
	
	// Access internal state to examine hook storage
	// hooks := &service
	
	// Register hooks across multiple events
	events := []string{"user.created", "user.updated", "user.deleted", "order.placed", "order.shipped"}
	
	totalHooks := 0
	// Stay within resource limits: maxHooksPerEvent = 100, maxTotalHooks = 10000
	for round := 0; round < 10; round++ {
		for _, event := range events {
			for i := 0; i < 10; i++ { // 10*5*10 = 500 total hooks, 10*10=100 per event
				hook, err := service.Hook(event, func(ctx context.Context, data string) error {
					return nil
				})
				require.NoError(t, err)
				totalHooks++
				
				// Don't unhook - let them accumulate
				_ = hook
			}
		}
	}
	
	// Examine hook distribution across events
	t.Logf("Total hooks registered: %d", totalHooks)
	t.Logf("Expected hooks per event: %d", totalHooks/len(events))
	
	// This demonstrates the storage pattern - hooks accumulate in map[string][]hookEntry
	// No automatic cleanup mechanism removes unused hooks
}

// TestLongRunningServicePattern simulates realistic usage
func TestLongRunningServicePattern(t *testing.T) {
	service := hookz.New[string]()
	defer service.Close()
	
	// Simulate plugin registration pattern - common in long-running services
	var registeredHooks []hookz.Hook
	
	// Phase 1: Plugin startup - register many hooks
	for pluginID := 0; pluginID < 100; pluginID++ {
		for eventType := 0; eventType < 10; eventType++ {
			event := "plugin.event." + string(rune('A'+eventType))
			hook, err := service.Hook(event, func(ctx context.Context, data string) error {
				// Plugin processing logic
				return nil
			})
			require.NoError(t, err)
			registeredHooks = append(registeredHooks, hook)
		}
	}
	
	t.Logf("Registered %d hooks across plugins", len(registeredHooks))
	
	// Phase 2: Service runs - hooks accumulate in memory
	// In real scenario: plugins might be disabled, features turned off,
	// but hooks remain registered consuming memory
	
	// Phase 3: Selective cleanup (what users must do manually)
	// Remove every other hook to simulate partial cleanup
	for i := 0; i < len(registeredHooks); i += 2 {
		err := registeredHooks[i].Unhook()
		require.NoError(t, err)
	}
	
	t.Logf("Cleaned up %d hooks, %d remain", len(registeredHooks)/2, len(registeredHooks)/2)
	
	// Without manual unhooking, hooks accumulate indefinitely
	// This is the "no automatic cleanup" concern
}
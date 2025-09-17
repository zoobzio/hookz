package main

import (
	"context"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoobzio/hookz"
)

// TestRealisticHookAccumulation demonstrates the "no automatic cleanup" concern
// in a realistic long-running service scenario
func TestRealisticHookAccumulation(t *testing.T) {
	service := hookz.New[string]()
	defer service.Close()
	
	var m1, m2, m3 runtime.MemStats
	
	// Phase 1: Baseline measurement
	runtime.GC()
	runtime.ReadMemStats(&m1)
	t.Logf("Baseline memory: %d bytes", m1.Alloc)
	
	// Phase 2: Simulate 6 months of plugin registrations
	var allHooks []hookz.Hook // Track all hooks (realistic: stored in plugin manager)
	
	// Each "month" adds plugins with hooks
	for month := 0; month < 3; month++ {
		t.Logf("Month %d: Adding plugins...", month+1)
		
		// Each month, 10 plugins register hooks
		for plugin := 0; plugin < 10; plugin++ {
			// Each plugin registers hooks for multiple events
			events := []string{
				"user.login", "user.logout", "user.created", 
				"order.created", "payment.processed", 
			}
			
			for _, event := range events {
				// Plugins register 1-2 hooks per event to stay within limits
				hookCount := (plugin%2)+1
				for i := 0; i < hookCount; i++ {
					hook, err := service.Hook(event, func(ctx context.Context, data string) error {
						// Plugin processing logic
						return nil
					})
					require.NoError(t, err)
					allHooks = append(allHooks, hook)
				}
			}
		}
	}
	
	runtime.GC()
	runtime.ReadMemStats(&m2)
	growthPhase2 := int64(m2.Alloc) - int64(m1.Alloc)
	t.Logf("After 3 months: %d hooks, memory growth: %d bytes (%.2f MB)", 
		len(allHooks), growthPhase2, float64(growthPhase2)/(1024*1024))
	
	// Phase 3: Some plugins are disabled/removed, but hooks remain
	// This demonstrates the "no automatic cleanup" issue
	// In reality, plugins get disabled but hooks aren't unregistered
	
	// Simulate only 50% of hooks being properly cleaned up
	halfCleanup := len(allHooks) / 2
	for i := 0; i < halfCleanup; i++ {
		err := allHooks[i].Unhook()
		require.NoError(t, err)
	}
	
	runtime.GC()
	runtime.ReadMemStats(&m3)
	remainingHooks := len(allHooks) - halfCleanup
	growthPhase3 := int64(m3.Alloc) - int64(m1.Alloc)
	
	t.Logf("After partial cleanup: %d hooks remain, memory usage: %d bytes (%.2f MB)",
		remainingHooks, growthPhase3, float64(growthPhase3)/(1024*1024))
	
	// Demonstrate the resource behavior
	memoryPerHook := float64(growthPhase3) / float64(remainingHooks)
	t.Logf("Estimated memory per remaining hook: %.2f bytes", memoryPerHook)
	
	// The key insight: Hooks accumulate in memory until explicitly unhooked
	// No automatic cleanup based on:
	// - Time (hooks don't expire)
	// - Usage (unused hooks aren't garbage collected)
	// - Events (event deletion doesn't cleanup hooks)
	
	t.Logf("CONCLUSION: Hooks require explicit cleanup via Unhook() or ClearAll()")
}

// TestHookLifecyclePatterns examines different cleanup scenarios
func TestHookLifecyclePatterns(t *testing.T) {
	service := hookz.New[string]()
	defer service.Close()
	
	t.Run("NoCleanup", func(t *testing.T) {
		// Register hooks but never clean up
		var hooks []hookz.Hook
		for i := 0; i < 1000; i++ {
			event := "test.event." + string(rune('A'+(i%10)))
			hook, err := service.Hook(event, func(ctx context.Context, data string) error {
				return nil
			})
			require.NoError(t, err)
			hooks = append(hooks, hook)
		}
		
		// Hooks accumulate in service.hooks map
		// Memory usage grows with hook count
		// No automatic cleanup mechanism
		
		t.Logf("Pattern: NoCleanup - %d hooks registered, consuming memory indefinitely", len(hooks))
	})
	
	t.Run("ManualCleanup", func(t *testing.T) {
		// Register and manually clean up
		var hooks []hookz.Hook
		for i := 0; i < 1000; i++ {
			event := "manual.event." + string(rune('A'+(i%10)))
			hook, err := service.Hook(event, func(ctx context.Context, data string) error {
				return nil
			})
			require.NoError(t, err)
			hooks = append(hooks, hook)
		}
		
		// Manual cleanup - user responsibility
		for _, hook := range hooks {
			err := hook.Unhook()
			require.NoError(t, err)
		}
		
		t.Logf("Pattern: ManualCleanup - %d hooks registered and cleaned up", len(hooks))
	})
	
	t.Run("ServiceCleanup", func(t *testing.T) {
		// Service cleanup clears all hooks
		for i := 0; i < 1000; i++ {
			event := "service.event." + string(rune('A'+(i%10)))
			_, err := service.Hook(event, func(ctx context.Context, data string) error {
				return nil
			})
			require.NoError(t, err)
		}
		
		// Bulk cleanup
		clearedCount := service.ClearAll()
		t.Logf("Pattern: ServiceCleanup - %d hooks cleared via ClearAll()", clearedCount)
	})
}

// TestMemoryBehaviorWithoutCleanup demonstrates the specific "leak" concern
func TestMemoryBehaviorWithoutCleanup(t *testing.T) {
	service := hookz.New[string]()
	defer service.Close()
	
	// The concern: In long-running services, hooks accumulate without automatic cleanup
	
	// Scenario 1: Feature flags - features get disabled but hooks remain
	featureHooks := make(map[string][]hookz.Hook)
	features := []string{"feature_a", "feature_b", "feature_c", "feature_d", "feature_e"}
	
	for _, feature := range features {
		var hooks []hookz.Hook
		for i := 0; i < 50; i++ {
			event := feature + ".event." + string(rune('0'+i%10))
			hook, err := service.Hook(event, func(ctx context.Context, data string) error {
				// Feature processing
				return nil
			})
			require.NoError(t, err)
			hooks = append(hooks, hook)
		}
		featureHooks[feature] = hooks
	}
	
	t.Logf("Registered hooks for %d features", len(features))
	
	// Features get disabled in production, but hooks aren't cleaned up
	// This is the "no automatic cleanup" problem
	
	// Only manual cleanup prevents the accumulation:
	// for _, hooks := range featureHooks {
	//     for _, hook := range hooks {
	//         hook.Unhook()
	//     }
	// }
	
	// Without manual cleanup, hooks persist until service shutdown
	t.Logf("Without cleanup: %d features * 50 hooks = %d hooks consuming memory", 
		len(features), len(features)*50)
}
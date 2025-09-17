package benchmarks

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zoobzio/hookz"
)

// BenchmarkConcurrentHookManagement tests hook registration and removal under concurrent load.
// This reveals mutex contention and O(n) removal performance impacts.
func BenchmarkConcurrentHookManagement(b *testing.B) {
	b.Run("registration_contention", func(b *testing.B) {
		service := hookz.New[TestEvent]()
		defer service.Close()
		
		var registrations atomic.Int64
		var failures atomic.Int64
		
		b.ReportAllocs()
		b.ResetTimer()
		
		// Test concurrent registration with immediate removal
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				hook, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
					return nil
				})
				if err != nil {
					failures.Add(1)
					continue
				}
				
				registrations.Add(1)
				
				// Immediate removal to test registration/removal churn
				err = hook.Unhook()
				if err != nil {
					failures.Add(1)
				}
			}
		})
		
		b.ReportMetric(float64(registrations.Load()), "successful_registrations")
		b.ReportMetric(float64(failures.Load()), "failures")
	})
	
	b.Run("unhook_with_many_hooks", func(b *testing.B) {
		service := hookz.New[TestEvent]()
		defer service.Close()
		
		// Pre-register hooks up to near the limit
		var hooks []hookz.Hook
		for i := 0; i < 95; i++ {
			hook, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
				return nil
			})
			if err != nil {
				b.Fatal(err)
			}
			hooks = append(hooks, hook)
		}
		
		b.ReportAllocs()
		b.ResetTimer()
		
		// Test removal performance when hook list is large (O(n) operation)
		for i := 0; i < b.N; i++ {
			// Register at near-limit
			hook, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
				return nil
			})
			if err != nil {
				b.Fatal(err)
			}
			
			b.StartTimer()
			err = hook.Unhook() // This is O(n) with current implementation
			b.StopTimer()
			
			if err != nil {
				b.Fatal(err)
			}
		}
		
		// Clean up
		for _, hook := range hooks {
			hook.Unhook()
		}
		
		b.ReportMetric(95, "existing_hooks")
	})
}

// BenchmarkHookLimitBehavior tests performance at resource limits
func BenchmarkHookLimitBehavior(b *testing.B) {
	b.Run("at_hook_limit", func(b *testing.B) {
		service := hookz.New[TestEvent]()
		defer service.Close()
		
		// Register exactly 100 hooks (the limit)
		var hooks []hookz.Hook
		for i := 0; i < 100; i++ {
			hook, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
				return nil
			})
			if err != nil {
				b.Fatal(err)
			}
			hooks = append(hooks, hook)
		}
		
		events := generateRealisticEvents(1000) // Fixed size to avoid overwhelming
		
		b.ReportAllocs()
		b.ResetTimer()
		
		var successful, dropped int64
		
		// Test emission performance with maximum hooks
		for i := 0; i < b.N; i++ {
			eventIndex := i % len(events)
			err := service.Emit(context.Background(), "test.event", events[eventIndex])
			if err == nil {
				successful++
			} else if errors.Is(err, hookz.ErrQueueFull) {
				dropped++
			} else {
				b.Fatal(err)
			}
			
			// Pace emission to allow processing
			if i%10 == 0 {
				time.Sleep(time.Microsecond)
			}
		}
		
		// Clean up
		for _, hook := range hooks {
			hook.Unhook()
		}
		
		b.ReportMetric(100, "hook_count")
		b.ReportMetric(float64(successful), "events_accepted")
		b.ReportMetric(float64(dropped), "events_dropped")
		if successful+dropped > 0 {
			dropRate := float64(dropped) / float64(successful+dropped) * 100
			b.ReportMetric(dropRate, "drop_rate_%")
		}
	})
	
	b.Run("exceed_hook_limit", func(b *testing.B) {
		service := hookz.New[TestEvent]()
		defer service.Close()
		
		// Register maximum hooks first
		var hooks []hookz.Hook
		for i := 0; i < 100; i++ {
			hook, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
				return nil
			})
			if err != nil {
				b.Fatal(err)
			}
			hooks = append(hooks, hook)
		}
		
		var rejections atomic.Int64
		
		b.ReportAllocs()
		b.ResetTimer()
		
		// Test registration failure handling when at limit
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
					return nil
				})
				if err != nil {
					rejections.Add(1)
				}
			}
		})
		
		// Clean up
		for _, hook := range hooks {
			hook.Unhook()
		}
		
		b.ReportMetric(float64(rejections.Load()), "rejections")
		b.ReportMetric(100, "existing_hooks")
	})
}

// BenchmarkHookRemovalPatterns tests different removal patterns
func BenchmarkHookRemovalPatterns(b *testing.B) {
	patterns := []struct {
		name     string
		register int
		remove   int
	}{
		{"remove_first", 50, 1},    // Remove first hook (best case)
		{"remove_middle", 50, 25},  // Remove middle hook (average case)
		{"remove_last", 50, 50},    // Remove last hook (worst case)
	}
	
	for _, pattern := range patterns {
		b.Run(pattern.name, func(b *testing.B) {
			service := hookz.New[TestEvent]()
			defer service.Close()
			
			b.ReportAllocs()
			b.ResetTimer()
			
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				
				// Register hooks
				var hooks []hookz.Hook
				for j := 0; j < pattern.register; j++ {
					hook, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
						return nil
					})
					if err != nil {
						b.Fatal(err)
					}
					hooks = append(hooks, hook)
				}
				
				b.StartTimer()
				
				// Remove specific hook based on pattern
				err := hooks[pattern.remove-1].Unhook()
				if err != nil {
					b.Fatal(err)
				}
				
				b.StopTimer()
				
				// Clean up remaining hooks
				for j, hook := range hooks {
					if j != pattern.remove-1 { // Skip already removed
						hook.Unhook()
					}
				}
			}
			
			b.ReportMetric(float64(pattern.register), "total_hooks")
			b.ReportMetric(float64(pattern.remove), "removed_position")
		})
	}
}

// BenchmarkHookChurn tests realistic registration/removal patterns
func BenchmarkHookChurn(b *testing.B) {
	service := hookz.New[TestEvent]()
	defer service.Close()
	
	// Maintain a base set of persistent hooks
	var persistentHooks []hookz.Hook
	for i := 0; i < 20; i++ {
		hook, err := service.Hook("persistent.event", func(ctx context.Context, evt TestEvent) error {
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
		persistentHooks = append(persistentHooks, hook)
	}
	
	events := generateRealisticEvents(1000) // Fixed size to avoid overwhelming
	
	b.ReportAllocs()
	b.ResetTimer()
	
	var persistentSuccessful, persistentDropped int64
	var tempSuccessful, tempDropped int64
	
	// Simulate realistic churn: register temporary hooks, emit events, remove hooks
	for i := 0; i < b.N; i++ {
		// Register 5 temporary hooks
		var tempHooks []hookz.Hook
		for j := 0; j < 5; j++ {
			hook, err := service.Hook("temp.event", func(ctx context.Context, evt TestEvent) error {
				return nil
			})
			if err != nil {
				b.Fatal(err)
			}
			tempHooks = append(tempHooks, hook)
		}
		
		eventIndex := i % len(events)
		
		// Emit to both persistent and temporary events
		err := service.Emit(context.Background(), "persistent.event", events[eventIndex])
		if err == nil {
			persistentSuccessful++
		} else if errors.Is(err, hookz.ErrQueueFull) {
			persistentDropped++
		} else {
			b.Fatal(err)
		}
		
		err = service.Emit(context.Background(), "temp.event", events[eventIndex])
		if err == nil {
			tempSuccessful++
		} else if errors.Is(err, hookz.ErrQueueFull) {
			tempDropped++
		} else {
			b.Fatal(err)
		}
		
		// Remove temporary hooks
		for _, hook := range tempHooks {
			err := hook.Unhook()
			if err != nil {
				b.Fatal(err)
			}
		}
		
		// Pace iteration to allow processing
		if i%10 == 0 {
			time.Sleep(time.Microsecond)
		}
	}
	
	// Clean up persistent hooks
	for _, hook := range persistentHooks {
		hook.Unhook()
	}
	
	b.ReportMetric(20, "persistent_hooks")
	b.ReportMetric(5, "temp_hooks_per_cycle")
	b.ReportMetric(float64(persistentSuccessful), "persistent_events_accepted")
	b.ReportMetric(float64(persistentDropped), "persistent_events_dropped")
	b.ReportMetric(float64(tempSuccessful), "temp_events_accepted")
	b.ReportMetric(float64(tempDropped), "temp_events_dropped")
}
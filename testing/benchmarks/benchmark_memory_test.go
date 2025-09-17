package benchmarks

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/zoobzio/hookz"
)

// BenchmarkMemoryPressure tests memory behavior with large payloads and many hooks.
// This reveals memory scaling characteristics and potential leak patterns.
func BenchmarkMemoryPressure(b *testing.B) {
	payloadSizes := []int{256, 1024, 4096, 16384, 65536} // From small to large
	
	for _, size := range payloadSizes {
		b.Run(fmt.Sprintf("payload_%dB", size), func(b *testing.B) {
			service := hookz.New[TestEvent](
				hookz.WithWorkers(10),
				hookz.WithQueueSize(30), // Larger queue for memory tests
			)
			defer service.Close()
			
			// Force GC and get baseline memory
			runtime.GC()
			runtime.GC() // Call twice to ensure cleanup
			var m1 runtime.MemStats
			runtime.ReadMemStats(&m1)
			
			// Register 50 hooks (half the limit) to test memory scaling
			for i := 0; i < 50; i++ {
				capturedI := i // Capture for closure
				_, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
					// Hold reference briefly to simulate processing
					_ = len(evt.Payload)
					_ = capturedI // Use captured variable to prevent optimization
					// Small processing delay to keep events in memory longer
					time.Sleep(time.Microsecond)
					return nil
				})
				if err != nil {
					b.Fatal(err)
				}
			}
			
			// Generate events with specified payload size - fixed count to avoid overwhelming
			events := generateFixedSizeEvents(1000, size)
			eventIndex := 0
			
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			
			var successful, dropped int64
			
			for i := 0; i < b.N; i++ {
				err := service.Emit(context.Background(), "test.event", events[eventIndex])
				if err == nil {
					successful++
				} else if errors.Is(err, hookz.ErrQueueFull) {
					dropped++
				} else {
					b.Fatal(err)
				}
				
				eventIndex = (eventIndex + 1) % len(events)
				
				// Pace emission to allow processing and prevent overwhelming
				if i%10 == 0 {
					time.Sleep(time.Microsecond)
				}
			}
			
			b.StopTimer()
			
			// Wait for processing to complete
			time.Sleep(50 * time.Millisecond)
			
			// Force GC and measure final memory
			runtime.GC()
			runtime.GC()
			var m2 runtime.MemStats
			runtime.ReadMemStats(&m2)
			
			// Calculate memory growth
			heapGrowth := int64(m2.HeapAlloc) - int64(m1.HeapAlloc)
			if heapGrowth < 0 {
				heapGrowth = 0 // GC might have reduced overall heap
			}
			
			b.ReportMetric(float64(heapGrowth)/float64(successful), "heap_growth_bytes/successful_op")
			b.ReportMetric(float64(m2.Alloc-m1.Alloc)/float64(successful), "net_alloc_bytes/successful_op")
			b.ReportMetric(float64(size), "payload_size_bytes")
			b.ReportMetric(float64(successful), "events_accepted")
			b.ReportMetric(float64(dropped), "events_dropped")
			if successful+dropped > 0 {
				dropRate := float64(dropped) / float64(successful+dropped) * 100
				b.ReportMetric(dropRate, "drop_rate_%")
			}
		})
	}
}

// BenchmarkMemoryManyHooks tests memory usage with maximum hook count
func BenchmarkMemoryManyHooks(b *testing.B) {
	service := hookz.New[TestEvent](
		hookz.WithWorkers(20),
		hookz.WithQueueSize(50), // Larger queue for many hooks
	)
	defer service.Close()
	
	// Force GC and baseline
	runtime.GC()
	runtime.GC()
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)
	
	// Register maximum hooks (100 per event limit)
	for i := 0; i < 100; i++ {
		capturedI := i
		// Create some variety in closure captures to test memory impact
		extraData := make([]byte, 64) // Small amount of data per hook
		for j := range extraData {
			extraData[j] = byte(i + j)
		}
		
		_, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
			_ = len(evt.Payload) + capturedI + len(extraData)
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	
	// Measure memory after hook registration
	runtime.GC()
	runtime.GC()
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)
	
	hookRegistrationMemory := int64(m2.HeapAlloc) - int64(m1.HeapAlloc)
	
	events := generateRealisticEvents(1000) // Fixed size to avoid overwhelming
	eventIndex := 0
	
	b.ReportAllocs()
	b.ResetTimer()
	
	var successful, dropped int64
	
	for i := 0; i < b.N; i++ {
		err := service.Emit(context.Background(), "test.event", events[eventIndex])
		if err == nil {
			successful++
		} else if errors.Is(err, hookz.ErrQueueFull) {
			dropped++
		} else {
			b.Fatal(err)
		}
		
		eventIndex = (eventIndex + 1) % len(events)
		
		// Pace emission for sustainable measurement
		if i%10 == 0 {
			time.Sleep(time.Microsecond)
		}
	}
	
	b.StopTimer()
	
	// Wait for processing
	time.Sleep(100 * time.Millisecond)
	
	// Final memory measurement
	runtime.GC()
	runtime.GC()
	var m3 runtime.MemStats
	runtime.ReadMemStats(&m3)
	
	totalMemoryGrowth := int64(m3.HeapAlloc) - int64(m1.HeapAlloc)
	emissionMemoryGrowth := int64(m3.HeapAlloc) - int64(m2.HeapAlloc)
	
	b.ReportMetric(float64(hookRegistrationMemory), "hook_registration_bytes")
	if successful > 0 {
		b.ReportMetric(float64(emissionMemoryGrowth)/float64(successful), "emission_bytes/successful_op")
	}
	b.ReportMetric(float64(totalMemoryGrowth), "total_memory_growth_bytes")
	b.ReportMetric(100, "hook_count")
	b.ReportMetric(float64(successful), "events_accepted")
	b.ReportMetric(float64(dropped), "events_dropped")
}

// BenchmarkMemoryLeak tests for potential memory leaks over time
func BenchmarkMemoryLeak(b *testing.B) {
	// Run this benchmark with longer duration to detect leaks
	if testing.Short() {
		b.Skip("Skipping memory leak test in short mode")
	}
	
	service := hookz.New[TestEvent](
		hookz.WithWorkers(10),
		hookz.WithQueueSize(30),
	)
	defer service.Close()
	
	// Register moderate number of hooks
	for i := 0; i < 25; i++ {
		_, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
			// Minimal processing
			_ = len(evt.Payload)
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	
	events := generateRealisticEvents(1000) // Reuse events to avoid allocation noise
	eventIndex := 0
	
	// Take memory snapshots at intervals
	var memSnapshots []uint64
	snapshotInterval := b.N / 10
	if snapshotInterval == 0 {
		snapshotInterval = 1
	}
	
	b.ReportAllocs()
	b.ResetTimer()
	
	var successful, dropped int64
	
	for i := 0; i < b.N; i++ {
		err := service.Emit(context.Background(), "test.event", events[eventIndex])
		if err == nil {
			successful++
		} else if errors.Is(err, hookz.ErrQueueFull) {
			dropped++
		}
		
		eventIndex = (eventIndex + 1) % len(events)
		
		// Pace emission to allow meaningful leak detection
		if i%20 == 0 {
			time.Sleep(time.Microsecond)
		}
		
		// Take memory snapshot periodically
		if i%snapshotInterval == 0 {
			runtime.GC()
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			memSnapshots = append(memSnapshots, m.HeapAlloc)
		}
	}
	
	// Wait for processing to complete
	time.Sleep(200 * time.Millisecond)
	runtime.GC()
	runtime.GC()
	
	// Analyze memory trend
	if len(memSnapshots) >= 3 {
		start := memSnapshots[0]
		mid := memSnapshots[len(memSnapshots)/2]
		end := memSnapshots[len(memSnapshots)-1]
		
		// Calculate memory growth rate
		if start > 0 {
			midGrowth := float64(mid-start) / float64(start) * 100
			endGrowth := float64(end-start) / float64(start) * 100
			
			b.ReportMetric(midGrowth, "memory_growth_mid_%")
			b.ReportMetric(endGrowth, "memory_growth_end_%")
		}
		
		b.ReportMetric(float64(start), "memory_start_bytes")
		b.ReportMetric(float64(end), "memory_end_bytes")
	}
	
	b.ReportMetric(float64(successful), "events_accepted")
	b.ReportMetric(float64(dropped), "events_dropped")
	if successful+dropped > 0 {
		dropRate := float64(dropped) / float64(successful+dropped) * 100
		b.ReportMetric(dropRate, "drop_rate_%")
	}
}

// BenchmarkMemoryAllocationRate measures allocations per successful operation
func BenchmarkMemoryAllocationRate(b *testing.B) {
	hookCounts := []int{1, 10, 50, 100}
	
	for _, hooks := range hookCounts {
		b.Run(fmt.Sprintf("hooks_%d", hooks), func(b *testing.B) {
			service := hookz.New[TestEvent](
				hookz.WithWorkers(20),
				hookz.WithQueueSize(50),
			)
			defer service.Close()
			
			// Register specified number of hooks
			for i := 0; i < hooks; i++ {
				capturedI := i
				_, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
					// Minimal work to prevent optimization
					_ = len(evt.Payload) + capturedI + evt.ID
					return nil
				})
				if err != nil {
					b.Fatal(err)
				}
			}
			
			events := generateRealisticEvents(1000)
			eventIndex := 0
			
			b.ReportAllocs()
			b.ResetTimer()
			
			var successful, dropped int64
			
			for i := 0; i < b.N; i++ {
				err := service.Emit(context.Background(), "test.event", events[eventIndex])
				if err == nil {
					successful++
				} else if errors.Is(err, hookz.ErrQueueFull) {
					dropped++
				} else {
					b.Fatal(err)
				}
				
				eventIndex = (eventIndex + 1) % len(events)
				
				// Pace emission to keep queue draining
				if i%20 == 0 {
					time.Sleep(time.Microsecond)
				}
			}
			
			b.ReportMetric(float64(hooks), "hook_count")
			b.ReportMetric(float64(successful), "events_accepted")
			b.ReportMetric(float64(dropped), "events_dropped")
			if successful+dropped > 0 {
				dropRate := float64(dropped) / float64(successful+dropped) * 100
				b.ReportMetric(dropRate, "drop_rate_%")
			}
		})
	}
}
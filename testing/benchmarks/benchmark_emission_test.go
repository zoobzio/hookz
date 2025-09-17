package benchmarks

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zoobzio/hookz"
)

// BenchmarkEmitScaling measures emission latency as hook count increases.
// This reveals mutex contention and scaling without processing completion waits.
func BenchmarkEmitScaling(b *testing.B) {
	hookCounts := []int{1, 5, 10, 25, 50, 100}
	
	for _, count := range hookCounts {
		b.Run(fmt.Sprintf("hooks_%d", count), func(b *testing.B) {
			service := hookz.New[TestEvent](
				hookz.WithWorkers(20),
				hookz.WithQueueSize(100), // Reasonable queue size
			)
			defer service.Close()
			
			// Register hooks with instant processing to keep queue draining
			for i := 0; i < count; i++ {
				hookIndex := i // Capture for closure
				_, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
					// Minimal CPU work to prevent optimization
					_ = len(evt.Payload) + hookIndex + evt.ID
					return nil
				})
				if err != nil {
					b.Fatal(err)
				}
			}
			
			// Pre-generate events - fixed small count to avoid excessive queue pressure
			events := generateRealisticEvents(1000)
			eventIndex := 0
			
			b.ReportAllocs()
			b.ResetTimer()
			
			var successful, dropped int64
			
			// Emit b.N events with pacing to allow queue to drain
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
				
				// Pace emission to prevent queue saturation
				if i%10 == 0 {
					time.Sleep(time.Microsecond)
				}
			}
			
			b.ReportMetric(float64(successful), "events_accepted")
			b.ReportMetric(float64(dropped), "events_dropped")
			if successful+dropped > 0 {
				dropRate := float64(dropped) / float64(successful+dropped) * 100
				b.ReportMetric(dropRate, "drop_rate_%")
			}
			b.ReportMetric(float64(count), "hook_count")
		})
	}
}

// BenchmarkEmitConcurrency tests emission under concurrent load
func BenchmarkEmitConcurrency(b *testing.B) {
	service := hookz.New[TestEvent](
		hookz.WithWorkers(20),
		hookz.WithQueueSize(100),
	)
	defer service.Close()
	
	// Register 25 hooks - enough to show contention
	for i := 0; i < 25; i++ {
		hookIndex := i
		_, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
			// Minimal work to prevent optimization
			_ = len(evt.Payload) + hookIndex + evt.ID
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	
	events := generateRealisticEvents(1000)
	
	b.ReportAllocs()
	b.ResetTimer()
	
	var successful, dropped atomic.Int64
	
	// Use RunParallel to test concurrent emission
	b.RunParallel(func(pb *testing.PB) {
		eventIndex := 0
		for pb.Next() {
			if eventIndex >= len(events) {
				eventIndex = 0
			}
			err := service.Emit(context.Background(), "test.event", events[eventIndex])
			if err == nil {
				successful.Add(1)
			} else if errors.Is(err, hookz.ErrQueueFull) {
				dropped.Add(1)
			}
			eventIndex++
		}
	})
	
	successfulCount := successful.Load()
	droppedCount := dropped.Load()
	total := successfulCount + droppedCount
	
	b.ReportMetric(float64(successfulCount), "events_accepted")
	b.ReportMetric(float64(droppedCount), "events_dropped")
	if total > 0 {
		dropRate := float64(droppedCount) / float64(total) * 100
		b.ReportMetric(dropRate, "drop_rate_%")
	}
}

// BenchmarkEmitMultipleEvents tests performance with multiple event types
func BenchmarkEmitMultipleEvents(b *testing.B) {
	eventTypes := []string{"user.created", "order.placed", "payment.processed", "notification.sent"}
	
	for _, eventCount := range []int{1, 4, 8} {
		b.Run(fmt.Sprintf("events_%d", eventCount), func(b *testing.B) {
			service := hookz.New[TestEvent](
				hookz.WithWorkers(20),
				hookz.WithQueueSize(100),
			)
			defer service.Close()
			
			// Register 10 hooks per event type
			for i := 0; i < eventCount; i++ {
				eventType := eventTypes[i%len(eventTypes)]
				for j := 0; j < 10; j++ {
					hookIndex := i*10 + j
					_, err := service.Hook(eventType, func(ctx context.Context, evt TestEvent) error {
						_ = len(evt.Payload) + hookIndex + evt.ID
						return nil
					})
					if err != nil {
						b.Fatal(err)
					}
				}
			}
			
			events := generateRealisticEvents(1000)
			
			b.ReportAllocs()
			b.ResetTimer()
			
			var successful, dropped int64
			
			for i := 0; i < b.N; i++ {
				// Round-robin through event types
				eventType := eventTypes[i%len(eventTypes)]
				eventIndex := i % len(events)
				
				err := service.Emit(context.Background(), eventType, events[eventIndex])
				if err == nil {
					successful++
				} else if errors.Is(err, hookz.ErrQueueFull) {
					dropped++
				} else {
					b.Fatal(err)
				}
				
				// Pace emission
				if i%10 == 0 {
					time.Sleep(time.Microsecond)
				}
			}
			
			b.ReportMetric(float64(eventCount), "event_types")
			b.ReportMetric(float64(eventCount*10), "total_hooks")
			b.ReportMetric(float64(successful), "events_accepted")
			b.ReportMetric(float64(dropped), "events_dropped")
			if successful+dropped > 0 {
				dropRate := float64(dropped) / float64(successful+dropped) * 100
				b.ReportMetric(dropRate, "drop_rate_%")
			}
		})
	}
}

// BenchmarkEmissionLatency measures pure emission latency without queue pressure
func BenchmarkEmissionLatency(b *testing.B) {
	service := hookz.New[TestEvent](
		hookz.WithWorkers(50), // Plenty of workers
		hookz.WithQueueSize(200), // Large queue
	)
	defer service.Close()
	
	// Single fast hook to keep queue draining
	_, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
		return nil // Instant processing
	})
	if err != nil {
		b.Fatal(err)
	}
	
	events := generateRealisticEvents(100)
	latencies := make([]time.Duration, 0, b.N)
	
	b.ReportAllocs()
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		eventIndex := i % len(events)
		
		start := time.Now()
		err := service.Emit(context.Background(), "test.event", events[eventIndex])
		latency := time.Since(start)
		
		if err == nil {
			latencies = append(latencies, latency)
		}
		
		// Minimal pacing to prevent overwhelming
		if i%100 == 0 {
			time.Sleep(time.Microsecond)
		}
	}
	
	if len(latencies) > 0 {
		// Calculate percentiles
		p50 := percentile(latencies, 50)
		p95 := percentile(latencies, 95)
		p99 := percentile(latencies, 99)
		
		b.ReportMetric(float64(p50.Nanoseconds()), "latency_p50_ns")
		b.ReportMetric(float64(p95.Nanoseconds()), "latency_p95_ns")
		b.ReportMetric(float64(p99.Nanoseconds()), "latency_p99_ns")
		b.ReportMetric(float64(len(latencies)), "successful_emissions")
	}
}

// percentile calculates the nth percentile of durations
func percentile(durations []time.Duration, p int) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	
	// Sort durations to get actual percentiles
	sort.Slice(durations, func(i, j int) bool {
		return durations[i] < durations[j]
	})
	
	// Simple percentile calculation - good enough for benchmarks
	index := (len(durations) * p) / 100
	if index >= len(durations) {
		index = len(durations) - 1
	}
	
	return durations[index]
}
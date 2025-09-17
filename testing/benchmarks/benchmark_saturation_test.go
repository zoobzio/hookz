package benchmarks

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zoobzio/hookz"
)

// BenchmarkQueueSaturation measures system behavior when worker queue fills up.
// This benchmark reveals the actual drop rate and performance degradation under load.
func BenchmarkQueueSaturation(b *testing.B) {
	configs := []struct {
		workers   int
		queueSize int
		delay     time.Duration
	}{
		{2, 4, 10 * time.Millisecond},   // Severe constraint - should drop events
		{5, 10, 5 * time.Millisecond},   // Moderate constraint - some drops
		{10, 20, 2 * time.Millisecond},  // Light constraint - minimal drops
		{20, 40, 1 * time.Millisecond},  // Generous capacity - no drops expected
	}
	
	for _, cfg := range configs {
		b.Run(fmt.Sprintf("w%d_q%d_d%dms", cfg.workers, cfg.queueSize, cfg.delay/time.Millisecond), func(b *testing.B) {
			service := hookz.New[TestEvent](
				hookz.WithWorkers(cfg.workers),
				hookz.WithQueueSize(cfg.queueSize),
			)
			defer service.Close()
			
			// Register slow hook that creates backpressure
			_, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
				time.Sleep(cfg.delay)
				return nil
			})
			if err != nil {
				b.Fatal(err)
			}
			
			// Generate events once to avoid allocation overhead in measurement
			events := generateRealisticEvents(b.N)
			
			b.ReportAllocs()
			b.ResetTimer()
			
			var dropped atomic.Int64
			var successful atomic.Int64
			
			// Use RunParallel to simulate realistic concurrent load
			b.RunParallel(func(pb *testing.PB) {
				eventIndex := 0
				for pb.Next() {
					if eventIndex >= len(events) {
						eventIndex = 0 // Wrap around if needed
					}
					err := service.Emit(context.Background(), "test.event", events[eventIndex])
					if errors.Is(err, hookz.ErrQueueFull) {
						dropped.Add(1)
					} else if err == nil {
						successful.Add(1)
					}
					eventIndex++
				}
			})
			
			// Report custom metrics that matter for capacity planning
			totalEvents := float64(dropped.Load() + successful.Load())
			if totalEvents > 0 {
				dropRate := float64(dropped.Load()) * 100 / totalEvents
				b.ReportMetric(dropRate, "drop_rate_%")
				b.ReportMetric(float64(successful.Load()), "successful_events")
				b.ReportMetric(float64(dropped.Load()), "dropped_events")
			}
		})
	}
}

// BenchmarkSaturationProgressive tests how drop rate changes with increasing load
func BenchmarkSaturationProgressive(b *testing.B) {
	// Fixed small configuration to guarantee saturation
	service := hookz.New[TestEvent](
		hookz.WithWorkers(3),
		hookz.WithQueueSize(6),
	)
	defer service.Close()
	
	// Slow processing to ensure queue fills
	_, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
		time.Sleep(20 * time.Millisecond)
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	
	loadLevels := []int{10, 50, 100, 200, 500} // Events per burst
	
	for _, load := range loadLevels {
		b.Run(fmt.Sprintf("burst_%d", load), func(b *testing.B) {
			events := generateRealisticEvents(load)
			
			b.ReportAllocs()
			b.ResetTimer()
			
			var dropped atomic.Int64
			
			for i := 0; i < b.N; i++ {
				// Send burst of events
				for _, event := range events {
					err := service.Emit(context.Background(), "test.event", event)
					if errors.Is(err, hookz.ErrQueueFull) {
						dropped.Add(1)
					}
				}
				
				// Small pause between bursts to let queue drain partially
				time.Sleep(5 * time.Millisecond)
			}
			
			totalEvents := float64(b.N * load)
			dropRate := float64(dropped.Load()) * 100 / totalEvents
			b.ReportMetric(dropRate, "drop_rate_%")
			b.ReportMetric(float64(load), "burst_size")
		})
	}
}
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

// BenchmarkSustainableRate tests sustainable event rates without drops.
// This measures real-world streaming capacity under controlled conditions.
func BenchmarkSustainableRate(b *testing.B) {
	// Test different rates to find sustainable throughput thresholds
	rates := []struct {
		name         string
		eventsPerSec int
		interval     time.Duration
	}{
		{"rate_1k_per_sec", 1000, time.Microsecond * 1000},
		{"rate_5k_per_sec", 5000, time.Microsecond * 200},
		{"rate_10k_per_sec", 10000, time.Microsecond * 100},
		{"rate_50k_per_sec", 50000, time.Microsecond * 20},
		{"rate_100k_per_sec", 100000, time.Microsecond * 10},
	}

	for _, rate := range rates {
		b.Run(rate.name, func(b *testing.B) {
			service := hookz.New[TestEvent](
				hookz.WithWorkers(20),
				hookz.WithQueueSize(100),
			)
			defer service.Close()

			// Register moderate hooks with fast processing
			for i := 0; i < 25; i++ {
				hookIndex := i
				_, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
					// Fast processing to keep up with emission
					_ = len(evt.Payload) + hookIndex + evt.ID
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
			ticker := time.NewTicker(rate.interval)
			defer ticker.Stop()

			emitted := 0
			for emitted < b.N {
				<-ticker.C

				err := service.Emit(context.Background(), "test.event", events[eventIndex])
				if err == nil {
					successful++
					emitted++
				} else if errors.Is(err, hookz.ErrQueueFull) {
					dropped++
					emitted++ // Count as attempt
				} else {
					b.Fatal(err)
				}

				eventIndex = (eventIndex + 1) % len(events)
			}

			b.ReportMetric(float64(rate.eventsPerSec), "target_rate_per_sec")
			b.ReportMetric(float64(successful), "events_accepted")
			b.ReportMetric(float64(dropped), "events_dropped")
			if successful+dropped > 0 {
				dropRate := float64(dropped) / float64(successful+dropped) * 100
				b.ReportMetric(dropRate, "drop_rate_%")
			}
		})
	}
}

// BenchmarkBurstCapacity tests how well the system handles traffic bursts
func BenchmarkBurstCapacity(b *testing.B) {
	burstSizes := []int{10, 50, 100, 200, 500}

	for _, burst := range burstSizes {
		b.Run(fmt.Sprintf("burst_%d", burst), func(b *testing.B) {
			service := hookz.New[TestEvent](
				hookz.WithWorkers(20),
				hookz.WithQueueSize(100),
			)
			defer service.Close()

			// Fast processing hook to drain queue quickly
			_, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
				time.Sleep(100 * time.Microsecond) // Small processing time
				return nil
			})
			if err != nil {
				b.Fatal(err)
			}

			events := generateRealisticEvents(burst)

			b.ReportAllocs()
			b.ResetTimer()

			var totalAccepted, totalDropped int64

			iterations := b.N / burst
			if iterations == 0 {
				iterations = 1
			}

			for i := 0; i < iterations; i++ {
				burstAccepted := 0
				burstDropped := 0

				// Send burst of events as fast as possible
				for _, event := range events {
					err := service.Emit(context.Background(), "test.event", event)
					if err == nil {
						burstAccepted++
					} else if errors.Is(err, hookz.ErrQueueFull) {
						burstDropped++
					} else {
						b.Fatal(err)
					}
				}

				totalAccepted += int64(burstAccepted)
				totalDropped += int64(burstDropped)

				// Recovery time between bursts
				time.Sleep(50 * time.Millisecond)
			}

			b.ReportMetric(float64(burst), "burst_size")
			b.ReportMetric(float64(totalAccepted), "total_accepted")
			b.ReportMetric(float64(totalDropped), "total_dropped")
			if totalAccepted+totalDropped > 0 {
				dropRate := float64(totalDropped) / float64(totalAccepted+totalDropped) * 100
				b.ReportMetric(dropRate, "drop_rate_%")
			}
			b.ReportMetric(float64(iterations), "burst_count")
		})
	}
}

// BenchmarkStreamingPatterns tests realistic traffic patterns
func BenchmarkStreamingPatterns(b *testing.B) {
	patterns := []struct {
		name    string
		pattern func(service *hookz.Hooks[TestEvent], events []TestEvent, n int) (int64, int64)
	}{
		{
			"steady_stream",
			func(service *hookz.Hooks[TestEvent], events []TestEvent, n int) (int64, int64) {
				var successful, dropped int64
				for i := 0; i < n; i++ {
					eventIndex := i % len(events)
					err := service.Emit(context.Background(), "test.event", events[eventIndex])
					if err == nil {
						successful++
					} else if errors.Is(err, hookz.ErrQueueFull) {
						dropped++
					}
					time.Sleep(100 * time.Microsecond) // 10k/sec steady rate
				}
				return successful, dropped
			},
		},
		{
			"periodic_burst",
			func(service *hookz.Hooks[TestEvent], events []TestEvent, n int) (int64, int64) {
				var successful, dropped int64
				cycles := n / 20 // 20 events per cycle
				if cycles == 0 {
					cycles = 1
				}
				
				for i := 0; i < cycles; i++ {
					// Burst of 10 events
					for j := 0; j < 10; j++ {
						eventIndex := (i*10 + j) % len(events)
						err := service.Emit(context.Background(), "test.event", events[eventIndex])
						if err == nil {
							successful++
						} else if errors.Is(err, hookz.ErrQueueFull) {
							dropped++
						}
					}
					// Pause between bursts
					time.Sleep(5 * time.Millisecond)
				}
				return successful, dropped
			},
		},
		{
			"random_intervals",
			func(service *hookz.Hooks[TestEvent], events []TestEvent, n int) (int64, int64) {
				var successful, dropped int64
				for i := 0; i < n; i++ {
					eventIndex := i % len(events)
					err := service.Emit(context.Background(), "test.event", events[eventIndex])
					if err == nil {
						successful++
					} else if errors.Is(err, hookz.ErrQueueFull) {
						dropped++
					}
					// Random delay 0-2ms
					delay := time.Duration(i%2000) * time.Microsecond
					time.Sleep(delay)
				}
				return successful, dropped
			},
		},
	}

	for _, pattern := range patterns {
		b.Run(pattern.name, func(b *testing.B) {
			service := hookz.New[TestEvent](
				hookz.WithWorkers(15),
				hookz.WithQueueSize(75),
			)
			defer service.Close()

			// Register hooks with moderate processing time
			for i := 0; i < 20; i++ {
				hookIndex := i
				_, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
					time.Sleep(500 * time.Microsecond) // Realistic processing
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

			successful, dropped := pattern.pattern(service, events, b.N)

			b.ReportMetric(float64(successful), "events_accepted")
			b.ReportMetric(float64(dropped), "events_dropped")
			if successful+dropped > 0 {
				dropRate := float64(dropped) / float64(successful+dropped) * 100
				b.ReportMetric(dropRate, "drop_rate_%")
			}
		})
	}
}

// BenchmarkQueueDepthImpact tests how queue size affects performance
func BenchmarkQueueDepthImpact(b *testing.B) {
	queueSizes := []int{10, 25, 50, 100, 200}

	for _, queueSize := range queueSizes {
		b.Run(fmt.Sprintf("queue_%d", queueSize), func(b *testing.B) {
			service := hookz.New[TestEvent](
				hookz.WithWorkers(10),
				hookz.WithQueueSize(queueSize),
			)
			defer service.Close()

			// Slow processing to create queue pressure
			_, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
				time.Sleep(time.Millisecond) // 1ms processing
				return nil
			})
			if err != nil {
				b.Fatal(err)
			}

			events := generateRealisticEvents(1000)
			eventIndex := 0

			b.ReportAllocs()
			b.ResetTimer()

			var successful, dropped int64
			start := time.Now()

			// Emit at steady rate to test queue behavior
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

				// Pace to create consistent pressure
				if i%10 == 0 {
					time.Sleep(500 * time.Microsecond)
				}
			}

			elapsed := time.Since(start)
			throughput := float64(successful) / elapsed.Seconds()

			b.ReportMetric(float64(queueSize), "queue_size")
			b.ReportMetric(throughput, "throughput_per_sec")
			b.ReportMetric(float64(successful), "events_accepted")
			b.ReportMetric(float64(dropped), "events_dropped")
			if successful+dropped > 0 {
				dropRate := float64(dropped) / float64(successful+dropped) * 100
				b.ReportMetric(dropRate, "drop_rate_%")
			}
		})
	}
}

// BenchmarkConcurrentStreams tests multiple concurrent event streams
func BenchmarkConcurrentStreams(b *testing.B) {
	streamCounts := []int{1, 2, 4, 8}

	for _, streams := range streamCounts {
		b.Run(fmt.Sprintf("streams_%d", streams), func(b *testing.B) {
			service := hookz.New[TestEvent](
				hookz.WithWorkers(20),
				hookz.WithQueueSize(100),
			)
			defer service.Close()

			// Register hooks for each stream
			for i := 0; i < streams; i++ {
				streamID := i
				eventType := fmt.Sprintf("stream.%d", i)
				
				for j := 0; j < 10; j++ { // 10 hooks per stream
					hookIndex := j
					_, err := service.Hook(eventType, func(ctx context.Context, evt TestEvent) error {
						time.Sleep(200 * time.Microsecond) // Processing time
						_ = len(evt.Payload) + streamID + hookIndex + evt.ID
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

			var successful, dropped atomic.Int64

			// Run concurrent streams
			b.RunParallel(func(pb *testing.PB) {
				streamID := 0
				eventIndex := 0
				
				for pb.Next() {
					eventType := fmt.Sprintf("stream.%d", streamID%streams)
					err := service.Emit(context.Background(), eventType, events[eventIndex])
					
					if err == nil {
						successful.Add(1)
					} else if errors.Is(err, hookz.ErrQueueFull) {
						dropped.Add(1)
					}

					streamID++
					eventIndex = (eventIndex + 1) % len(events)
				}
			})

			successfulCount := successful.Load()
			droppedCount := dropped.Load()

			b.ReportMetric(float64(streams), "concurrent_streams")
			b.ReportMetric(float64(successfulCount), "events_accepted")
			b.ReportMetric(float64(droppedCount), "events_dropped")
			if successfulCount+droppedCount > 0 {
				dropRate := float64(droppedCount) / float64(successfulCount+droppedCount) * 100
				b.ReportMetric(dropRate, "drop_rate_%")
			}
		})
	}
}
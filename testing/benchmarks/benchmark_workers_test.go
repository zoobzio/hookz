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

// BenchmarkWorkerEfficiency tests worker configurations to find optimal settings.
// This measures sustainable throughput rather than bulk processing completion.
func BenchmarkWorkerEfficiency(b *testing.B) {
	workloads := []struct {
		name     string
		work     time.Duration
		variance bool
	}{
		{"uniform_fast", 100 * time.Microsecond, false},
		{"uniform_medium", 1 * time.Millisecond, false},
		{"variable", 1 * time.Millisecond, true},
	}
	
	workerCounts := []int{1, 2, 5, 10, 15, 20, 30}
	
	for _, workload := range workloads {
		for _, workers := range workerCounts {
			b.Run(fmt.Sprintf("%s_w%d", workload.name, workers), func(b *testing.B) {
				service := hookz.New[TestEvent](
					hookz.WithWorkers(workers),
					hookz.WithQueueSize(workers * 3), // Reasonable queue size
				)
				defer service.Close()
				
				var processed atomic.Int64
				
				// Register hook with specified workload characteristics
				_, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
					duration := workload.work
					if workload.variance {
						// Simulate variable processing time based on event ID
						variance := time.Duration(evt.ID%10) * (workload.work / 10)
						duration = workload.work + variance
					}
					
					if duration > 0 {
						time.Sleep(duration)
					}
					
					processed.Add(1)
					return nil
				})
				if err != nil {
					b.Fatal(err)
				}
				
				events := generateRealisticEvents(1000) // Fixed size to avoid excessive pressure
				eventIndex := 0
				
				b.ReportAllocs()
				b.ResetTimer()
				
				var successful, dropped int64
				emissionStart := time.Now()
				
				// Emit events with sustainable pacing
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
					
					// Pace emission based on expected processing time
					// This allows sustainable measurement rather than overwhelming the queue
					if i%workers == 0 && workload.work > 0 {
						time.Sleep(workload.work / 10) // Gentle pacing
					}
				}
				
				emissionDuration := time.Since(emissionStart)
				
				// Calculate metrics that actually matter
				var throughput float64
				if emissionDuration > 0 {
					throughput = float64(successful) / emissionDuration.Seconds()
				}
				
				var efficiency float64
				if successful > 0 {
					// Efficiency based on successful processing vs dropped events
					efficiency = float64(successful) / float64(successful+dropped) * 100
				}
				
				b.ReportMetric(efficiency, "efficiency_%")
				b.ReportMetric(float64(workers), "workers")
				b.ReportMetric(throughput, "events_per_second")
				b.ReportMetric(float64(successful), "events_accepted")
				b.ReportMetric(float64(dropped), "events_dropped")
				if successful+dropped > 0 {
					dropRate := float64(dropped) / float64(successful+dropped) * 100
					b.ReportMetric(dropRate, "drop_rate_%")
				}
			})
		}
	}
}

// BenchmarkWorkerUtilization measures actual worker utilization patterns
func BenchmarkWorkerUtilization(b *testing.B) {
	workerCounts := []int{5, 10, 20}
	
	for _, workers := range workerCounts {
		b.Run(fmt.Sprintf("workers_%d", workers), func(b *testing.B) {
			service := hookz.New[TestEvent](
				hookz.WithWorkers(workers),
				hookz.WithQueueSize(workers * 4),
			)
			defer service.Close()
			
			var processing atomic.Int64
			var maxConcurrent atomic.Int64
			var totalProcessed atomic.Int64
			
			_, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
				current := processing.Add(1)
				
				// Track maximum concurrent processing
				for {
					max := maxConcurrent.Load()
					if current <= max || maxConcurrent.CompareAndSwap(max, current) {
						break
					}
				}
				
				// Moderate processing time
				time.Sleep(time.Millisecond)
				
				processing.Add(-1)
				totalProcessed.Add(1)
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
			
			// Emit events with controlled pacing to allow utilization measurement
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
				
				// Pace emission to allow meaningful utilization measurement
				if i%10 == 0 {
					time.Sleep(100 * time.Microsecond)
				}
			}
			
			utilization := float64(maxConcurrent.Load()) / float64(workers) * 100
			
			b.ReportMetric(utilization, "max_utilization_%")
			b.ReportMetric(float64(maxConcurrent.Load()), "max_concurrent")
			b.ReportMetric(float64(successful), "events_accepted")
			b.ReportMetric(float64(dropped), "events_dropped")
			b.ReportMetric(float64(totalProcessed.Load()), "events_processed")
		})
	}
}

// BenchmarkWorkerStarvation tests behavior when work is imbalanced
func BenchmarkWorkerStarvation(b *testing.B) {
	service := hookz.New[TestEvent](
		hookz.WithWorkers(10),
		hookz.WithQueueSize(30),
	)
	defer service.Close()
	
	var fastCount, slowCount atomic.Int64
	var fastDropped, slowDropped atomic.Int64
	
	// Register fast hooks
	for i := 0; i < 8; i++ {
		_, err := service.Hook("fast.event", func(ctx context.Context, evt TestEvent) error {
			fastCount.Add(1)
			time.Sleep(100 * time.Microsecond) // Fast processing
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	
	// Register slow hooks  
	for i := 0; i < 2; i++ {
		_, err := service.Hook("slow.event", func(ctx context.Context, evt TestEvent) error {
			slowCount.Add(1)
			time.Sleep(5 * time.Millisecond) // Slow processing
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	
	fastEvents := generateRealisticEvents(500)
	slowEvents := generateRealisticEvents(500)
	
	b.ReportAllocs()
	b.ResetTimer()
	
	// Interleave fast and slow events with sustainable pacing
	fastIndex := 0
	slowIndex := 0
	
	for i := 0; i < b.N; i++ {
		// Emit fast event
		if i%2 == 0 {
			err := service.Emit(context.Background(), "fast.event", fastEvents[fastIndex])
			if errors.Is(err, hookz.ErrQueueFull) {
				fastDropped.Add(1)
			}
			fastIndex = (fastIndex + 1) % len(fastEvents)
		} else {
			// Emit slow event
			err := service.Emit(context.Background(), "slow.event", slowEvents[slowIndex])
			if errors.Is(err, hookz.ErrQueueFull) {
				slowDropped.Add(1)
			}
			slowIndex = (slowIndex + 1) % len(slowEvents)
		}
		
		// Pace emission to allow reasonable processing
		if i%20 == 0 {
			time.Sleep(time.Millisecond)
		}
	}
	
	b.ReportMetric(float64(fastCount.Load()), "fast_executions")
	b.ReportMetric(float64(slowCount.Load()), "slow_executions")
	b.ReportMetric(float64(fastDropped.Load()), "fast_dropped")
	b.ReportMetric(float64(slowDropped.Load()), "slow_dropped")
	
	// Calculate drop rates for each type
	totalFast := fastCount.Load() + fastDropped.Load()
	totalSlow := slowCount.Load() + slowDropped.Load()
	
	if totalFast > 0 {
		fastDropRate := float64(fastDropped.Load()) / float64(totalFast) * 100
		b.ReportMetric(fastDropRate, "fast_drop_rate_%")
	}
	if totalSlow > 0 {
		slowDropRate := float64(slowDropped.Load()) / float64(totalSlow) * 100
		b.ReportMetric(slowDropRate, "slow_drop_rate_%")
	}
}

// BenchmarkWorkerScaling tests how additional workers affect throughput
func BenchmarkWorkerScaling(b *testing.B) {
	processingTime := 2 * time.Millisecond // Fixed processing time
	workerCounts := []int{1, 2, 4, 8, 16, 32}
	
	for _, workers := range workerCounts {
		b.Run(fmt.Sprintf("workers_%d", workers), func(b *testing.B) {
			service := hookz.New[TestEvent](
				hookz.WithWorkers(workers),
				hookz.WithQueueSize(workers * 5), // Scale queue with workers
			)
			defer service.Close()
			
			var processed atomic.Int64
			
			_, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
				time.Sleep(processingTime)
				processed.Add(1)
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
			
			// Emit with rate limiting to test sustainable throughput
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
				
				// Rate limit based on expected capacity
				expectedCapacity := float64(workers) / processingTime.Seconds()
				if i > 0 && i%(int(expectedCapacity)/10) == 0 {
					time.Sleep(100 * time.Millisecond)
				}
			}
			
			elapsed := time.Since(start)
			throughput := float64(successful) / elapsed.Seconds()
			
			b.ReportMetric(float64(workers), "workers")
			b.ReportMetric(throughput, "events_per_second")
			b.ReportMetric(float64(successful), "events_accepted")
			b.ReportMetric(float64(dropped), "events_dropped")
			if successful+dropped > 0 {
				dropRate := float64(dropped) / float64(successful+dropped) * 100
				b.ReportMetric(dropRate, "drop_rate_%")
			}
		})
	}
}
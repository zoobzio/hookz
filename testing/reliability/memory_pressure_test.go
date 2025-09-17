package reliability

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zoobzio/hookz"
)

// MemoryEvent carries variable-sized payloads for memory testing
type MemoryEvent struct {
	ID       int
	Payload  []byte
	Metadata map[string]interface{} // Additional allocations
}

// getMemStats returns current memory usage in MB
func getMemStats() (allocMB, sysMB float64) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	allocMB = float64(m.Alloc) / 1024 / 1024
	sysMB = float64(m.Sys) / 1024 / 1024
	return
}

// TestMemoryPressureGradual tests behavior under gradually increasing memory load
// CI Mode: Small payloads, controlled growth
// Stress Mode: Set HOOKZ_TEST_LOAD_MULTIPLIER=10 for aggressive memory use
func TestMemoryPressureGradual(t *testing.T) {
	cfg := getTestConfig()

	// Force GC to get baseline
	runtime.GC()
	startAlloc, startSys := getMemStats()

	service := hookz.New[MemoryEvent](
		hookz.WithWorkers(cfg.workers),
		hookz.WithQueueSize(cfg.queueSize),
	)
	defer service.Close()

	basePayloadSize := int(1024 * cfg.loadMultiplier) // 1KB base
	var totalAllocated atomic.Int64
	var peakPayloadSize atomic.Int32

	// Hook that holds memory temporarily
	_, err := service.Hook("memory.test", func(ctx context.Context, evt MemoryEvent) error {
		totalAllocated.Add(int64(len(evt.Payload)))

		if len(evt.Payload) > int(peakPayloadSize.Load()) {
			peakPayloadSize.Store(int32(len(evt.Payload)))
		}

		// Hold memory briefly (simulates processing)
		time.Sleep(cfg.hookDelay)

		// Force some additional allocations
		temp := make([]byte, len(evt.Payload)/10)
		_ = temp

		return nil
	})
	require.NoError(t, err)

	// Gradually increase payload size
	var wg sync.WaitGroup
	for i := 0; i < int(10*cfg.loadMultiplier); i++ {
		wg.Add(1)
		go func(iteration int) {
			defer wg.Done()

			// Exponentially growing payload
			size := basePayloadSize * (1 << (iteration / 3))
			if size > 10*1024*1024 { // Cap at 10MB
				size = 10 * 1024 * 1024
			}

			evt := MemoryEvent{
				ID:      iteration,
				Payload: make([]byte, size),
				Metadata: map[string]interface{}{
					"size":      size,
					"iteration": iteration,
					"timestamp": time.Now(),
				},
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			// Best effort - don't fail on queue full
			_ = service.Emit(ctx, "memory.test", evt)
		}(i)

		// Stagger emissions
		time.Sleep(10 * time.Millisecond)
	}

	wg.Wait()

	// Let system process remaining events
	time.Sleep(time.Second)

	// Get final memory stats
	runtime.GC()
	endAlloc, endSys := getMemStats()

	allocatedMB := float64(totalAllocated.Load()) / 1024 / 1024
	peakSizeMB := float64(peakPayloadSize.Load()) / 1024 / 1024
	memGrowthMB := endAlloc - startAlloc

	t.Logf("Memory Pressure Results:")
	t.Logf("  Start memory: %.2f MB alloc, %.2f MB sys", startAlloc, startSys)
	t.Logf("  End memory: %.2f MB alloc, %.2f MB sys", endAlloc, endSys)
	t.Logf("  Memory growth: %.2f MB", memGrowthMB)
	t.Logf("  Total allocated through events: %.2f MB", allocatedMB)
	t.Logf("  Peak payload size: %.2f MB", peakSizeMB)

	// Verify system handled memory pressure
	assert.Greater(t, totalAllocated.Load(), int64(0), "Should have processed events")

	if cfg.loadMultiplier > 5.0 {
		t.Logf("  STRESS: System handled %.2f MB total allocation", allocatedMB)
	}
}

// TestMemoryLeakDetection attempts to detect memory leaks in hook management
func TestMemoryLeakDetection(t *testing.T) {
	cfg := getTestConfig()

	// Force GC and get baseline
	runtime.GC()
	runtime.GC() // Double GC for accuracy
	baseAlloc, _ := getMemStats()

	service := hookz.New[MemoryEvent](
		hookz.WithWorkers(cfg.workers),
		hookz.WithQueueSize(cfg.queueSize),
	)
	defer service.Close()

	iterations := int(100 * cfg.loadMultiplier)

	// Repeatedly register and unregister hooks
	for i := 0; i < iterations; i++ {
		// Register multiple hooks
		var hooks []hookz.Hook
		for j := 0; j < 10; j++ {
			hook, err := service.Hook(hookz.Key(fmt.Sprintf("leak.test.%d", j)),
				func(ctx context.Context, evt MemoryEvent) error {
					// Allocate some memory
					data := make([]byte, 1024)
					_ = data
					return nil
				})
			require.NoError(t, err)
			hooks = append(hooks, hook)
		}

		// Emit events to all hooks
		for j := 0; j < 10; j++ {
			evt := MemoryEvent{
				ID:      i*10 + j,
				Payload: make([]byte, 1024),
			}
			_ = service.Emit(context.Background(), hookz.Key(fmt.Sprintf("leak.test.%d", j)), evt)
		}

		// Unregister all hooks
		for _, hook := range hooks {
			err := hook.Unhook()
			assert.NoError(t, err)
		}

		// Periodically check memory
		if i%20 == 0 && i > 0 {
			runtime.GC()
			currentAlloc, _ := getMemStats()
			growth := currentAlloc - baseAlloc

			if growth > 50.0 { // Alert if > 50MB growth
				t.Logf("Warning: Memory growth detected: %.2f MB after %d iterations", growth, i)
			}
		}
	}

	// Final memory check
	runtime.GC()
	runtime.GC()
	time.Sleep(100 * time.Millisecond) // Let finalizers run

	finalAlloc, _ := getMemStats()
	totalGrowth := finalAlloc - baseAlloc

	t.Logf("Memory Leak Test Results:")
	t.Logf("  Iterations: %d", iterations)
	t.Logf("  Base memory: %.2f MB", baseAlloc)
	t.Logf("  Final memory: %.2f MB", finalAlloc)
	t.Logf("  Total growth: %.2f MB", totalGrowth)
	t.Logf("  Growth per iteration: %.4f MB", totalGrowth/float64(iterations))

	// In CI mode, expect minimal growth
	if cfg.loadMultiplier <= 1.0 {
		assert.Less(t, totalGrowth, 10.0,
			"Excessive memory growth detected - possible leak")
	} else {
		// Stress mode - just report
		if totalGrowth > 100.0 {
			t.Logf("  STRESS: High memory growth detected: %.2f MB", totalGrowth)
		}
	}
}

// TestMemoryWithOverflow tests memory behavior with overflow queues
func TestMemoryWithOverflow(t *testing.T) {
	cfg := getTestConfig()

	// Test with different overflow configurations
	testCases := []struct {
		name             string
		overflowCapacity int
		evictionStrategy string
	}{
		{
			name:             "NoOverflow",
			overflowCapacity: 0,
		},
		{
			name:             "SmallOverflowFIFO",
			overflowCapacity: 10,
			evictionStrategy: "fifo",
		},
		{
			name:             "LargeOverflowLIFO",
			overflowCapacity: int(100 * cfg.loadMultiplier),
			evictionStrategy: "lifo",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			runtime.GC()
			startAlloc, _ := getMemStats()

			opts := []hookz.Option{
				hookz.WithWorkers(3),
				hookz.WithQueueSize(6),
			}

			if tc.overflowCapacity > 0 {
				opts = append(opts, hookz.WithOverflow(hookz.OverflowConfig{
					Capacity:         tc.overflowCapacity,
					DrainInterval:    10 * time.Millisecond,
					EvictionStrategy: tc.evictionStrategy,
				}))
			}

			service := hookz.New[MemoryEvent](opts...)
			defer service.Close()

			var processed atomic.Int64
			var maxQueuedPayload atomic.Int64

			// Slow handler to cause queuing
			_, err := service.Hook("overflow.test", func(ctx context.Context, evt MemoryEvent) error {
				processed.Add(1)

				// Track maximum payload seen
				if int64(len(evt.Payload)) > maxQueuedPayload.Load() {
					maxQueuedPayload.Store(int64(len(evt.Payload)))
				}

				time.Sleep(cfg.hookDelay * 5) // Slow processing
				return nil
			})
			require.NoError(t, err)

			// Send burst of events with payloads
			payloadSize := int(10240 * cfg.loadMultiplier) // 10KB base
			burstSize := 50

			var sent, dropped atomic.Int64

			for i := 0; i < burstSize; i++ {
				evt := MemoryEvent{
					ID:      i,
					Payload: make([]byte, payloadSize),
				}

				err := service.Emit(context.Background(), "overflow.test", evt)
				if err == nil {
					sent.Add(1)
				} else if errors.Is(err, hookz.ErrQueueFull) {
					dropped.Add(1)
				}
			}

			// Wait for processing
			time.Sleep(time.Second)

			runtime.GC()
			endAlloc, _ := getMemStats()
			memoryUsed := endAlloc - startAlloc

			t.Logf("  Config: %s", tc.name)
			t.Logf("  Sent: %d, Dropped: %d, Processed: %d",
				sent.Load(), dropped.Load(), processed.Load())
			t.Logf("  Memory used: %.2f MB", memoryUsed)
			t.Logf("  Max payload queued: %.2f KB", float64(maxQueuedPayload.Load())/1024)

			// Verify overflow helped reduce drops
			if tc.overflowCapacity > 0 {
				assert.Less(t, dropped.Load(), sent.Load()/2,
					"Overflow should reduce drop rate")
			}
		})
	}
}

// TestMemoryPressureConcurrent tests memory under concurrent load
func TestMemoryPressureConcurrent(t *testing.T) {
	cfg := getTestConfig()

	service := hookz.New[MemoryEvent](
		hookz.WithWorkers(cfg.workers),
		hookz.WithQueueSize(cfg.queueSize),
	)
	defer service.Close()

	// Multiple hooks with different memory patterns
	patterns := []struct {
		event       hookz.Key
		payloadSize int
		delay       time.Duration
	}{
		{"memory.small", 1024, cfg.hookDelay},
		{"memory.medium", 10240, cfg.hookDelay * 2},
		{"memory.large", 102400, cfg.hookDelay * 5},
	}

	var totalProcessed atomic.Int64

	for _, pattern := range patterns {
		p := pattern // Capture for closure
		_, err := service.Hook(p.event, func(ctx context.Context, evt MemoryEvent) error {
			totalProcessed.Add(1)

			// Allocate additional memory during processing
			temp := make([]byte, p.payloadSize/2)
			_ = append(temp, evt.Payload[:min(len(evt.Payload), 100)]...)

			time.Sleep(p.delay)
			return nil
		})
		require.NoError(t, err)
	}

	// Concurrent emitters
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < cfg.goroutines; i++ {
		for _, pattern := range patterns {
			wg.Add(1)
			go func(id int, p struct {
				event       hookz.Key
				payloadSize int
				delay       time.Duration
			}) {
				defer wg.Done()

				for j := 0; j < cfg.eventCount/len(patterns); j++ {
					select {
					case <-ctx.Done():
						return
					default:
						evt := MemoryEvent{
							ID:      id*1000 + j,
							Payload: make([]byte, p.payloadSize),
						}

						// Best effort emission
						_ = service.Emit(ctx, p.event, evt)

						// Small delay between emissions
						time.Sleep(time.Millisecond)
					}
				}
			}(i, pattern)
		}
	}

	wg.Wait()

	t.Logf("Concurrent Memory Test Results:")
	t.Logf("  Total events processed: %d", totalProcessed.Load())
	t.Logf("  Events per pattern: %d", totalProcessed.Load()/int64(len(patterns)))

	assert.Greater(t, totalProcessed.Load(), int64(0), "Should process events under memory pressure")
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

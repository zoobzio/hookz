package reliability

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zoobzio/hookz"
)

// ResilienceEvent for testing resilience features
type ResilienceEvent struct {
	ID        int
	Timestamp time.Time
	Priority  int
}

// TestBackpressureEffectiveness tests if backpressure reduces drops
// CI Mode: Quick validation with minimal delays
// Stress Mode: Set HOOKZ_TEST_DELAY_MULTIPLIER=10 for extended backpressure periods
func TestBackpressureEffectiveness(t *testing.T) {
	cfg := getTestConfig()

	testCases := []struct {
		name         string
		config       *hookz.BackpressureConfig
		expectedDrop bool
	}{
		{
			name:         "NoBackpressure",
			config:       nil,
			expectedDrop: true,
		},
		{
			name: "FixedBackpressure",
			config: &hookz.BackpressureConfig{
				MaxWait:        time.Duration(float64(10*time.Millisecond) * cfg.delayMultiplier),
				StartThreshold: 0.8,
				Strategy:       "fixed",
			},
			expectedDrop: false,
		},
		{
			name: "LinearBackpressure",
			config: &hookz.BackpressureConfig{
				MaxWait:        time.Duration(float64(20*time.Millisecond) * cfg.delayMultiplier),
				StartThreshold: 0.7,
				Strategy:       "linear",
			},
			expectedDrop: false,
		},
		{
			name: "ExponentialBackpressure",
			config: &hookz.BackpressureConfig{
				MaxWait:        time.Duration(float64(15*time.Millisecond) * cfg.delayMultiplier),
				StartThreshold: 0.9,
				Strategy:       "exponential",
			},
			expectedDrop: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			opts := []hookz.Option{
				hookz.WithWorkers(3),
				hookz.WithQueueSize(6), // Small queue to force saturation
			}

			if tc.config != nil {
				opts = append(opts, hookz.WithBackpressure(*tc.config))
			}

			service := hookz.New[ResilienceEvent](opts...)
			defer service.Close()

			var processed atomic.Int64
			var dropped atomic.Int64
			var succeeded atomic.Int64

			// Slow hook to create backpressure
			_, err := service.Hook("backpressure.test", func(ctx context.Context, evt ResilienceEvent) error {
				processed.Add(1)
				time.Sleep(cfg.hookDelay * 3) // Slower than emission rate
				return nil
			})
			require.NoError(t, err)

			// Burst emission to saturate queue
			burstSize := int(20 * cfg.loadMultiplier)
			start := time.Now()

			for i := 0; i < burstSize; i++ {
				evt := ResilienceEvent{
					ID:        i,
					Timestamp: time.Now(),
				}

				err := service.Emit(context.Background(), "backpressure.test", evt)
				if errors.Is(err, hookz.ErrQueueFull) {
					dropped.Add(1)
				} else if err == nil {
					succeeded.Add(1)
				}
			}

			emissionTime := time.Since(start)

			// Wait for processing
			time.Sleep(time.Second)

			dropRate := float64(dropped.Load()) * 100 / float64(burstSize)

			t.Logf("  Strategy: %s", tc.name)
			t.Logf("  Burst size: %d in %v", burstSize, emissionTime)
			t.Logf("  Succeeded: %d, Dropped: %d (%.1f%%)",
				succeeded.Load(), dropped.Load(), dropRate)
			t.Logf("  Processed: %d", processed.Load())

			if tc.expectedDrop {
				if cfg.loadMultiplier <= 1.0 {
					assert.Greater(t, dropped.Load(), int64(0),
						"Expected drops without backpressure")
				}
			} else {
				if cfg.loadMultiplier <= 1.0 {
					assert.LessOrEqual(t, dropRate, 20.0,
						"Backpressure should significantly reduce drops")
				}
			}

			// Verify backpressure didn't cause excessive delays in CI mode
			if tc.config != nil && cfg.delayMultiplier <= 1.0 {
				avgEmissionTime := emissionTime / time.Duration(burstSize)
				assert.Less(t, avgEmissionTime, 5*time.Millisecond,
					"Backpressure delays too high for CI")
			}
		})
	}
}

// TestOverflowCapacityLimits tests overflow queue behavior at capacity limits
func TestOverflowCapacityLimits(t *testing.T) {
	cfg := getTestConfig()

	strategies := []string{"fifo", "lifo", "reject"}

	for _, strategy := range strategies {
		t.Run(fmt.Sprintf("Strategy_%s", strategy), func(t *testing.T) {
			capacity := int(10 * cfg.loadMultiplier)

			service := hookz.New[ResilienceEvent](
				hookz.WithWorkers(2),
				hookz.WithQueueSize(4),
				hookz.WithOverflow(hookz.OverflowConfig{
					Capacity:         capacity,
					DrainInterval:    time.Duration(float64(50*time.Millisecond) * cfg.delayMultiplier),
					EvictionStrategy: strategy,
				}),
			)
			defer service.Close()

			var processed atomic.Int64
			var processingOrder []int
			var mu sync.Mutex

			// Very slow hook to force overflow usage
			_, err := service.Hook("overflow.test", func(ctx context.Context, evt ResilienceEvent) error {
				processed.Add(1)

				mu.Lock()
				processingOrder = append(processingOrder, evt.ID)
				mu.Unlock()

				time.Sleep(cfg.hookDelay * 10) // Very slow
				return nil
			})
			require.NoError(t, err)

			// Send events beyond combined capacity
			overflowTestSize := capacity + 10
			var succeeded, dropped atomic.Int64

			for i := 0; i < overflowTestSize; i++ {
				evt := ResilienceEvent{
					ID:        i,
					Timestamp: time.Now(),
					Priority:  i, // Later events have higher priority
				}

				err := service.Emit(context.Background(), "overflow.test", evt)
				if errors.Is(err, hookz.ErrQueueFull) {
					dropped.Add(1)
				} else {
					succeeded.Add(1)
				}
			}

			// Wait for some processing
			time.Sleep(time.Second)

			t.Logf("  Strategy: %s, Capacity: %d", strategy, capacity)
			t.Logf("  Sent: %d, Succeeded: %d, Dropped: %d",
				overflowTestSize, succeeded.Load(), dropped.Load())
			t.Logf("  Processed: %d", processed.Load())

			mu.Lock()
			if len(processingOrder) > 0 {
				t.Logf("  First processed: %v", processingOrder[:min(len(processingOrder), 5)])
			}
			mu.Unlock()

			// Verify strategy-specific behavior
			switch strategy {
			case "reject":
				if cfg.loadMultiplier <= 1.0 {
					assert.Greater(t, dropped.Load(), int64(0),
						"Reject strategy should drop events at capacity")
				}
			case "fifo", "lifo":
				if cfg.loadMultiplier <= 1.0 {
					assert.Greater(t, succeeded.Load(), int64(capacity),
						"Eviction strategies should accept more than capacity")
				}
			}
		})
	}
}

// TestBackpressureWithOverflow tests combined resilience features
func TestBackpressureWithOverflow(t *testing.T) {
	cfg := getTestConfig()

	service := hookz.New[ResilienceEvent](
		hookz.WithWorkers(3),
		hookz.WithQueueSize(6),
		hookz.WithBackpressure(hookz.BackpressureConfig{
			MaxWait:        time.Duration(float64(10*time.Millisecond) * cfg.delayMultiplier),
			StartThreshold: 0.8,
			Strategy:       "linear",
		}),
		hookz.WithOverflow(hookz.OverflowConfig{
			Capacity:         int(20 * cfg.loadMultiplier),
			DrainInterval:    time.Duration(float64(25*time.Millisecond) * cfg.delayMultiplier),
			EvictionStrategy: "fifo",
		}),
	)
	defer service.Close()

	var processed atomic.Int64

	// Hook that occasionally creates backpressure
	_, err := service.Hook("combined.test", func(ctx context.Context, evt ResilienceEvent) error {
		processed.Add(1)

		// Variable processing time
		delay := cfg.hookDelay
		if evt.ID%5 == 0 {
			delay *= 5 // Some events take much longer
		}

		time.Sleep(delay)
		return nil
	})
	require.NoError(t, err)

	// Generate sustained load
	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(float64(2*time.Second)*cfg.delayMultiplier))
	defer cancel()

	var wg sync.WaitGroup
	var emitted, dropped atomic.Int64

	// Multiple emitters
	for i := 0; i < cfg.goroutines; i++ {
		wg.Add(1)
		go func(emitterID int) {
			defer wg.Done()

			eventID := 0
			for {
				select {
				case <-ctx.Done():
					return
				default:
					evt := ResilienceEvent{
						ID:        emitterID*10000 + eventID,
						Timestamp: time.Now(),
					}

					emitted.Add(1)
					err := service.Emit(context.Background(), "combined.test", evt)
					if errors.Is(err, hookz.ErrQueueFull) {
						dropped.Add(1)
					}

					eventID++

					// Variable emission rate
					if eventID%3 == 0 {
						time.Sleep(cfg.emitInterval * 2)
					} else {
						time.Sleep(cfg.emitInterval)
					}
				}
			}
		}(i)
	}

	wg.Wait()

	// Allow processing to complete
	time.Sleep(time.Second)

	totalEmitted := emitted.Load()
	totalDropped := dropped.Load()
	totalProcessed := processed.Load()

	dropRate := float64(totalDropped) * 100 / float64(totalEmitted)
	processRate := float64(totalProcessed) * 100 / float64(totalEmitted)

	t.Logf("Combined Resilience Results:")
	t.Logf("  Test duration: %v", ctx.Err())
	t.Logf("  Total emitted: %d", totalEmitted)
	t.Logf("  Total dropped: %d (%.2f%%)", totalDropped, dropRate)
	t.Logf("  Total processed: %d (%.2f%%)", totalProcessed, processRate)

	// Combined features should significantly reduce drops
	if cfg.loadMultiplier <= 1.0 {
		assert.Less(t, dropRate, 10.0,
			"Combined resilience should keep drops under 10%")
		assert.Greater(t, processRate, 80.0,
			"Should process majority of events")
	} else {
		t.Logf("  STRESS: Combined resilience handled %.2f%% drop rate", dropRate)
	}
}

// TestResilienceFailureCascade tests resilience under cascading failures
func TestResilienceFailureCascade(t *testing.T) {
	cfg := getTestConfig()

	service := hookz.New[ResilienceEvent](
		hookz.WithWorkers(5),
		hookz.WithQueueSize(10),
		hookz.WithBackpressure(hookz.BackpressureConfig{
			MaxWait:        time.Duration(float64(20*time.Millisecond) * cfg.delayMultiplier),
			StartThreshold: 0.7,
			Strategy:       "exponential",
		}),
		hookz.WithOverflow(hookz.OverflowConfig{
			Capacity:         int(50 * cfg.loadMultiplier),
			DrainInterval:    time.Duration(float64(20*time.Millisecond) * cfg.delayMultiplier),
			EvictionStrategy: "lifo",
		}),
	)
	defer service.Close()

	var processed atomic.Int64
	var failures atomic.Int64
	var cascadeDepth atomic.Int32

	// Hook that can trigger cascades
	_, err := service.Hook("cascade.resilience", func(ctx context.Context, evt ResilienceEvent) error {
		processed.Add(1)

		// Track cascade depth
		if evt.Priority > int(cascadeDepth.Load()) {
			cascadeDepth.Store(int32(evt.Priority))
		}

		// Inject failures based on configuration
		if cfg.errorRate > 0 && float64(processed.Load()%100) < cfg.errorRate*100 {
			failures.Add(1)
			return fmt.Errorf("injected failure at depth %d", evt.Priority)
		}

		// Trigger cascade events
		if evt.Priority < 5 {
			for i := 0; i < 2; i++ {
				child := ResilienceEvent{
					ID:       evt.ID*10 + i,
					Priority: evt.Priority + 1,
				}

				// Best effort cascade (don't fail on drops)
				_ = service.Emit(ctx, "cascade.resilience", child)
			}
		}

		time.Sleep(cfg.hookDelay)
		return nil
	})
	require.NoError(t, err)

	// Start cascade with multiple roots
	for i := 0; i < int(3*cfg.loadMultiplier); i++ {
		root := ResilienceEvent{
			ID:       i,
			Priority: 0,
		}

		err := service.Emit(context.Background(), "cascade.resilience", root)
		if err != nil {
			t.Logf("Failed to emit root event %d: %v", i, err)
		}
	}

	// Let cascade run
	time.Sleep(time.Duration(float64(2*time.Second) * cfg.delayMultiplier))

	t.Logf("Resilience Cascade Results:")
	t.Logf("  Events processed: %d", processed.Load())
	t.Logf("  Peak cascade depth: %d", cascadeDepth.Load())
	t.Logf("  Failures injected: %d", failures.Load())

	// Verify system handled cascade with resilience
	assert.Greater(t, processed.Load(), int64(0), "Should process cascade events")
	assert.Greater(t, cascadeDepth.Load(), int32(0), "Cascade should progress")

	if cfg.errorRate > 0 {
		t.Logf("  STRESS: Resilience handled cascade with %.0f%% failure rate",
			cfg.errorRate*100)
	}
}

// TestOverflowDrainEfficiency tests overflow drain performance
func TestOverflowDrainEfficiency(t *testing.T) {
	cfg := getTestConfig()

	// Test different drain intervals
	intervals := []time.Duration{
		time.Duration(float64(5*time.Millisecond) * cfg.delayMultiplier),
		time.Duration(float64(25*time.Millisecond) * cfg.delayMultiplier),
		time.Duration(float64(100*time.Millisecond) * cfg.delayMultiplier),
	}

	for _, interval := range intervals {
		t.Run(fmt.Sprintf("Interval_%v", interval), func(t *testing.T) {
			service := hookz.New[ResilienceEvent](
				hookz.WithWorkers(2),
				hookz.WithQueueSize(4),
				hookz.WithOverflow(hookz.OverflowConfig{
					Capacity:         int(20 * cfg.loadMultiplier),
					DrainInterval:    interval,
					EvictionStrategy: "fifo",
				}),
			)
			defer service.Close()

			var processed atomic.Int64
			var overflowUsed atomic.Bool

			// Slow hook to ensure overflow usage
			_, err := service.Hook("drain.test", func(ctx context.Context, evt ResilienceEvent) error {
				processed.Add(1)
				time.Sleep(cfg.hookDelay * 8) // Very slow processing
				return nil
			})
			require.NoError(t, err)

			// Quick burst to fill primary and overflow
			burstSize := int(30 * cfg.loadMultiplier)
			start := time.Now()

			for i := 0; i < burstSize; i++ {
				evt := ResilienceEvent{ID: i}
				err := service.Emit(context.Background(), "drain.test", evt)

				// If primary is full but overflow accepts, we're using overflow
				if err == nil && i > 8 {
					overflowUsed.Store(true)
				}
			}

			burstTime := time.Since(start)

			// Measure drain efficiency
			initialProcessed := processed.Load()
			time.Sleep(interval * 5) // Let drain work
			drainProcessed := processed.Load() - initialProcessed

			t.Logf("  Drain interval: %v", interval)
			t.Logf("  Burst time: %v for %d events", burstTime, burstSize)
			t.Logf("  Overflow used: %v", overflowUsed.Load())
			t.Logf("  Events drained in %v: %d", interval*5, drainProcessed)
			t.Logf("  Total processed: %d", processed.Load())

			// Verify overflow was used and is draining
			if cfg.loadMultiplier <= 1.0 {
				assert.True(t, overflowUsed.Load(), "Should use overflow with burst")
				assert.Greater(t, drainProcessed, int64(0), "Should drain events from overflow")
			}
		})
	}
}

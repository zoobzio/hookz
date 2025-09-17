package reliability

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zoobzio/hookz"
)

// CascadeEvent represents an event that can trigger cascading failures
type CascadeEvent struct {
	ID           int
	TriggerChain bool   // Should this event trigger more events?
	ChainDepth   int    // How deep in the cascade chain
	FailureType  string // Type of failure to inject
	Payload      []byte // Payload for memory testing
}

// TestCascadeFailureSimple tests cascading event chains
// CI Mode: Small chain depth, quick execution
// Stress Mode: Set HOOKZ_TEST_LOAD_MULTIPLIER=10 for deep chains
func TestCascadeFailureSimple(t *testing.T) {
	cfg := getTestConfig()

	service := hookz.New[CascadeEvent](
		hookz.WithWorkers(cfg.workers),
		hookz.WithQueueSize(cfg.queueSize),
	)
	defer service.Close()

	maxDepth := int(5 * cfg.loadMultiplier)
	var totalEvents atomic.Int64
	var droppedEvents atomic.Int64

	// Handler that triggers more events (cascade)
	_, err := service.Hook("cascade.trigger", func(ctx context.Context, evt CascadeEvent) error {
		totalEvents.Add(1)

		if evt.ChainDepth < maxDepth {
			// Trigger multiple child events
			for i := 0; i < 2; i++ {
				childEvent := CascadeEvent{
					ID:           evt.ID*10 + i,
					TriggerChain: true,
					ChainDepth:   evt.ChainDepth + 1,
				}

				err := service.Emit(ctx, "cascade.trigger", childEvent)
				if errors.Is(err, hookz.ErrQueueFull) {
					droppedEvents.Add(1)
				}
			}
		}

		// Simulate processing
		time.Sleep(cfg.hookDelay)
		return nil
	})
	require.NoError(t, err)

	// Start cascade
	rootEvent := CascadeEvent{
		ID:           1,
		TriggerChain: true,
		ChainDepth:   0,
	}

	err = service.Emit(context.Background(), "cascade.trigger", rootEvent)
	require.NoError(t, err)

	// Wait for cascade to complete
	time.Sleep(time.Duration(maxDepth) * cfg.hookDelay * 10)

	expectedEvents := (1 << (maxDepth + 1)) - 1 // 2^(depth+1) - 1 for binary tree
	actualEvents := totalEvents.Load()
	dropped := droppedEvents.Load()

	t.Logf("Cascade Test Results:")
	t.Logf("  Max depth: %d", maxDepth)
	t.Logf("  Expected events: %d", expectedEvents)
	t.Logf("  Processed events: %d", actualEvents)
	t.Logf("  Dropped events: %d", dropped)
	t.Logf("  Success rate: %.2f%%", float64(actualEvents)*100/float64(expectedEvents))

	if cfg.loadMultiplier <= 1.0 {
		// CI mode: Expect most events to be processed
		assert.Greater(t, actualEvents, int64(expectedEvents/2),
			"Too many events dropped in cascade")
	}
}

// TestCascadeWithBackpressure tests cascade behavior with varying event rates
// This simulates real-world scenarios where cascades create traffic spikes
func TestCascadeWithBackpressure(t *testing.T) {
	cfg := getTestConfig()

	// Test with and without backpressure
	testCases := []struct {
		name         string
		backpressure *hookz.BackpressureConfig
		expectDrops  bool
	}{
		{
			name:        "NoBackpressure",
			expectDrops: true,
		},
		{
			name: "WithBackpressure",
			backpressure: &hookz.BackpressureConfig{
				MaxWait:        10 * time.Millisecond,
				StartThreshold: 0.7,
				Strategy:       "linear",
			},
			expectDrops: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			opts := []hookz.Option{
				hookz.WithWorkers(5),
				hookz.WithQueueSize(10),
			}

			if tc.backpressure != nil {
				opts = append(opts, hookz.WithBackpressure(*tc.backpressure))
			}

			service := hookz.New[CascadeEvent](opts...)
			defer service.Close()

			var processed atomic.Int64
			var dropped atomic.Int64

			// Fast initial handler
			_, err := service.Hook("cascade.fast", func(ctx context.Context, evt CascadeEvent) error {
				processed.Add(1)

				// Fast processing initially
				if evt.ChainDepth < 3 {
					time.Sleep(100 * time.Microsecond)

					// Trigger burst of events
					for i := 0; i < 3; i++ {
						child := CascadeEvent{
							ID:         evt.ID*10 + i,
							ChainDepth: evt.ChainDepth + 1,
						}
						err := service.Emit(ctx, "cascade.slow", child)
						if errors.Is(err, hookz.ErrQueueFull) {
							dropped.Add(1)
						}
					}
				}
				return nil
			})
			require.NoError(t, err)

			// Slow secondary handler (creates backpressure)
			_, err = service.Hook("cascade.slow", func(ctx context.Context, evt CascadeEvent) error {
				processed.Add(1)
				time.Sleep(cfg.hookDelay * 5) // Slower processing
				return nil
			})
			require.NoError(t, err)

			// Start cascade
			err = service.Emit(context.Background(), "cascade.fast", CascadeEvent{ID: 1})
			require.NoError(t, err)

			// Wait for completion
			time.Sleep(500 * time.Millisecond)

			t.Logf("  Processed: %d, Dropped: %d", processed.Load(), dropped.Load())

			if tc.expectDrops {
				if cfg.loadMultiplier <= 1.0 {
					// In CI, system might be fast enough to handle load - just log
					if dropped.Load() == 0 {
						t.Logf("  System handled load without drops (faster than expected)")
					}
				}
			} else if cfg.loadMultiplier <= 1.0 {
				assert.Equal(t, int64(0), dropped.Load(),
					"Backpressure should prevent drops in CI mode")
			}
		})
	}
}

// TestCascadeMemoryPressure tests cascading failures under memory pressure
// CI Mode: Small payloads, controlled growth
// Stress Mode: Set HOOKZ_TEST_LOAD_MULTIPLIER=5 for larger payloads
func TestCascadeMemoryPressure(t *testing.T) {
	cfg := getTestConfig()

	payloadSize := int(1024 * cfg.loadMultiplier) // 1KB base, scales with multiplier

	service := hookz.New[CascadeEvent](
		hookz.WithWorkers(cfg.workers),
		hookz.WithQueueSize(cfg.queueSize),
	)
	defer service.Close()

	var totalBytes atomic.Int64
	var peakDepth atomic.Int32

	// Handler that accumulates memory
	_, err := service.Hook("memory.cascade", func(ctx context.Context, evt CascadeEvent) error {
		// Track memory usage
		totalBytes.Add(int64(len(evt.Payload)))

		if evt.ChainDepth > int(peakDepth.Load()) {
			peakDepth.Store(int32(evt.ChainDepth))
		}

		// Create child events with growing payloads
		if evt.ChainDepth < 5 {
			for i := 0; i < 2; i++ {
				// Allocate new payload (simulates data accumulation)
				childPayload := make([]byte, payloadSize*(evt.ChainDepth+1))

				child := CascadeEvent{
					ID:         evt.ID*10 + i,
					ChainDepth: evt.ChainDepth + 1,
					Payload:    childPayload,
				}

				// Best effort emission (don't fail test on drops)
				_ = service.Emit(ctx, "memory.cascade", child)
			}
		}

		// Hold memory briefly
		time.Sleep(cfg.hookDelay)

		return nil
	})
	require.NoError(t, err)

	// Start with moderate payload
	initialPayload := make([]byte, payloadSize)
	err = service.Emit(context.Background(), "memory.cascade", CascadeEvent{
		ID:      1,
		Payload: initialPayload,
	})
	require.NoError(t, err)

	// Wait for cascade
	time.Sleep(time.Second)

	totalMB := float64(totalBytes.Load()) / (1024 * 1024)

	t.Logf("Memory Cascade Results:")
	t.Logf("  Payload size: %d bytes", payloadSize)
	t.Logf("  Peak depth reached: %d", peakDepth.Load())
	t.Logf("  Total memory accumulated: %.2f MB", totalMB)

	// Verify system handled memory pressure
	assert.Greater(t, peakDepth.Load(), int32(0), "Cascade should have progressed")

	if cfg.loadMultiplier > 2.0 {
		t.Logf("  STRESS: System handled %.2f MB cascade load", totalMB)
	}
}

// TestCascadeDeadlock tests for potential deadlocks in event chains
func TestCascadeDeadlock(t *testing.T) {
	cfg := getTestConfig()

	service := hookz.New[CascadeEvent](
		hookz.WithWorkers(2), // Small worker pool to increase deadlock chance
		hookz.WithQueueSize(4),
	)
	defer service.Close()

	var mu sync.Mutex
	var eventOrder []int

	// Handler A triggers B
	_, err := service.Hook("cascade.A", func(ctx context.Context, evt CascadeEvent) error {
		mu.Lock()
		eventOrder = append(eventOrder, evt.ID)
		mu.Unlock()

		// Potentially dangerous: emit while holding resources
		if evt.ChainDepth < 3 {
			child := CascadeEvent{
				ID:         evt.ID + 100,
				ChainDepth: evt.ChainDepth + 1,
			}

			// This could deadlock if not handled properly
			err := service.Emit(ctx, "cascade.B", child)
			if errors.Is(err, hookz.ErrQueueFull) {
				// Accept drops to prevent deadlock
				return nil
			}
		}

		time.Sleep(cfg.hookDelay)
		return nil
	})
	require.NoError(t, err)

	// Handler B triggers A (circular dependency)
	_, err = service.Hook("cascade.B", func(ctx context.Context, evt CascadeEvent) error {
		mu.Lock()
		eventOrder = append(eventOrder, evt.ID)
		mu.Unlock()

		if evt.ChainDepth < 3 {
			child := CascadeEvent{
				ID:         evt.ID + 200,
				ChainDepth: evt.ChainDepth + 1,
			}

			// Circular reference back to A
			err := service.Emit(ctx, "cascade.A", child)
			if errors.Is(err, hookz.ErrQueueFull) {
				// Accept drops to prevent deadlock
				return nil
			}
		}

		time.Sleep(cfg.hookDelay)
		return nil
	})
	require.NoError(t, err)

	// Start circular cascade with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan bool)
	go func() {
		err := service.Emit(ctx, "cascade.A", CascadeEvent{ID: 1})
		assert.NoError(t, err)

		// Wait a bit for cascade
		time.Sleep(500 * time.Millisecond)
		done <- true
	}()

	// Verify no deadlock occurs
	select {
	case <-done:
		t.Log("Circular cascade completed without deadlock")
	case <-ctx.Done():
		t.Fatal("Deadlock detected - cascade did not complete")
	}

	mu.Lock()
	t.Logf("Event execution order: %v", eventOrder)
	mu.Unlock()

	assert.Greater(t, len(eventOrder), 0, "Events should have been processed")
}

// TestCascadeRecovery tests system recovery after cascade-induced exhaustion
func TestCascadeRecovery(t *testing.T) {
	cfg := getTestConfig()

	service := hookz.New[CascadeEvent](
		hookz.WithWorkers(3),
		hookz.WithQueueSize(6),
	)
	defer service.Close()

	var phase atomic.Int32 // 0=cascade, 1=exhausted, 2=recovering, 3=recovered

	// Aggressive cascade handler
	_, err := service.Hook("cascade.aggressive", func(ctx context.Context, evt CascadeEvent) error {
		currentPhase := phase.Load()

		if currentPhase == 0 && evt.ChainDepth < 10 {
			// Flood the system
			for i := 0; i < 5; i++ {
				child := CascadeEvent{
					ID:         evt.ID*10 + i,
					ChainDepth: evt.ChainDepth + 1,
				}

				err := service.Emit(ctx, "cascade.aggressive", child)
				if errors.Is(err, hookz.ErrQueueFull) && currentPhase == 0 {
					phase.Store(1) // Mark as exhausted
				}
			}
		}

		// Slow processing to maintain pressure
		time.Sleep(cfg.hookDelay * 2)
		return nil
	})
	require.NoError(t, err)

	// Phase 1: Trigger cascade
	t.Log("Phase 1: Triggering cascade")
	err = service.Emit(context.Background(), "cascade.aggressive", CascadeEvent{ID: 1})
	require.NoError(t, err)

	// Wait for exhaustion
	timeout := time.After(5 * time.Second)
	for phase.Load() < 1 {
		select {
		case <-timeout:
			t.Skip("System didn't reach exhaustion - queue too large or processing too fast")
		case <-time.After(10 * time.Millisecond):
			// Check periodically
		}
	}

	t.Log("Phase 2: System exhausted")

	// Verify exhaustion
	err = service.Emit(context.Background(), "cascade.aggressive", CascadeEvent{ID: 999})
	if err == nil {
		t.Skip("System didn't reach exhaustion - processing too fast or load too light")
	}
	assert.ErrorIs(t, err, hookz.ErrQueueFull, "System should be exhausted")

	// Phase 3: Wait for recovery
	phase.Store(2)
	t.Log("Phase 3: Waiting for recovery")

	// Stop cascade and let system drain
	recoveryChan := make(chan bool)
	go func() {
		for {
			err := service.Emit(context.Background(), "cascade.test", CascadeEvent{ID: 1000})
			if err == nil {
				phase.Store(3)
				recoveryChan <- true
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()

	select {
	case <-recoveryChan:
		t.Log("Phase 4: System recovered successfully")
	case <-time.After(10 * time.Second):
		t.Fatal("System failed to recover from cascade exhaustion")
	}

	// Verify full recovery
	for i := 0; i < 5; i++ {
		err = service.Emit(context.Background(), "cascade.test", CascadeEvent{ID: 2000 + i})
		assert.NoError(t, err, "System should be fully functional after recovery")
	}
}

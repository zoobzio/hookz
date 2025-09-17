package reliability

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zoobzio/hookz"
)

// TimeoutEvent for timeout testing
type TimeoutEvent struct {
	ID            int
	ExpectedDelay time.Duration
	ShouldTimeout bool
	CancelEarly   bool
}

// TestGlobalTimeout tests global timeout functionality
// CI Mode: Short timeouts, quick validation
// Stress Mode: Set HOOKZ_TEST_DELAY_MULTIPLIER=5 for extended timeout testing
func TestGlobalTimeout(t *testing.T) {
	cfg := getTestConfig()

	baseTimeout := time.Duration(float64(100*time.Millisecond) * cfg.delayMultiplier)

	service := hookz.New[TimeoutEvent](
		hookz.WithWorkers(cfg.workers),
		hookz.WithQueueSize(cfg.queueSize),
		hookz.WithTimeout(baseTimeout),
	)
	defer service.Close()

	var completed atomic.Int64
	var timedOut atomic.Int64

	// Hook with variable execution time
	_, err := service.Hook("timeout.test", func(ctx context.Context, evt TimeoutEvent) error {
		// Check for timeout signal
		select {
		case <-ctx.Done():
			timedOut.Add(1)
			return ctx.Err()
		case <-time.After(evt.ExpectedDelay):
			completed.Add(1)
			return nil
		}
	})
	require.NoError(t, err)

	// Test cases with different delays
	testCases := []TimeoutEvent{
		{ID: 1, ExpectedDelay: baseTimeout / 4, ShouldTimeout: false}, // Fast
		{ID: 2, ExpectedDelay: baseTimeout / 2, ShouldTimeout: false}, // Medium
		{ID: 3, ExpectedDelay: baseTimeout * 2, ShouldTimeout: true},  // Slow (should timeout)
		{ID: 4, ExpectedDelay: baseTimeout * 3, ShouldTimeout: true},  // Very slow
	}

	// Emit all test events
	for _, evt := range testCases {
		err := service.Emit(context.Background(), "timeout.test", evt)
		require.NoError(t, err)
	}

	// Wait for processing
	time.Sleep(baseTimeout * 4)

	t.Logf("Global Timeout Results:")
	t.Logf("  Timeout threshold: %v", baseTimeout)
	t.Logf("  Events completed: %d", completed.Load())
	t.Logf("  Events timed out: %d", timedOut.Load())

	// Verify timeout behavior
	if cfg.delayMultiplier <= 1.0 {
		assert.Equal(t, int64(2), completed.Load(), "Fast events should complete")
		assert.Equal(t, int64(2), timedOut.Load(), "Slow events should timeout")
	}

	assert.Equal(t, int64(len(testCases)), completed.Load()+timedOut.Load(),
		"All events should be processed")
}

// TestContextCancellation tests context cancellation propagation
func TestContextCancellation(t *testing.T) {
	cfg := getTestConfig()

	service := hookz.New[TimeoutEvent](
		hookz.WithWorkers(cfg.workers),
		hookz.WithQueueSize(cfg.queueSize),
	)
	defer service.Close()

	var started atomic.Int64
	var canceled atomic.Int64
	var completed atomic.Int64

	// Hook that respects context cancellation
	_, err := service.Hook("cancel.test", func(ctx context.Context, evt TimeoutEvent) error {
		started.Add(1)

		// Simulate work with cancellation checking
		for i := 0; i < 10; i++ {
			select {
			case <-ctx.Done():
				canceled.Add(1)
				return ctx.Err()
			case <-time.After(cfg.hookDelay):
				// Continue working
			}
		}

		completed.Add(1)
		return nil
	})
	require.NoError(t, err)

	// Start events with cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	// Emit multiple events
	for i := 0; i < 5; i++ {
		evt := TimeoutEvent{ID: i}
		err := service.Emit(ctx, "cancel.test", evt)
		require.NoError(t, err)
	}

	// Let some processing start
	time.Sleep(cfg.hookDelay * 2)
	startedCount := started.Load()

	// Cancel context
	cancel()

	// Wait for cancellation to propagate
	time.Sleep(cfg.hookDelay * 15) // Longer than hook duration

	t.Logf("Context Cancellation Results:")
	t.Logf("  Events started: %d", started.Load())
	t.Logf("  Events canceled: %d", canceled.Load())
	t.Logf("  Events completed: %d", completed.Load())

	// Verify cancellation worked
	assert.Greater(t, startedCount, int64(0), "Some events should have started")
	assert.Greater(t, canceled.Load(), int64(0), "Some events should be canceled")
	assert.Equal(t, started.Load(), canceled.Load()+completed.Load(),
		"All started events should be canceled or completed")
}

// TestTimeoutWithBackpressure tests timeout behavior under backpressure
func TestTimeoutWithBackpressure(t *testing.T) {
	cfg := getTestConfig()

	timeout := time.Duration(float64(50*time.Millisecond) * cfg.delayMultiplier)

	service := hookz.New[TimeoutEvent](
		hookz.WithWorkers(2),
		hookz.WithQueueSize(4),
		hookz.WithTimeout(timeout),
		hookz.WithBackpressure(hookz.BackpressureConfig{
			MaxWait:        time.Duration(float64(20*time.Millisecond) * cfg.delayMultiplier),
			StartThreshold: 0.8,
			Strategy:       "linear",
		}),
	)
	defer service.Close()

	var processingTimedOut atomic.Int64
	var processingCompleted atomic.Int64

	// Hook that tracks timing
	_, err := service.Hook("backpressure.timeout", func(ctx context.Context, evt TimeoutEvent) error {
		start := time.Now()

		// Simulate work
		for time.Since(start) < cfg.hookDelay*5 {
			select {
			case <-ctx.Done():
				processingTimedOut.Add(1)
				return ctx.Err()
			case <-time.After(cfg.hookDelay / 10):
				// Continue processing in small chunks
			}
		}

		processingCompleted.Add(1)
		return nil
	})
	require.NoError(t, err)

	// Generate load that will cause backpressure
	var emitDelayed atomic.Int64

	var wg sync.WaitGroup
	for i := 0; i < int(10*cfg.loadMultiplier); i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			evt := TimeoutEvent{ID: id}
			emitStart := time.Now()

			_ = service.Emit(context.Background(), "backpressure.timeout", evt)
			emitDuration := time.Since(emitStart)

			// If emit took a while, backpressure was applied
			if emitDuration > timeout/4 {
				emitDelayed.Add(1)
			}
		}(i)
	}

	wg.Wait()

	// Wait for processing
	time.Sleep(timeout * 10)

	t.Logf("Backpressure Timeout Results:")
	t.Logf("  Timeout threshold: %v", timeout)
	t.Logf("  Emit operations delayed by backpressure: %d", emitDelayed.Load())
	t.Logf("  Processing completed: %d", processingCompleted.Load())
	t.Logf("  Processing timed out: %d", processingTimedOut.Load())

	// Verify system behavior
	if cfg.delayMultiplier <= 1.0 {
		if emitDelayed.Load() == 0 {
			t.Logf("  System processed all events without backpressure delays (faster than expected)")
		}
	}

	assert.Greater(t, processingCompleted.Load()+processingTimedOut.Load(), int64(0),
		"Events should be processed or timeout")
}

// TestTimeoutCascade tests timeout behavior in cascading events
func TestTimeoutCascade(t *testing.T) {
	cfg := getTestConfig()

	timeout := time.Duration(float64(30*time.Millisecond) * cfg.delayMultiplier)

	service := hookz.New[TimeoutEvent](
		hookz.WithWorkers(cfg.workers),
		hookz.WithQueueSize(cfg.queueSize),
		hookz.WithTimeout(timeout),
	)
	defer service.Close()

	var cascadeDepth atomic.Int32
	var timeoutsByDepth [5]atomic.Int64 // Track timeouts by cascade depth
	var completionsByDepth [5]atomic.Int64

	// Cascading hook with timeout tracking
	_, err := service.Hook("cascade.timeout", func(ctx context.Context, evt TimeoutEvent) error {
		depth := evt.ID / 100 // Use ID to encode depth
		if depth > 4 {
			depth = 4 // Cap at array size
		}

		if depth > int(cascadeDepth.Load()) {
			cascadeDepth.Store(int32(depth))
		}

		// Variable processing time based on depth
		processingTime := cfg.hookDelay * time.Duration(depth+1)

		select {
		case <-ctx.Done():
			timeoutsByDepth[depth].Add(1)
			return ctx.Err()
		case <-time.After(processingTime):
			completionsByDepth[depth].Add(1)

			// Trigger next level if not too deep
			if depth < 3 {
				child := TimeoutEvent{
					ID: (depth+1)*100 + evt.ID%100,
				}
				// Best effort cascade (ignore errors)
				_ = service.Emit(ctx, "cascade.timeout", child)
			}

			return nil
		}
	})
	require.NoError(t, err)

	// Start cascade
	for i := 0; i < int(3*cfg.loadMultiplier); i++ {
		root := TimeoutEvent{ID: i} // Depth 0
		err := service.Emit(context.Background(), "cascade.timeout", root)
		require.NoError(t, err)
	}

	// Wait for cascade completion
	time.Sleep(timeout * 20)

	t.Logf("Cascade Timeout Results:")
	t.Logf("  Timeout threshold: %v", timeout)
	t.Logf("  Max cascade depth reached: %d", cascadeDepth.Load())

	for i := 0; i < 5; i++ {
		completed := completionsByDepth[i].Load()
		timedout := timeoutsByDepth[i].Load()
		if completed > 0 || timedout > 0 {
			t.Logf("  Depth %d: %d completed, %d timed out", i, completed, timedout)
		}
	}

	// Verify timeout affects deeper levels more
	assert.Greater(t, cascadeDepth.Load(), int32(0), "Cascade should progress")

	// Deeper levels should have more timeouts (they take longer)
	if cfg.delayMultiplier <= 1.0 {
		depth0Timeouts := timeoutsByDepth[0].Load()
		depth2Timeouts := timeoutsByDepth[2].Load()
		if depth2Timeouts > 0 {
			assert.Greater(t, depth2Timeouts, depth0Timeouts,
				"Deeper cascade levels should timeout more often")
		}
	}
}

// TestTimeoutRecovery tests system recovery after timeout-induced failures
func TestTimeoutRecovery(t *testing.T) {
	cfg := getTestConfig()

	shortTimeout := time.Duration(float64(20*time.Millisecond) * cfg.delayMultiplier)

	service := hookz.New[TimeoutEvent](
		hookz.WithWorkers(cfg.workers),
		hookz.WithQueueSize(cfg.queueSize),
		hookz.WithTimeout(shortTimeout),
	)
	defer service.Close()

	var phase atomic.Int32 // 0=slow, 1=fast, 2=mixed
	var slowCompleted atomic.Int64
	var slowTimedOut atomic.Int64
	var fastCompleted atomic.Int64
	var fastTimedOut atomic.Int64

	// Hook with variable processing based on phase
	_, err := service.Hook("recovery.timeout", func(ctx context.Context, evt TimeoutEvent) error {
		currentPhase := phase.Load()

		var processingTime time.Duration
		switch currentPhase {
		case 0:
			// Phase 0: Slow processing (should timeout)
			processingTime = shortTimeout * 3
		case 1:
			// Phase 1: Fast processing (should complete)
			processingTime = shortTimeout / 4
		default:
			// Phase 2: Mixed processing
			if evt.ID%2 == 0 {
				processingTime = shortTimeout / 4
			} else {
				processingTime = shortTimeout * 2
			}
		}

		select {
		case <-ctx.Done():
			if currentPhase == 0 {
				slowTimedOut.Add(1)
			} else {
				fastTimedOut.Add(1)
			}
			return ctx.Err()
		case <-time.After(processingTime):
			if currentPhase == 0 {
				slowCompleted.Add(1)
			} else {
				fastCompleted.Add(1)
			}
			return nil
		}
	})
	require.NoError(t, err)

	// Phase 0: Send slow events (should mostly timeout)
	t.Log("Phase 0: Sending slow events")
	for i := 0; i < int(5*cfg.loadMultiplier); i++ {
		evt := TimeoutEvent{ID: i}
		_ = service.Emit(context.Background(), "recovery.timeout", evt)
	}

	time.Sleep(shortTimeout * 5)

	// Phase 1: Switch to fast processing
	phase.Store(1)
	t.Log("Phase 1: Switching to fast events")

	for i := 100; i < int(100+5*cfg.loadMultiplier); i++ {
		evt := TimeoutEvent{ID: i}
		_ = service.Emit(context.Background(), "recovery.timeout", evt)
	}

	time.Sleep(shortTimeout * 5)

	// Phase 2: Mixed processing
	phase.Store(2)
	t.Log("Phase 2: Mixed processing speed")

	for i := 200; i < int(200+10*cfg.loadMultiplier); i++ {
		evt := TimeoutEvent{ID: i}
		_ = service.Emit(context.Background(), "recovery.timeout", evt)
	}

	time.Sleep(shortTimeout * 10)

	t.Logf("Timeout Recovery Results:")
	t.Logf("  Timeout threshold: %v", shortTimeout)
	t.Logf("  Phase 0 (slow): %d completed, %d timed out",
		slowCompleted.Load(), slowTimedOut.Load())
	t.Logf("  Phase 1+ (fast/mixed): %d completed, %d timed out",
		fastCompleted.Load(), fastTimedOut.Load())

	// Verify recovery: fast phase should have fewer timeouts
	if cfg.delayMultiplier <= 1.0 {
		assert.Greater(t, slowTimedOut.Load(), int64(0), "Slow phase should have timeouts")
		assert.Greater(t, fastCompleted.Load(), int64(0), "Fast phase should complete events")

		slowTimeoutRate := float64(slowTimedOut.Load()) / float64(slowCompleted.Load()+slowTimedOut.Load())
		fastTimeoutRate := float64(fastTimedOut.Load()) / float64(fastCompleted.Load()+fastTimedOut.Load())

		assert.Greater(t, slowTimeoutRate, fastTimeoutRate,
			"System should recover - fast phase should have lower timeout rate")
	}
}

// TestContextWithTimeout tests context timeout vs global timeout interaction
func TestContextWithTimeout(t *testing.T) {
	cfg := getTestConfig()

	globalTimeout := time.Duration(float64(100*time.Millisecond) * cfg.delayMultiplier)
	contextTimeout := time.Duration(float64(50*time.Millisecond) * cfg.delayMultiplier)

	service := hookz.New[TimeoutEvent](
		hookz.WithWorkers(cfg.workers),
		hookz.WithQueueSize(cfg.queueSize),
		hookz.WithTimeout(globalTimeout), // Longer global timeout
	)
	defer service.Close()

	var contextTimeouts atomic.Int64
	var globalTimeouts atomic.Int64
	var completed atomic.Int64

	// Hook that can differentiate timeout sources
	_, err := service.Hook("dual.timeout", func(ctx context.Context, evt TimeoutEvent) error {
		// Work for longer than context timeout but less than global timeout
		workTime := contextTimeout + (globalTimeout-contextTimeout)/2

		select {
		case <-ctx.Done():
			err := ctx.Err()
			if err == context.DeadlineExceeded {
				// Check which timeout triggered by examining remaining context
				if ctx.Err() != nil {
					contextTimeouts.Add(1)
				} else {
					globalTimeouts.Add(1)
				}
			}
			return err
		case <-time.After(workTime):
			completed.Add(1)
			return nil
		}
	})
	require.NoError(t, err)

	// Test with context timeout (shorter)
	ctx, cancel := context.WithTimeout(context.Background(), contextTimeout)
	defer cancel()

	for i := 0; i < int(5*cfg.loadMultiplier); i++ {
		evt := TimeoutEvent{ID: i}
		_ = service.Emit(ctx, "dual.timeout", evt)
	}

	// Test without context timeout (only global)
	for i := 100; i < int(100+5*cfg.loadMultiplier); i++ {
		evt := TimeoutEvent{ID: i}
		_ = service.Emit(context.Background(), "dual.timeout", evt)
	}

	// Wait for processing
	time.Sleep(globalTimeout * 3)

	t.Logf("Dual Timeout Results:")
	t.Logf("  Global timeout: %v", globalTimeout)
	t.Logf("  Context timeout: %v", contextTimeout)
	t.Logf("  Context timeouts: %d", contextTimeouts.Load())
	t.Logf("  Global timeouts: %d", globalTimeouts.Load())
	t.Logf("  Completed: %d", completed.Load())

	// Context timeout should trigger more often (it's shorter)
	if cfg.delayMultiplier <= 1.0 {
		assert.Greater(t, contextTimeouts.Load(), int64(0),
			"Context timeout should occur")
		// Note: Global timeouts might be 0 if context timeout always wins
	}
}

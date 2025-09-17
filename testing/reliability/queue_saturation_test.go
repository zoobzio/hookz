package reliability

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zoobzio/hookz"
)

// Test configuration that can be overridden via environment variables
// This allows CI to run with safe defaults while stress testing can push limits
type testConfig struct {
	// Base load parameters
	workers      int
	queueSize    int
	eventCount   int
	goroutines   int
	hookDelay    time.Duration
	emitInterval time.Duration

	// Stress multipliers (1.0 for CI, higher for stress testing)
	loadMultiplier  float64
	delayMultiplier float64

	// Failure injection probabilities (0.0 for CI, higher for chaos)
	panicRate float64
	errorRate float64
	slowRate  float64

	// Timeout and limits
	testTimeout time.Duration
	maxDropRate float64 // Maximum acceptable drop rate
}

// getTestConfig returns configuration from environment or safe defaults
func getTestConfig() testConfig {
	cfg := testConfig{
		// Safe CI defaults
		workers:         10,
		queueSize:       20,
		eventCount:      100,
		goroutines:      5,
		hookDelay:       1 * time.Millisecond,
		emitInterval:    100 * time.Microsecond,
		loadMultiplier:  1.0,
		delayMultiplier: 1.0,
		panicRate:       0.0,
		errorRate:       0.0,
		slowRate:        0.0,
		testTimeout:     10 * time.Second,
		maxDropRate:     5.0, // Allow up to 5% drops in CI
	}

	// Override from environment for stress testing
	if v := os.Getenv("HOOKZ_TEST_LOAD_MULTIPLIER"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.loadMultiplier = f
			cfg.eventCount = int(float64(cfg.eventCount) * f)
			cfg.goroutines = int(float64(cfg.goroutines) * f)
		}
	}

	if v := os.Getenv("HOOKZ_TEST_DELAY_MULTIPLIER"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.delayMultiplier = f
			cfg.hookDelay = time.Duration(float64(cfg.hookDelay) * f)
		}
	}

	if v := os.Getenv("HOOKZ_TEST_PANIC_RATE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.panicRate = f
		}
	}

	if v := os.Getenv("HOOKZ_TEST_ERROR_RATE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.errorRate = f
		}
	}

	if v := os.Getenv("HOOKZ_TEST_SLOW_RATE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.slowRate = f
		}
	}

	if v := os.Getenv("HOOKZ_TEST_MAX_DROP_RATE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.maxDropRate = f
		}
	}

	return cfg
}

// TestQueueSaturationGradual verifies system behavior as queue approaches saturation
// CI Mode: Light load, no drops expected
// Stress Mode: Set HOOKZ_TEST_LOAD_MULTIPLIER=10 to find saturation point
func TestQueueSaturationGradual(t *testing.T) {
	cfg := getTestConfig()

	service := hookz.New[int](
		hookz.WithWorkers(cfg.workers),
		hookz.WithQueueSize(cfg.queueSize),
	)
	defer service.Close()

	// Track metrics
	var processed atomic.Int64
	var emitted atomic.Int64
	var dropped atomic.Int64

	// Register hook with configurable delay
	_, err := service.Hook("test.event", func(ctx context.Context, value int) error {
		time.Sleep(cfg.hookDelay)
		processed.Add(1)
		return nil
	})
	require.NoError(t, err)

	// Start load generation
	ctx, cancel := context.WithTimeout(context.Background(), cfg.testTimeout)
	defer cancel()

	var wg sync.WaitGroup

	// Multiple goroutines simulating concurrent load
	for i := 0; i < cfg.goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < cfg.eventCount; j++ {
				select {
				case <-ctx.Done():
					return
				default:
					emitted.Add(1)
					err := service.Emit(ctx, "test.event", j)
					if errors.Is(err, hookz.ErrQueueFull) {
						dropped.Add(1)
					}

					// Controlled emission rate
					if cfg.emitInterval > 0 {
						time.Sleep(cfg.emitInterval)
					}
				}
			}
		}(i)
	}

	wg.Wait()

	// Allow time for processing to complete
	time.Sleep(cfg.hookDelay * 10)

	// Calculate metrics
	totalEmitted := emitted.Load()
	totalDropped := dropped.Load()
	totalProcessed := processed.Load()

	dropRate := float64(totalDropped) * 100 / float64(totalEmitted)

	t.Logf("Queue Saturation Results:")
	t.Logf("  Configuration: workers=%d, queue=%d, load_multiplier=%.1f",
		cfg.workers, cfg.queueSize, cfg.loadMultiplier)
	t.Logf("  Events emitted: %d", totalEmitted)
	t.Logf("  Events dropped: %d (%.2f%%)", totalDropped, dropRate)
	t.Logf("  Events processed: %d", totalProcessed)

	// Assertions based on configuration
	if cfg.loadMultiplier <= 1.0 {
		// CI mode: Expect minimal drops
		assert.LessOrEqual(t, dropRate, cfg.maxDropRate,
			"Drop rate %.2f%% exceeds maximum %.2f%% for CI configuration",
			dropRate, cfg.maxDropRate)
	} else {
		// Stress mode: Just report, don't fail
		if dropRate > 0 {
			t.Logf("  STRESS: Found saturation point at %.2f%% drop rate", dropRate)
		}
	}
}

// TestQueueSaturationBurst tests behavior under sudden load spikes
// CI Mode: Small controlled bursts
// Stress Mode: Set HOOKZ_TEST_LOAD_MULTIPLIER=20 for massive bursts
func TestQueueSaturationBurst(t *testing.T) {
	cfg := getTestConfig()

	service := hookz.New[int](
		hookz.WithWorkers(cfg.workers),
		hookz.WithQueueSize(cfg.queueSize),
	)
	defer service.Close()

	// Slower hook to increase queue pressure
	_, err := service.Hook("burst.event", func(ctx context.Context, value int) error {
		time.Sleep(cfg.hookDelay * 2)
		return nil
	})
	require.NoError(t, err)

	// Generate burst load
	burstSize := int(float64(cfg.queueSize*2) * cfg.loadMultiplier)

	var dropped atomic.Int64
	var succeeded atomic.Int64

	// Send burst as fast as possible
	start := time.Now()
	for i := 0; i < burstSize; i++ {
		err := service.Emit(context.Background(), "burst.event", i)
		if errors.Is(err, hookz.ErrQueueFull) {
			dropped.Add(1)
		} else {
			succeeded.Add(1)
		}
	}
	burstDuration := time.Since(start)

	dropRate := float64(dropped.Load()) * 100 / float64(burstSize)

	t.Logf("Burst Test Results:")
	t.Logf("  Burst size: %d events in %v", burstSize, burstDuration)
	t.Logf("  Succeeded: %d", succeeded.Load())
	t.Logf("  Dropped: %d (%.2f%%)", dropped.Load(), dropRate)
	t.Logf("  Rate: %.0f events/sec", float64(burstSize)/burstDuration.Seconds())

	// CI mode expects to handle reasonable bursts
	if cfg.loadMultiplier <= 1.0 {
		assert.Less(t, dropRate, 50.0, "Burst caused excessive drops in CI mode")
	}
}

// TestQueueSaturationWithFailures tests saturation when hooks fail/panic
// CI Mode: No failures injected
// Stress Mode: Set HOOKZ_TEST_PANIC_RATE=0.1 for 10% panic rate
func TestQueueSaturationWithFailures(t *testing.T) {
	cfg := getTestConfig()

	service := hookz.New[int](
		hookz.WithWorkers(cfg.workers),
		hookz.WithQueueSize(cfg.queueSize),
	)
	defer service.Close()

	var hookExecutions atomic.Int64
	var hookPanics atomic.Int64
	var hookErrors atomic.Int64

	// Register hook that sometimes fails
	_, err := service.Hook("failure.event", func(ctx context.Context, value int) error {
		execNum := hookExecutions.Add(1)

		// Inject failures based on configuration
		if cfg.panicRate > 0 && float64(execNum%100) < cfg.panicRate*100 {
			hookPanics.Add(1)
			panic(fmt.Sprintf("injected panic at execution %d", execNum))
		}

		if cfg.errorRate > 0 && float64(execNum%100) < cfg.errorRate*100 {
			hookErrors.Add(1)
			return fmt.Errorf("injected error at execution %d", execNum)
		}

		// Normal processing
		time.Sleep(cfg.hookDelay)
		return nil
	})
	require.NoError(t, err)

	// Generate load
	var emitted atomic.Int64
	var dropped atomic.Int64

	ctx, cancel := context.WithTimeout(context.Background(), cfg.testTimeout)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < cfg.goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < cfg.eventCount; j++ {
				select {
				case <-ctx.Done():
					return
				default:
					emitted.Add(1)
					err := service.Emit(ctx, "failure.event", j)
					if errors.Is(err, hookz.ErrQueueFull) {
						dropped.Add(1)
					}
				}
			}
		}()
	}

	wg.Wait()
	time.Sleep(cfg.hookDelay * 10) // Allow processing to complete

	t.Logf("Failure Injection Results:")
	t.Logf("  Total emitted: %d", emitted.Load())
	t.Logf("  Total dropped: %d", dropped.Load())
	t.Logf("  Hook executions: %d", hookExecutions.Load())
	t.Logf("  Hook panics: %d (%.2f%%)", hookPanics.Load(),
		float64(hookPanics.Load())*100/float64(hookExecutions.Load()))
	t.Logf("  Hook errors: %d (%.2f%%)", hookErrors.Load(),
		float64(hookErrors.Load())*100/float64(hookExecutions.Load()))

	// Verify system survived failures
	assert.NotZero(t, hookExecutions.Load(), "No hooks executed")

	// In stress mode with failures, verify resilience
	if cfg.panicRate > 0 || cfg.errorRate > 0 {
		t.Logf("  STRESS: System survived %.0f%% failure rate",
			(cfg.panicRate+cfg.errorRate)*100)
	}
}

// TestQueueExhaustion tests complete queue exhaustion and recovery
// CI Mode: Quick exhaustion and recovery
// Stress Mode: Prolonged exhaustion testing
func TestQueueExhaustion(t *testing.T) {
	cfg := getTestConfig()

	// Small queue for easier exhaustion
	service := hookz.New[int](
		hookz.WithWorkers(2),
		hookz.WithQueueSize(4),
	)
	defer service.Close()

	// Very slow hook to guarantee exhaustion
	blockTime := time.Duration(float64(100*time.Millisecond) * cfg.delayMultiplier)

	var blocked atomic.Bool
	blocked.Store(true)

	_, err := service.Hook("exhaust.event", func(ctx context.Context, value int) error {
		// Block until released
		for blocked.Load() {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Millisecond):
				// Check periodically
			}
		}
		return nil
	})
	require.NoError(t, err)

	// Fill queue completely
	var exhausted bool
	for i := 0; i < 100; i++ {
		err := service.Emit(context.Background(), "exhaust.event", i)
		if errors.Is(err, hookz.ErrQueueFull) {
			exhausted = true
			t.Logf("Queue exhausted after %d events", i)
			break
		}
	}

	assert.True(t, exhausted, "Failed to exhaust queue")

	// Verify queue remains exhausted
	err = service.Emit(context.Background(), "exhaust.event", 999)
	assert.ErrorIs(t, err, hookz.ErrQueueFull, "Queue should be exhausted")

	// Release blocks and verify recovery
	blocked.Store(false)
	time.Sleep(blockTime) // Wait for queue to drain

	// Queue should accept events again
	err = service.Emit(context.Background(), "exhaust.event", 1000)
	assert.NoError(t, err, "Queue should have recovered from exhaustion")

	t.Log("Queue successfully recovered from exhaustion")
}

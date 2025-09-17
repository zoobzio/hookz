# Hookz Reliability Test Suite

This directory contains comprehensive reliability tests for the hookz event system. The tests are designed to pass consistently in CI environments while being parameterizable for stress testing and chaos engineering.

## Test Philosophy

These tests follow the "CI-Safe, Stress-Capable" design pattern:
- **CI Mode**: Conservative parameters ensure reliable test execution in automated environments
- **Stress Mode**: Environment variables allow pushing the system to breaking points for reliability analysis

## Test Categories

### Queue Saturation Tests (`queue_saturation_test.go`)
Tests system behavior as event queues approach and exceed capacity.

**Key Tests:**
- `TestQueueSaturationGradual`: Progressive load increase to find saturation points
- `TestQueueSaturationBurst`: Sudden load spikes and recovery patterns  
- `TestQueueSaturationWithFailures`: Saturation behavior when hooks panic/error
- `TestQueueExhaustion`: Complete queue exhaustion and recovery cycles

### Cascade Failure Tests (`cascade_failure_test.go`) 
Tests cascading event chains that can amplify failures across the system.

**Key Tests:**
- `TestCascadeFailureSimple`: Basic event chain propagation
- `TestCascadeWithBackpressure`: Cascade behavior with traffic shaping
- `TestCascadeMemoryPressure`: Memory accumulation in deep cascades
- `TestCascadeDeadlock`: Detection of circular dependency deadlocks
- `TestCascadeRecovery`: System recovery after cascade-induced exhaustion

### Memory Pressure Tests (`memory_pressure_test.go`)
Tests system behavior under various memory allocation patterns.

**Key Tests:**
- `TestMemoryPressureGradual`: Progressive memory load increase
- `TestMemoryLeakDetection`: Hook registration/deregistration leak detection
- `TestMemoryWithOverflow`: Memory behavior with overflow queues
- `TestMemoryPressureConcurrent`: Memory pressure under concurrent access

### Resilience Feature Tests (`resilience_test.go`)
Tests backpressure and overflow queue effectiveness under stress.

**Key Tests:**
- `TestBackpressureEffectiveness`: Backpressure strategies (fixed/linear/exponential)
- `TestOverflowCapacityLimits`: Overflow queue eviction strategies (FIFO/LIFO/reject)
- `TestBackpressureWithOverflow`: Combined resilience feature interaction
- `TestOverflowDrainEfficiency`: Overflow queue drain performance

### Timeout and Context Tests (`timeout_test.go`)
Tests timeout handling and context cancellation propagation.

**Key Tests:**
- `TestGlobalTimeout`: Global timeout enforcement across all hooks
- `TestContextCancellation`: Context cancellation signal propagation
- `TestTimeoutWithBackpressure`: Timeout interaction with backpressure delays
- `TestTimeoutCascade`: Timeout behavior in cascading event chains
- `TestContextWithTimeout`: Context timeout vs global timeout precedence

## Configuration

All tests use a common configuration system that can be controlled via environment variables:

### Load Control
- `HOOKZ_TEST_LOAD_MULTIPLIER`: Multiplies event counts and goroutines (default: 1.0)
- `HOOKZ_TEST_DELAY_MULTIPLIER`: Multiplies processing delays and timeouts (default: 1.0)

### Failure Injection  
- `HOOKZ_TEST_PANIC_RATE`: Probability of hook panics (0.0-1.0, default: 0.0)
- `HOOKZ_TEST_ERROR_RATE`: Probability of hook errors (0.0-1.0, default: 0.0) 
- `HOOKZ_TEST_SLOW_RATE`: Probability of slow hook execution (0.0-1.0, default: 0.0)

### Quality Thresholds
- `HOOKZ_TEST_MAX_DROP_RATE`: Maximum acceptable event drop percentage (default: 5.0)

## Usage Examples

### CI/Local Development (Default)
```bash
# Run with safe defaults
go test ./testing/reliability/...

# Specific test with moderate load
HOOKZ_TEST_LOAD_MULTIPLIER=2 go test -v -run TestQueueSaturationGradual
```

### Stress Testing
```bash
# High load with failure injection
HOOKZ_TEST_LOAD_MULTIPLIER=10 \
HOOKZ_TEST_DELAY_MULTIPLIER=5 \
HOOKZ_TEST_PANIC_RATE=0.05 \
HOOKZ_TEST_ERROR_RATE=0.1 \
go test -v -timeout 30m ./testing/reliability/...

# Memory pressure testing
HOOKZ_TEST_LOAD_MULTIPLIER=20 go test -v -run TestMemory

# Cascade failure analysis
HOOKZ_TEST_LOAD_MULTIPLIER=15 \
HOOKZ_TEST_ERROR_RATE=0.02 \
go test -v -run TestCascade
```

### Performance Profiling
```bash
# CPU profiling under stress
HOOKZ_TEST_LOAD_MULTIPLIER=5 \
go test -v -cpuprofile=cpu.prof -run TestQueueSaturationBurst

# Memory profiling for leak detection  
HOOKZ_TEST_LOAD_MULTIPLIER=8 \
go test -v -memprofile=mem.prof -run TestMemoryLeak
```

## Test Design Patterns

### Graduated Destruction
Tests apply increasing stress levels rather than immediate maximum load:

```go
// Progressive load increase
for load := 1.0; load <= maxLoad; load *= 2 {
    results := applyLoad(load)
    if results.failureRate > threshold {
        t.Logf("System breaking point: %.1fx load", load)
        break
    }
}
```

### Parameterized Assertions
Assertions adapt based on test configuration:

```go
if cfg.loadMultiplier <= 1.0 {
    // CI mode: Strict requirements
    assert.LessOrEqual(t, dropRate, 5.0)
} else {
    // Stress mode: Just report findings
    t.Logf("STRESS: %.2f%% drop rate at %.1fx load", dropRate, cfg.loadMultiplier)
}
```

### Deterministic Failure Points
Tests find consistent failure patterns rather than random failures:

```go
// Fill queue to exact capacity
for i := 0; i < queueSize + workerCount; i++ {
    err := service.Emit(ctx, "test", event)
    if errors.Is(err, hookz.ErrQueueFull) {
        t.Logf("Queue saturated after %d events", i)
        break
    }
}
```

### Recovery Verification
Tests verify system recovery after induced failures:

```go
// Induce failure
causeSystemExhaustion()

// Verify failure state
assert.ErrorIs(t, service.Emit(...), hookz.ErrQueueFull)

// Allow recovery
stopFailureInduction()
time.Sleep(recoveryTime)

// Verify recovery
assert.NoError(t, service.Emit(...))
```

## Interpreting Results

### CI Mode Results
- **Drop rates < 5%**: System handles normal load well
- **Memory growth < 10MB**: No significant leaks detected
- **Recovery time < 1s**: Fast recovery from transient failures

### Stress Mode Results
- **Breaking point identification**: "System saturated at 8.5x load"  
- **Cascade amplification factors**: "10 root events → 847 total events"
- **Memory scaling characteristics**: "Linear growth: 2.3MB per 1000 events"
- **Resilience effectiveness**: "Backpressure reduced drops from 67% to 12%"

### Failure Mode Analysis
Look for patterns in test outputs:
- **Gradual degradation**: Good - system fails gracefully
- **Cliff effects**: Concerning - sudden complete failures  
- **Recovery patterns**: Fast recovery indicates good resilience
- **Resource leak trends**: Memory/goroutine growth over time

## Integration with CI

### Required Test Passes
All tests must pass in CI mode (default configuration) for merge approval.

### Performance Regression Detection  
Benchmark integration tracks performance metrics:
- Event processing throughput
- Memory usage patterns  
- Queue saturation thresholds
- Recovery time measurements

### Stress Test Scheduling
Regular stress test runs provide ongoing reliability assessment:
- Nightly: Moderate stress (5x load multiplier)
- Weekly: High stress (20x load multiplier) 
- Release: Full chaos (failure injection enabled)

## Contributing

When adding reliability tests:

1. **Follow the parameterization pattern** - Support both CI and stress modes
2. **Use graduated stress application** - Start low, increase systematically  
3. **Verify recovery behavior** - Don't just break things, ensure they recover
4. **Document breaking points** - Log when/how the system fails
5. **Test real failure modes** - Focus on actual production failure scenarios

### Test Naming Convention
- `Test[Component][FailureType]`: e.g., `TestQueueSaturationBurst`
- `Test[Component][FailureType][Condition]`: e.g., `TestCascadeWithBackpressure`

### Assertion Guidelines  
- CI mode: Strict assertions that must pass
- Stress mode: Informational logging with optional assertions
- Always include context in failure messages
- Report both absolute numbers and rates/percentages
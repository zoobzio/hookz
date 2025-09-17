# Production Configuration Guide

This guide provides specific formulas and examples for configuring hookz in production environments based on benchmark-validated performance characteristics.

## Quick Configuration Calculator

Use this calculator to estimate your configuration needs:

```bash
#!/bin/bash
# Configuration Calculator for hookz

# Your inputs - adjust these values
events_per_second=5000
avg_processing_time_ms=2
burst_tolerance_factor=2.0

# Core capacity calculation
workers_float=$(echo "$events_per_second * $avg_processing_time_ms / 1000" | bc -l)
workers=$(echo "scale=0; $workers_float + 0.999" | bc -l | cut -d'.' -f1)  # Ceiling

# Queue sizing based on burst tolerance
queue=$(echo "scale=0; $workers * $burst_tolerance_factor" | bc -l | cut -d'.' -f1)

echo "=== Configuration Recommendations ==="
echo "Events per second: $events_per_second"
echo "Average processing time: ${avg_processing_time_ms}ms"
echo "Burst tolerance factor: $burst_tolerance_factor"
echo ""
echo "Recommended workers: $workers"
echo "Recommended queue size: $queue"
echo "Theoretical max rate: $(echo "$workers * 1000 / $avg_processing_time_ms" | bc -l | cut -d'.' -f1) events/sec"
```

## Configuration Formulas

### Core Capacity Formula
```
workers = ceiling(peak_events_per_second × avg_processing_time_seconds)
```

**Where:**
- `peak_events_per_second`: Your maximum expected event rate
- `avg_processing_time_seconds`: Average time all hooks take to process one event
- Use `ceiling()` to ensure adequate capacity

### Queue Sizing Formula  
```
queue_size = workers × burst_tolerance_factor
```

**Burst Tolerance Factors:**
- `1.5-2.0`: Conservative (low burst tolerance)
- `2.0-3.0`: Balanced (moderate bursts)
- `3.0-4.0`: High burst tolerance (traffic spikes)
- `>4.0`: Consider if architecture needs review

### Drop Rate Prediction
```
if arrival_rate <= processing_capacity:
    expected_drop_rate = 0%
elif queue_full_time > 0:
    expected_drop_rate = (arrival_rate - processing_capacity) / arrival_rate × 100%
```

## Real-World Configuration Examples

### Example 1: Web API Request Logging
```yaml
scenario: "api_request_logging"
description: "Log all API requests with basic metadata"

# Load characteristics
events_per_second: 1000
avg_processing_time_ms: 1  # Simple logging
peak_multiplier: 2.0       # 2x traffic spikes

# Calculated configuration
workers: 2                 # ceiling(1000 * 0.001) = 1, but use minimum 2
queue: 6                   # 2 workers × 3.0 burst factor
hook_count: "1-5"
expected_drops: "<0.1% under normal load"

# Monitoring thresholds
drop_rate_alert: 1%
latency_alert: 5μs
```

### Example 2: Data Transformation Pipeline
```yaml
scenario: "data_transformation"  
description: "Transform and enrich events before storage"

# Load characteristics
events_per_second: 500
avg_processing_time_ms: 10  # Database lookups, API calls
peak_multiplier: 1.5        # Moderate spikes

# Calculated configuration  
workers: 5                  # ceiling(500 * 0.010) = 5
queue: 15                   # 5 workers × 3.0 burst factor
hook_count: "5-15"
expected_drops: "1-3% during peak load"

# Monitoring thresholds
drop_rate_alert: 5%
latency_alert: 15μs
queue_depth_alert: 12       # 80% of queue capacity
```

### Example 3: External API Integration
```yaml
scenario: "external_api_calls"
description: "Send events to multiple external services"

# Load characteristics  
events_per_second: 200
avg_processing_time_ms: 50   # HTTP calls with timeout
peak_multiplier: 3.0         # Batch processing spikes

# Calculated configuration
workers: 10                  # ceiling(200 * 0.050) = 10  
queue: 30                    # 10 workers × 3.0 burst factor
hook_count: "1-10"
expected_drops: "Variable based on API latency"

# Monitoring thresholds
drop_rate_alert: 10%         # Higher tolerance for external dependencies
latency_alert: 25μs
timeout_handling: "Required" # Handle API timeouts gracefully
```

### Example 4: High-Throughput Analytics
```yaml
scenario: "analytics_ingestion"
description: "High-volume event processing for analytics"

# Load characteristics
events_per_second: 10000
avg_processing_time_ms: 2    # Fast aggregation and batching
peak_multiplier: 1.2         # Steady load with small spikes

# Calculated configuration
workers: 20                  # ceiling(10000 * 0.002) = 20
queue: 40                    # 20 workers × 2.0 (lower burst, higher steady)
hook_count: "1-25"
expected_drops: "<0.5% with proper configuration"

# Monitoring thresholds  
drop_rate_alert: 2%
latency_alert: 10μs
efficiency_alert: 75%       # Worker efficiency threshold
```

## Configuration Validation

### Pre-Deployment Testing
```bash
# Test your configuration with the benchmark suite
go test -bench=BenchmarkQueueSaturation -benchtime=30s \
  -args -workers=$YOUR_WORKERS -queue=$YOUR_QUEUE_SIZE

# Expected result: Drop rate should match your predictions
```

### Load Testing Validation
```bash
# Generate sustained load at your expected rate
go test -bench=BenchmarkEmitScaling -benchtime=60s \
  -args -rate=$YOUR_EVENTS_PER_SEC

# Monitor: Emission latency should stay under your alerts
```

## Capacity Planning Decision Tree

### Step 1: Determine Processing Time
```bash
# Profile your actual hooks
go test -bench=BenchmarkWorkerEfficiency -cpuprofile=hooks.prof
go tool pprof -top hooks.prof

# Look for total processing time per event
```

### Step 2: Calculate Base Configuration  
```bash
# Use the formulas above with your measured processing time
workers=$(ceiling($events_per_sec * $processing_time_sec))
queue=$(($workers * $burst_factor))
```

### Step 3: Validate with Benchmarks
```bash
# Test calculated configuration
go test -bench=. -benchtime=30s \
  -args -workers=$workers -queue=$queue
```

### Step 4: Adjust Based on Results
- **Drop rate too high**: Increase workers first, then queue size
- **Drop rate acceptable but high latency**: Check for contention, optimize hooks
- **Low utilization**: Reduce workers to save resources
- **Bursty drops**: Increase queue size (burst tolerance)

## Production Deployment Checklist

### Pre-Deployment
- [ ] Measured actual hook processing times
- [ ] Calculated worker/queue configuration using formulas
- [ ] Validated configuration with benchmark suite
- [ ] Set up monitoring for drop rate, latency, queue depth
- [ ] Configured alerts at appropriate thresholds
- [ ] Load tested with realistic event patterns

### Post-Deployment  
- [ ] Monitor drop rates during first week
- [ ] Verify emission latency stays within bounds
- [ ] Check worker efficiency metrics
- [ ] Adjust configuration based on actual traffic patterns
- [ ] Document any configuration changes with reasoning

### Ongoing Operations
- [ ] Weekly review of drop rate trends
- [ ] Monthly capacity planning review  
- [ ] Benchmark regression testing with new releases
- [ ] Configuration updates for traffic growth

## Troubleshooting Configuration Issues

### Problem: Higher than expected drop rates
```bash
# Diagnosis
current_capacity = workers * 1000 / avg_processing_time_ms
utilization = actual_event_rate / current_capacity

# Solution based on utilization
if utilization > 0.9:
    # Increase workers  
    new_workers = ceiling(workers * utilization * 1.1)
elif utilization < 0.7:
    # Check for other bottlenecks (GC, contention, etc.)
    # Profile the application
```

### Problem: High emission latency
```bash
# Check mutex contention
go test -bench=BenchmarkEmitScaling -mutexprofile=mutex.prof
go tool pprof -top mutex.prof

# Check GC pressure
GODEBUG=gctrace=1 your_application

# Solutions:
# - Optimize hook processing
# - Reduce hook count  
# - Check for resource contention
```

### Problem: Inconsistent performance
```bash
# Check for bursty traffic patterns
# Monitor queue depth over time

# Solutions:
# - Increase burst tolerance (queue size)
# - Implement rate limiting at source
# - Add load smoothing
```

## Advanced Configuration Patterns

### Pattern 1: Event Type Partitioning
For applications with different event types that have different processing characteristics:

```yaml
# Configure separate service instances for different event types
fast_events:
  workers: 5
  queue: 10
  processing_time: 1ms
  
slow_events:  
  workers: 20
  queue: 60
  processing_time: 25ms
```

### Pattern 2: Adaptive Configuration
For applications with variable load patterns:

```bash
# Monitor and adjust configuration based on metrics
if drop_rate > 5% for 5_minutes:
    workers = workers * 1.2
    queue = queue * 1.2
    restart_service()
```

### Pattern 3: Circuit Breaker Integration
For applications with external dependencies:

```yaml
circuit_breaker:
  enabled: true
  failure_threshold: 10%      # When API calls fail
  recovery_threshold: 1%      # When to try again
  
configuration:
  workers: 15                 # Higher worker count
  queue: 45                   # Higher queue for retries
  timeout_ms: 5000           # API call timeout
```

## Performance Optimization Tips

### Hook-Level Optimizations
1. **Minimize allocations** in hook functions
2. **Use object pooling** for large temporary objects
3. **Batch operations** where possible (database writes, API calls)
4. **Implement timeouts** for all external calls
5. **Handle errors gracefully** without panics

### System-Level Optimizations
1. **Right-size worker pools** using the formulas above
2. **Monitor GC pressure** and tune GOGC if needed
3. **Use CPU profiling** to identify hot paths
4. **Consider async patterns** for slow operations
5. **Implement backpressure** at event sources if needed

---

**Generated**: September 12, 2025  
**Based on**: Benchmark suite validation and RAINMAN-approved recommendations  
**Status**: Production-ready configuration guidance
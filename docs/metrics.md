# Metrics

The hookz library provides comprehensive metrics for monitoring service health and performance.

## Metrics Structure

```go
type Metrics struct {
    // Primary Queue Metrics
    QueueDepth    int64 // Current tasks in primary queue
    QueueCapacity int64 // Primary queue capacity (static)

    // Throughput Counters
    TasksProcessed int64 // Successfully completed hooks
    TasksRejected  int64 // Tasks rejected due to full queue
    TasksFailed    int64 // Hook executions that failed or panicked
    TasksExpired   int64 // Tasks discarded due to context cancellation

    // Registration Metrics
    RegisteredHooks int64 // Current registered hooks

    // Overflow Metrics (Phase 2 - currently set to 0)
    OverflowDepth    int64 // Current overflow queue depth
    OverflowCapacity int64 // Overflow queue capacity
    OverflowDrained  int64 // Tasks successfully moved from overflow to primary
}
```

## Accessing Metrics

```go
service := hookz.New[MyType]()
defer service.Close()

// Get current metrics
metrics := service.Metrics()

fmt.Printf("Queue depth: %d/%d\n", metrics.QueueDepth, metrics.QueueCapacity)
fmt.Printf("Tasks processed: %d\n", metrics.TasksProcessed)
fmt.Printf("Registered hooks: %d\n", metrics.RegisteredHooks)
```

## Thread Safety

All metrics are thread-safe and can be accessed concurrently:

- **Counter fields** (TasksProcessed, TasksRejected, etc.) use atomic operations
- **RegisteredHooks** uses mutex protection for consistency
- **QueueDepth** is updated atomically as tasks flow through the system
- **Capacity fields** are static and don't require synchronization

## Performance Impact

Metrics collection has minimal performance overhead:

- ~98ns per operation overhead (atomic operations)
- ~23ns to access metrics (no allocations)
- No additional goroutines or background processing

## Metrics Meaning

### Queue Metrics

- **QueueDepth**: Number of tasks currently waiting in the queue for worker processing
- **QueueCapacity**: Maximum number of tasks the queue can hold

### Throughput Metrics

- **TasksProcessed**: Hooks that executed successfully without errors
- **TasksRejected**: Tasks rejected because the queue was full
- **TasksFailed**: Hooks that returned errors or panicked during execution
- **TasksExpired**: Tasks discarded due to context cancellation

### Registration Metrics

- **RegisteredHooks**: Current number of hooks registered across all events

## Limitations

### Counter Overflow

64-bit counters may overflow after 2^63 operations. At 1 million operations per second, this occurs after ~292 billion years. Production monitoring should track rates rather than absolute values.

### Eventual Consistency

During high load, brief metric inconsistencies are acceptable. Metrics converge to accurate values when load stabilizes. Trend accuracy is prioritized over point-in-time precision.

### Memory Usage

Metrics use a fixed amount of memory (96 bytes per service instance) and do not grow with usage.


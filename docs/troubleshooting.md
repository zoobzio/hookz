# Troubleshooting

hookz has three real problems users encounter. Everything else is standard Go debugging.

## The Three Problems

1. [Events Not Processing](#events-not-processing)
2. [Memory Growing](#memory-growing)  
3. [Handlers Failing Silently](#handlers-failing-silently)

## Events Not Processing

### Symptoms
- `Emit()` succeeds but handlers never run
- No error messages
- Application seems to work but events ignored

### Root Causes & Solutions

#### Queue Full
```go
// Diagnosis
if err := hooks.Emit(ctx, "event", data); err == hookz.ErrQueueFull {
    log.Printf("Queue full - system overloaded")
}

// Solution: Increase workers
hooks := hookz.New[Data](hookz.WithWorkers(50))

// Solution: Implement backpressure
if err := hooks.Emit(ctx, "event", data); err == hookz.ErrQueueFull {
    time.Sleep(10 * time.Millisecond)
    return hooks.Emit(ctx, "event", data) // Retry
}
```

#### Handler Panics
```go
// Diagnosis: Add panic recovery
hooks.Hook("event", func(ctx context.Context, data Data) error {
    defer func() {
        if r := recover(); r != nil {
            log.Printf("Handler panicked: %v", r)
        }
    }()
    return processData(data)
})
```

#### Missing Close()
```go
// Problem: Process exits before async handlers finish
func main() {
    hooks := hookz.New[Event]()
    defer hooks.Close() // REQUIRED - waits for completion
    
    // Application logic...
}
```

## Memory Growing

### Symptoms
- Memory usage grows steadily
- Eventually runs out of memory

### Root Causes & Solutions

#### Unbounded Emission
```go
// Solution: Handle queue full gracefully
if err := hooks.Emit(ctx, "event", data); err == hookz.ErrQueueFull {
    metrics.Counter("events_dropped").Inc()
    return nil // Drop event instead of unlimited retry
}
```

#### Large Event Data
```go
// BAD: Large objects retained in queue
type LargeEvent struct {
    Data []byte // 1MB of data sits in memory
}

// GOOD: Queue references, fetch in handler
type EventRef struct {
    ID   string
    Path string
}

hooks.Hook("event", func(ctx context.Context, ref EventRef) error {
    data := storage.Get(ref.Path)        // Fetch when needed
    defer storage.Release(ref.Path)      // Clean up immediately
    return processData(data)
})
```

#### Hook Accumulation
```go
// Diagnosis
hook, err := service.Hook("event", handler)
if err == hookz.ErrTooManyHooks {
    log.Printf("Hit resource limits - clean up unused hooks")
}

// Solution: Clean up unused hooks
count := service.Clear("unused.event")
log.Printf("Removed %d unused hooks", count)
```

## Handlers Failing Silently

### Symptoms  
- Handlers fail but emit operation succeeds
- No visible errors from business logic perspective

### Root Cause
Hook errors don't propagate to `Emit()` caller:

```go
// This succeeds even if handler fails
err := hooks.Emit(ctx, "payment", payment)
assert.NoError(t, err) // Always passes
```

### Solutions

#### Log Critical Failures
```go
// BAD: Payment failures invisible
hooks.Hook("payment.processed", func(ctx context.Context, p Payment) error {
    return chargeCard(p) // Failure invisible!
})

// GOOD: Make failures visible
hooks.Hook("payment.processed", func(ctx context.Context, p Payment) error {
    if err := chargeCard(p); err != nil {
        auditLog.Security("payment_failure", p.ID, err)
        alertOps("Payment failed", err) // Page someone
        return err
    }
    return nil
})
```

#### Test Handlers Directly
```go
// Test the handler function directly
func TestPaymentHandler(t *testing.T) {
    handler := NewPaymentHandler(mockCardService)
    
    err := handler.ProcessPayment(context.Background(), payment)
    assert.NoError(t, err)
    assert.True(t, mockCardService.WasCharged())
}
```

## Common Error Messages

### "too many hooks registered"
- **Cause**: More than 100 hooks per event or 10,000 total
- **Solution**: `service.Clear("unused.event")` or redesign events

### "hook service is closed"  
- **Cause**: Operation after `Close()`
- **Solution**: Usually expected during shutdown, handle gracefully

### "worker queue is full"
- **Cause**: Events queued faster than processed
- **Solution**: Increase workers or implement backpressure

### Context deadline exceeded
- **Cause**: Handler took longer than timeout
- **Solution**: Respect context cancellation in handlers

```go
hooks.Hook("slow", func(ctx context.Context, data Data) error {
    for i := 0; i < 1000; i++ {
        select {
        case <-ctx.Done():
            return ctx.Err() // Respect cancellation
        default:
            processChunk(data, i)
        }
    }
    return nil
})
```
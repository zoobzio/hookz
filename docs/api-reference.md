# API Reference

Complete reference for hookz types, methods, and errors.

## Constructor

### New[T any](opts ...Option[T]) *Manager[T]

Creates hook manager with optional configuration.

```go
hooks := hookz.New[User]()                    // Default config
hooks := hookz.New[User](hookz.WithWorkers(20)) // Custom workers
```

**Default Configuration:**
- Workers: 10 goroutines
- No timeout limit
- Resource limits: 100 hooks/event, 10,000 total

## Core Methods

### Hook(event string, callback func(context.Context, T) error) (Hook, error)

Registers event handler.

```go
hook, err := hooks.Hook("user.created", func(ctx context.Context, user User) error {
    return sendEmail(ctx, user.Email)
})
if err != nil {
    return fmt.Errorf("hook registration failed: %w", err)
}
defer hook.Unhook()
```

**Errors:**
- `ErrTooManyHooks` - Resource limits exceeded
- `ErrServiceClosed` - System shutting down

### Emit(ctx context.Context, event string, data T) error

Triggers all handlers for event (async).

```go
user := User{ID: "123", Email: "user@example.com"}
if err := hooks.Emit(ctx, "user.created", user); err != nil {
    log.Printf("Event emission failed: %v", err)
    // Continue - emission failure shouldn't break business logic
}
```

**Behavior:**
- Returns immediately after queueing
- Handler errors don't affect this call
- Context passed to all handlers

**Errors:**
- `ErrQueueFull` - Worker pool saturated
- `ErrServiceClosed` - System shutting down

### Close() error

Graceful shutdown - waits for running handlers to complete.

```go
hooks := hookz.New[Event]()
defer hooks.Close() // Ensures clean shutdown
```

**Shutdown Process:**
1. Reject new operations
2. Drain worker queues
3. Wait for handler completion
4. Release resources

## Hook Management

### Hook.Unhook() error

Removes individual hook.

```go
if err := hook.Unhook(); err != nil {
    log.Printf("Unhook failed: %v", err)
    // Usually not critical
}
```

### Clear(event string) int

Removes all hooks for specific event.

```go
count := hooks.Clear("user.created")
log.Printf("Removed %d hooks", count)
```

### ClearAll() int

Removes all hooks.

```go
count := hooks.ClearAll()
log.Printf("Removed %d total hooks", count)
```

## Configuration Options

### WithWorkers(n int) Option[T]

Sets worker goroutine count.

```go
hooks := hookz.New[Event](hookz.WithWorkers(runtime.NumCPU() * 2))
```

**Guidelines:**
- Default (10): Good for most applications  
- High throughput: 2x CPU count
- I/O heavy hooks: Higher counts

### WithTimeout(d time.Duration) Option[T]

Sets global timeout for all handlers.

```go
hooks := hookz.New[Event](hookz.WithTimeout(30 * time.Second))
```

**Behavior:**
- Context cancellation after timeout
- Handlers should respect context cancellation
- Timeout applies per handler, not per event

## Error Types

### ErrTooManyHooks

Resource limits exceeded during registration.

```go
if err == hookz.ErrTooManyHooks {
    // Clean up unused hooks or increase limits
    hooks.Clear("old.event")
}
```

**Limits:**
- 100 hooks per event type
- 10,000 total hooks

### ErrServiceClosed

Operation attempted after Close().

```go
if err == hookz.ErrServiceClosed {
    log.Info("Hook system shutting down")
    return nil // Usually not critical during shutdown
}
```

### ErrQueueFull

Worker queues saturated.

```go
if err == hookz.ErrQueueFull {
    metrics.Inc("hookz.backpressure")
    // Consider: retry with backoff, increase workers, or drop event
}
```

**Solutions:**
- Increase workers with `WithWorkers()`
- Add timeout with `WithTimeout()`
- Implement backoff/retry logic

### ErrAlreadyUnhooked

Hook already removed.

```go
if err == hookz.ErrAlreadyUnhooked {
    // Hook is gone - mission accomplished
    return nil
}
```

## Type Safety

Generic type parameter ensures compile-time safety:

```go
hooks := hookz.New[User]()

// ✓ Correct
hooks.Hook("event", func(ctx context.Context, user User) error { return nil })
hooks.Emit(ctx, "event", User{ID: "123"})

// ✗ Compile error
hooks.Hook("event", func(ctx context.Context, order Order) error { return nil })
hooks.Emit(ctx, "event", Order{ID: "456"})
```

## Thread Safety

All operations are thread-safe:
- Concurrent Hook() registrations
- Concurrent Emit() calls
- Concurrent Unhook() operations
- Mixed operations across goroutines

Performance scales with Go's goroutine scheduler.
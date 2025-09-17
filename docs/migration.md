# Migration Guide

Migrate from common async patterns to hookz.

## From Manual Callbacks

**Before:**
```go
func processUser(user User) {
    go sendEmail(user.Email)
    go updateMetrics(user)
}
```

**After:**
```go
hooks := hookz.New[User]()
hooks.Hook("user.created", func(ctx context.Context, user User) error {
    return sendEmail(ctx, user.Email)
})
hooks.Hook("user.created", func(ctx context.Context, user User) error {
    return updateMetrics(ctx, user)
})

// Usage
hooks.Emit(context.Background(), "user.created", user)
```

## From Goroutine Management

**Before:**
```go
func notifyUser(user User) {
    go func() {
        select {
        case userCh <- user:
        case <-time.After(5 * time.Second):
            log.Error("notification timeout")
        }
    }()
}
```

**After:**
```go
hooks := hookz.New[User](hookz.WithTimeout(5 * time.Second))
hooks.Hook("user.notify", func(ctx context.Context, user User) error {
    return sendNotification(ctx, user)
})

hooks.Emit(context.Background(), "user.notify", user)
```

## From Observer Pattern

**Before:**
```go
type Observer interface {
    OnUserEvent(user User)
}

type UserService struct {
    observers []Observer
}

func (s *UserService) NotifyObservers(user User) {
    for _, obs := range s.observers {
        go obs.OnUserEvent(user) // Manual goroutines
    }
}
```

**After:**
```go
hooks := hookz.New[User]()

// Replace observers with hooks
hooks.Hook("user.created", func(ctx context.Context, user User) error {
    return sendWelcomeEmail(ctx, user)
})
hooks.Hook("user.created", func(ctx context.Context, user User) error {
    return logUserAudit(ctx, user)
})

// Replace NotifyObservers with Emit
hooks.Emit(context.Background(), "user.created", user)
```

## Common Gotchas

### Error Handling Changes

**Before:**
```go
if err := sendEmail(user.Email); err != nil {
    return fmt.Errorf("email failed: %w", err)
}
```

**After:**
```go
// Hook errors don't propagate to Emit() caller
hooks.Hook("user.created", func(ctx context.Context, user User) error {
    if err := sendEmail(ctx, user.Email); err != nil {
        // Log critical failures in the hook
        log.Printf("Email failed for user %s: %v", user.ID, err)
        return err // Logged but doesn't affect emit
    }
    return nil
})

// Emit always succeeds if queue isn't full
err := hooks.Emit(ctx, "user.created", user) // Different error model
```

### Context Propagation

**Before:**
```go
func processUser(ctx context.Context, user User) {
    go sendEmail(user.Email) // Context lost
}
```

**After:**
```go
hooks.Hook("user.created", func(ctx context.Context, user User) error {
    // Context automatically propagated
    return sendEmail(ctx, user.Email)
})

hooks.Emit(ctx, "user.created", user) // Context passed to all hooks
```

### Resource Cleanup

**Before:**
```go
// Manual goroutine lifecycle management
var wg sync.WaitGroup
wg.Add(3)

go func() {
    defer wg.Done()
    processTask1()
}()

wg.Wait() // Wait for completion
```

**After:**
```go
hooks := hookz.New[Task]()
defer hooks.Close() // Automatic cleanup and waiting

hooks.Hook("task.process", processTask1Handler)
hooks.Emit(ctx, "task.process", task)
// hooks.Close() waits for completion
```
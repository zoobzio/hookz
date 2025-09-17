# Common Patterns

Essential patterns for using hookz effectively.

## Service Integration

### Basic Service Pattern

```go
type UserService struct {
    hooks hookz.Hooks[User] // Private - only service can emit
    users map[string]User
}

// Expose consumer interface only
func (s *UserService) Events() hookz.HookRegistry[User] {
    return &s.hooks
}

func (s *UserService) CreateUser(ctx context.Context, user User) error {
    // Business logic first
    if user.ID == "" {
        return fmt.Errorf("user ID required")
    }
    s.users[user.ID] = user
    
    // Then notify (non-blocking)
    if err := s.hooks.Emit(ctx, "user.created", user); err != nil {
        log.Printf("Event emission failed: %v", err)
        // Continue - don't fail business logic
    }
    return nil
}
```

### Multiple Handlers

Multiple services react to the same event:

```go
orderService.Events().Hook("order.created", inventoryHandler)
orderService.Events().Hook("order.created", emailHandler)
orderService.Events().Hook("order.created", analyticsHandler)
```

## Error Handling

### Three Error Scenarios

#### 1. Registration Failures

```go
hook, err := service.Hook("event", handler)
switch err {
case nil:
    // Success
case hookz.ErrTooManyHooks:
    // Hit limits - clean up unused hooks
    service.Clear("unused.event")
case hookz.ErrServiceClosed:
    // Service shutting down - handle gracefully
}
```

#### 2. Emission Failures

```go
if err := hooks.Emit(ctx, "user.created", user); err != nil {
    switch err {
    case hookz.ErrQueueFull:
        // System overloaded - implement backpressure
        metrics.Counter("events_dropped").Inc()
    case hookz.ErrServiceClosed:
        // Shutting down - handle gracefully
    }
    // Continue - don't fail business logic
}
```

#### 3. Handler Failures (Silent)

Hook handler errors don't affect the emitter:

```go
// BAD: Critical failures invisible
hooks.Hook("payment.processed", func(ctx context.Context, p Payment) error {
    return chargeCard(p) // Could fail silently!
})

// GOOD: Log critical failures
hooks.Hook("payment.processed", func(ctx context.Context, p Payment) error {
    if err := chargeCard(p); err != nil {
        auditLog.Security("payment_failure", p.ID, err)
        alertOps("Payment failed", err) // Make visible
        return err
    }
    return nil
})
```

## Async Processing

### Working Example

User registration with side effects:

```go
type User struct {
    ID    string
    Email string
    Name  string
}

func main() {
    hooks := hookz.New[User]()
    defer hooks.Close()
    
    // Register handlers
    hooks.Hook("user.created", sendWelcomeEmail)
    hooks.Hook("user.created", updateAnalytics)
    hooks.Hook("user.created", scheduleOnboarding)
    
    // Create user - handlers run async
    user := User{ID: "u123", Email: "user@example.com", Name: "Alice"}
    hooks.Emit(context.Background(), "user.created", user)
    
    fmt.Println("User created, side effects processing...")
}
```

## Context Usage

### Timeout Handling

```go
// Global timeout for all handlers
hooks := hookz.New[Event](hookz.WithTimeout(5*time.Second))

// Per-emission timeout
ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
defer cancel()
hooks.Emit(ctx, "slow.operation", data)

// Handler respects context
hooks.Hook("slow.operation", func(ctx context.Context, data Data) error {
    select {
    case <-ctx.Done():
        return ctx.Err()
    case result := <-processData(data):
        return result.Error
    }
})
```

### Cancellation

```go
ctx, cancel := context.WithCancel(context.Background())

// Handler checks cancellation
hooks.Hook("long.process", func(ctx context.Context, data Data) error {
    for i := 0; i < 100; i++ {
        select {
        case <-ctx.Done():
            return ctx.Err() // Stop processing
        default:
            processChunk(data, i)
        }
    }
    return nil
})

// Cancel processing
cancel() // All handlers receive cancellation
```
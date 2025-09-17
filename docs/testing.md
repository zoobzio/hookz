# Testing hookz

Essential patterns for testing async hook systems reliably.

## Core Testing Principle

**Always call `Close()` to synchronize async operations in tests.**

hookz provides deterministic testing through graceful shutdown - `Close()` blocks until all queued operations complete.

## Basic Hook Testing

### Simple Hook Verification

```go
func TestHookExecution(t *testing.T) {
    hooks := hookz.New[string]()
    var received string
    
    // Register hook
    hook, err := hooks.Hook("test.event", func(ctx context.Context, data string) error {
        received = data
        return nil
    })
    require.NoError(t, err)
    
    // Emit event
    err = hooks.Emit(context.Background(), "test.event", "hello")
    require.NoError(t, err)
    
    // Wait for completion - CRITICAL for deterministic tests
    hooks.Close()
    
    // Verify result
    assert.Equal(t, "hello", received)
}
```

### Multiple Hook Testing

```go
func TestMultipleHooks(t *testing.T) {
    hooks := hookz.New[int]()
    results := make([]int, 0, 3)
    var mu sync.Mutex
    
    // Register multiple hooks
    for i := 0; i < 3; i++ {
        value := i // Capture loop variable
        hooks.Hook("number.event", func(ctx context.Context, data int) error {
            mu.Lock()
            results = append(results, data+value)
            mu.Unlock()
            return nil
        })
    }
    
    // Emit event
    hooks.Emit(context.Background(), "number.event", 10)
    
    // Synchronize
    hooks.Close()
    
    // Verify all hooks executed
    assert.Len(t, results, 3)
    assert.Contains(t, results, 10) // 10+0
    assert.Contains(t, results, 11) // 10+1  
    assert.Contains(t, results, 12) // 10+2
}
```

## Async Testing with Eventually

When you can't use `Close()` (testing long-running services):

```go
func TestEventualConsistency(t *testing.T) {
    service := NewUserService()
    defer service.Close()
    
    var emailSent bool
    service.Events().Hook("user.created", func(ctx context.Context, user User) error {
        emailSent = true
        return nil
    })
    
    service.CreateUser(context.Background(), User{ID: "123"})
    
    // Wait for async processing
    assert.Eventually(t, func() bool {
        return emailSent
    }, time.Second, 10*time.Millisecond)
}
```

## Error Testing

### Hook Error Collection

```go
func TestHookError(t *testing.T) {
    hooks := hookz.New[string]()
    var hookErr error
    done := make(chan bool, 1)
    
    // Hook captures its own error
    hooks.Hook("test.event", func(ctx context.Context, data string) error {
        err := errors.New("processing failed")
        hookErr = err    // Capture for verification
        done <- true     // Signal completion
        return err
    })
    
    hooks.Emit(context.Background(), "test.event", "data")
    
    // Wait for completion
    select {
    case <-done:
        // Hook completed
    case <-time.After(time.Second):
        t.Fatal("Hook timeout")
    }
    
    hooks.Close()
    assert.Error(t, hookErr)
}
```

### Emission Error Testing

```go
func TestEmissionErrors(t *testing.T) {
    hooks := hookz.New[string]()
    
    // Test after close
    hooks.Close()
    err := hooks.Emit(context.Background(), "event", "data")
    assert.Equal(t, hookz.ErrServiceClosed, err)
    
    // Test registration limits
    hooks = hookz.New[string]()
    defer hooks.Close()
    
    // Register 100 hooks (limit per event)
    for i := 0; i < 100; i++ {
        _, err := hooks.Hook("test", func(ctx context.Context, s string) error {
            return nil
        })
        require.NoError(t, err)
    }
    
    // 101st should fail
    _, err = hooks.Hook("test", func(ctx context.Context, s string) error {
        return nil
    })
    assert.Equal(t, hookz.ErrTooManyHooks, err)
}
```

## Test Cleanup

### Proper Resource Management

```go
func TestWithCleanup(t *testing.T) {
    hooks := hookz.New[string]()
    defer hooks.Close() // Always clean up
    
    hook, err := hooks.Hook("event", func(ctx context.Context, s string) error {
        return nil
    })
    require.NoError(t, err)
    defer hook.Unhook() // Clean up hook if test fails early
    
    // Test logic...
}
```

### Table-Driven Tests

```go
func TestEventScenarios(t *testing.T) {
    tests := []struct {
        name     string
        event    string
        data     string
        wantErr  bool
    }{
        {"valid event", "user.created", "valid", false},
        {"empty event", "", "data", false}, // hookz allows empty event names
        {"empty data", "event", "", false}, // hookz allows empty data
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            hooks := hookz.New[string]()
            defer hooks.Close()
            
            var received string
            hooks.Hook(tt.event, func(ctx context.Context, data string) error {
                received = data
                return nil
            })
            
            err := hooks.Emit(context.Background(), tt.event, tt.data)
            if tt.wantErr {
                assert.Error(t, err)
            } else {
                assert.NoError(t, err)
            }
            
            hooks.Close() // Sync before assertion
            assert.Equal(t, tt.data, received)
        })
    }
}
```
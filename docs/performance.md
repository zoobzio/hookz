# Performance Configuration

Simple guidelines for optimizing hookz performance.

## Worker Count

### Default Configuration
```go
hooks := hookz.New[Event]() // 10 workers - good for most applications
```

### High Throughput
```go
hooks := hookz.New[Event](hookz.WithWorkers(runtime.NumCPU() * 2))
```

**Guidelines:**
- **CPU-bound hooks**: `runtime.NumCPU()` workers  
- **I/O-bound hooks**: `runtime.NumCPU() * 2-4` workers
- **Mixed workload**: Start with `runtime.NumCPU() * 2`, tune based on monitoring

## Timeout Configuration

### Global Timeout
```go
hooks := hookz.New[Event](hookz.WithTimeout(30 * time.Second))
```

### Per-Emission Timeout
```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
hooks.Emit(ctx, "event", data)
```

**Recommendations:**
- **Critical paths**: 1-5 seconds
- **Background processing**: 30+ seconds
- **Long-running tasks**: No timeout (nil)

## When to Optimize

Only optimize when experiencing:

### Queue Full Errors
```go
if err := hooks.Emit(ctx, "event", data); err == hookz.ErrQueueFull {
    // Solution: Increase workers
    hooks := hookz.New[Event](hookz.WithWorkers(50))
}
```

### Hook Execution > 100ms Consistently
```go
hooks.Hook("slow.process", func(ctx context.Context, data Data) error {
    start := time.Now()
    defer func() {
        duration := time.Since(start)
        if duration > 100*time.Millisecond {
            log.Printf("Slow hook: %v", duration)
        }
    }()
    return processData(data)
})
```

### Memory Usage Growing Unbounded
```go
// Monitor with pprof
import _ "net/http/pprof"
// Visit http://localhost:6060/debug/pprof/heap
```

**Solutions:**
1. Increase workers: More parallel processing
2. Add timeouts: Prevent runaway handlers  
3. Reduce event data size: Queue smaller objects
4. Clean up unused hooks: `hooks.Clear("unused.event")`
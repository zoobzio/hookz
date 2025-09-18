# hookz

[![CI Status](https://github.com/zoobzio/hookz/workflows/CI/badge.svg)](https://github.com/zoobzio/hookz/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/zoobzio/hookz/graph/badge.svg?branch=main)](https://codecov.io/gh/zoobzio/hookz)
[![Go Report Card](https://goreportcard.com/badge/github.com/zoobzio/hookz)](https://goreportcard.com/report/github.com/zoobzio/hookz)
[![CodeQL](https://github.com/zoobzio/hookz/workflows/CodeQL/badge.svg)](https://github.com/zoobzio/hookz/security/code-scanning)
[![Go Reference](https://pkg.go.dev/badge/github.com/zoobzio/hookz.svg)](https://pkg.go.dev/github.com/zoobzio/hookz)
[![License](https://img.shields.io/github/license/zoobzio/hookz)](LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/zoobzio/hookz)](go.mod)
[![Release](https://img.shields.io/github/v/release/zoobzio/hookz)](https://github.com/zoobzio/hookz/releases)

Type-safe event hooks for Go. Notify interested parties without coupling.

## Complete Example - Copy and Run

```go
package main

import (
    "context"
    "fmt"
    "github.com/zoobzio/hookz"
)

func main() {
    hooks := hookz.New[string]()
    defer hooks.Close()
    
    hooks.Hook("greeting", func(ctx context.Context, name string) error {
        fmt.Printf("Hello, %s!\n", name)
        return nil
    })
    
    hooks.Emit(context.Background(), "greeting", "World")
    hooks.Close() // Wait for async processing
}
```

## Key Concepts

- **Type-safe**: Generic interfaces prevent runtime errors
- **Async execution**: `Emit()` returns immediately, hooks run in background
- **No failures propagate**: Hook errors don't break your business logic
- **Clean shutdown**: `Close()` waits for completion
- **Built-in metrics**: Monitor queue depth, throughput, and error rates

## Installation

```bash
go get github.com/zoobzio/hookz
```

## Service Integration

Add to existing service:

```go
type OrderService struct {
    hooks hookz.Hooks[Order] // Private - only service emits
    // existing fields...
}

// Expose consumer interface only  
func (s *OrderService) Events() hookz.HookRegistry[Order] {
    return &s.hooks
}

func (s *OrderService) CreateOrder(ctx context.Context, order Order) error {
    // Business logic first
    // ...
    
    // Then notify (non-blocking)
    s.hooks.Emit(ctx, "order.created", order)
    return nil
}

func (s *OrderService) GetMetrics() hookz.Metrics {
    return s.hooks.Metrics() // Monitor queue, throughput, errors
}
```

Multiple handlers for the same event:

```go
orderService.Events().Hook("order.created", inventoryHandler)
orderService.Events().Hook("order.created", emailHandler) 
orderService.Events().Hook("order.created", analyticsHandler)
```

## Error Handling

```go
hook, err := service.Hook("critical.event", handler)
if err == hookz.ErrTooManyHooks {
    // Hit resource limits - clean up unused hooks
} else if err == hookz.ErrServiceClosed {
    // Service shutting down
}

// Hook failures don't affect emitter
hooks.Emit(ctx, "event", data) // Always succeeds if queue not full
```

## Documentation

- **Quick Start** - [docs/quickstart.md](docs/quickstart.md) - Get working in 5 minutes
- **Common Patterns** - [docs/common-patterns.md](docs/common-patterns.md) - Essential usage patterns  
- **API Reference** - [docs/api-reference.md](docs/api-reference.md) - Complete method documentation
- **Metrics** - [docs/metrics.md](docs/metrics.md) - Monitoring and observability
- **Testing** - [docs/testing.md](docs/testing.md) - Testing async hook systems
- **Performance** - [docs/performance.md](docs/performance.md) - Configuration tuning  
- **Migration** - [docs/migration.md](docs/migration.md) - Migrate from other patterns
- **Troubleshooting** - [docs/troubleshooting.md](docs/troubleshooting.md) - When things go wrong
- **Examples** - [examples/](examples/) - Progressive complexity examples

## Requirements

- Go 1.23 or later
- Zero dependencies
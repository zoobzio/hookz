# Quick Start

Get working with hookz in 5 minutes.

## Installation

```bash
go get github.com/zoobzio/hookz
```

## Hello World

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
    
    hooks.Hook("greet", func(ctx context.Context, name string) error {
        fmt.Printf("Hello, %s!\n", name)
        return nil
    })
    
    hooks.Emit(context.Background(), "greet", "World")
    hooks.Close() // Wait for completion
}
// Output: Hello, World!
```

## Real-World Example

User registration with multiple side effects:

```go
type User struct {
    ID    string
    Email string
    Name  string
}

func main() {
    hooks := hookz.New[User]()
    defer hooks.Close()
    
    // Register multiple handlers
    hooks.Hook("user.created", sendWelcomeEmail)
    hooks.Hook("user.created", updateAnalytics)
    hooks.Hook("user.created", scheduleOnboarding)
    
    // Create user - handlers run async
    user := User{ID: "u123", Email: "user@example.com", Name: "Alice"}
    hooks.Emit(context.Background(), "user.created", user)
    
    // Continue with other work...
    fmt.Println("User created, side effects processing...")
}
```

## Next Steps

- **Common Patterns**: [common-patterns.md](common-patterns.md) - Event handling patterns
- **API Reference**: [api-reference.md](api-reference.md) - Complete method documentation  
- **Testing**: [testing.md](testing.md) - Testing hook-based code
- **Examples**: [../examples/](../examples/) - Progressive complexity examples
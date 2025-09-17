// Package hookz provides a type-safe, high-performance event hook system
// with async execution, global timeouts, and graceful shutdown.
//
// This is the v5 implementation with simplified API:
//   - Consumer API reduced to 2 methods: Hook and Unhook
//   - Global timeout configuration (not per-hook)
//   - Segregated interfaces for different use cases
//   - Resource limits to prevent memory exhaustion
//   - Race condition protection and panic recovery
//
// Basic Usage:
//
//	// Create a hookable service for user events
//	hooks := hookz.New[User]()
//
//	// Register hooks for events
//	hook, err := hooks.Hook("user.created", func(ctx context.Context, user User) error {
//		return sendWelcomeEmail(ctx, user)
//	})
//	if err != nil {
//		return err
//	}
//
//	// Emit events to trigger hooks
//	if err := hooks.Emit(ctx, "user.created", newUser); err != nil {
//		return err
//	}
//
//	// Clean shutdown
//	defer hooks.Close()
//
// Advanced Usage:
//
//	// Create with global timeout configuration
//	hooks := hookz.New[Order](hookz.WithWorkers(10), hookz.WithTimeout(5*time.Second)) // 5s timeout for all hooks
//
//	// Create with timeout configuration
//	hooks := hookz.New[Event](hookz.WithWorkers(20), hookz.WithTimeout(5*time.Second))
//
//	// Fire-and-forget emission
//	if err := hooks.Emit(context.Background(), "order.created", order); err != nil {
//		// Handle queue full error
//	}
//
// Service Integration:
//
//	type OrderService struct {
//		hooks *hookz.Hooks[Order] // Private implementation
//	}
//
//	// Expose hooks directly
//	func (s *OrderService) Events() *hookz.Hooks[Order] {
//		return &s.hooks
//	}
//
// Resource Management:
//
// The system enforces limits to prevent memory exhaustion:
//   - Maximum 100 hooks per event
//   - Maximum 10,000 total hooks
//   - Worker queue size limits async execution
//
// Hook functions should handle context cancellation for timeouts
// and service shutdown gracefully.
package hookz

// Key represents an event identifier used in hook registration and emission.
// This is a type alias for string that provides semantic meaning and encourages
// the use of package-level constants for better code organization and maintainability.
//
// Basic Usage with constants (recommended):
//
//	// Define event keys as package constants
//	const (
//		UserCreated Key = "user.created"
//		UserDeleted Key = "user.deleted"
//	)
//
//	hooks.Hook(UserCreated, func(ctx context.Context, user User) error {
//		return sendWelcomeEmail(ctx, user)
//	})
//
//	hooks.Emit(ctx, UserCreated, newUser)
type Key = string

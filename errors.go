package hookz

import "errors"

// Hook Management Errors
//
// These errors are returned when managing hook lifecycle
// (registration, unregistration, service state).

// ErrAlreadyUnhooked is returned when attempting to unhook a hook
// that has already been unhooked or was never valid.
var ErrAlreadyUnhooked = errors.New("hook already unhooked")

// ErrHookNotFound is returned when attempting to remove a hook
// that doesn't exist. This can occur in rare race conditions.
var ErrHookNotFound = errors.New("hook not found")

// Service Lifecycle Errors
//
// These errors are returned based on the service's lifecycle state.

// ErrServiceClosed is returned when attempting to use a service
// that has been closed via Close(). All operations on a closed
// service return this error.
var ErrServiceClosed = errors.New("service is closed")

// ErrAlreadyClosed is returned when calling Close() on a service
// that has already been closed. This prevents double-cleanup.
var ErrAlreadyClosed = errors.New("service already closed")

// Resource Limit Errors
//
// These errors are returned when resource limits are exceeded
// to prevent memory exhaustion attacks.

// ErrQueueFull is returned when the worker pool cannot accept
// more tasks. This indicates high load or slow hook execution.
//
// When this error occurs, the event emission is rejected and
// no hooks are executed for that emission.
var ErrQueueFull = errors.New("worker queue is full")

// ErrTooManyHooks is returned when attempting to register a hook
// would exceed either:
//   - maxHooksPerEvent (100 hooks for a single event)
//   - maxTotalHooks (10,000 total hooks across all events)
//
// These limits prevent memory exhaustion from unlimited hook registration.
var ErrTooManyHooks = errors.New("hook limit exceeded")

// Hook Execution Errors
//
// These errors are returned during hook execution.

// ErrHookPanicked is used internally to track hooks that panicked during execution.
// This error is not exposed to users but is used for metrics tracking.
var ErrHookPanicked = errors.New("hook panicked during execution")

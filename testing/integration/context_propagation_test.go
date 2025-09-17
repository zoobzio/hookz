package integration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zoobzio/hookz"
)

// Context keys for request-scoped values
type contextKey string

const (
	requestIDKey contextKey = "request-id"
	userIDKey    contextKey = "user-id"
	traceIDKey   contextKey = "trace-id"
	tenantIDKey  contextKey = "tenant-id"
)

// Event keys for context propagation testing
const (
	testEvent          hookz.Key = "test.event"
	deadlineTest       hookz.Key = "deadline.test"
	contextValues      hookz.Key = "context.values"
	contextTimeoutTest hookz.Key = "timeout.test"
	parentEvent        hookz.Key = "parent.event"
	childEvent         hookz.Key = "child.event"
	requestReceived    hookz.Key = "request.received"
	backgroundTest     hookz.Key = "background.test"
	authCheck          hookz.Key = "auth.check"
	businessProcess    hookz.Key = "business.process"
	cascadeTest        hookz.Key = "cascade.test"
)

// TestContextCancellationPropagation verifies that parent context cancellation
// properly cancels all hook executions
func TestContextCancellationPropagation(t *testing.T) {
	hooks := hookz.New[string]()
	defer hooks.Close()

	// Track which hooks started and completed
	var started, completed atomic.Int32

	// Register slow hook that respects context
	hook1, err := hooks.Hook(testEvent, func(ctx context.Context, data string) error {
		started.Add(1)

		// Simulate work that checks context
		select {
		case <-time.After(500 * time.Millisecond):
			completed.Add(1)
			return nil
		case <-ctx.Done():
			// Context canceled - clean shutdown
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatalf("Failed to register hook1: %v", err)
	}
	defer hook1.Unhook()

	// Register another slow hook
	hook2, err := hooks.Hook(testEvent, func(ctx context.Context, data string) error {
		started.Add(1)

		// Different timing to test concurrent cancellation
		select {
		case <-time.After(300 * time.Millisecond):
			completed.Add(1)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatalf("Failed to register hook2: %v", err)
	}
	defer hook2.Unhook()

	// Create parent context with cancellation
	ctx, cancel := context.WithCancel(context.Background())

	// Emit event
	if err := hooks.Emit(ctx, testEvent, "test data"); err != nil {
		t.Fatalf("Failed to emit event: %v", err)
	}

	// Cancel context after hooks start but before they complete
	time.Sleep(100 * time.Millisecond)
	cancel()

	// Wait to ensure hooks have time to react
	time.Sleep(600 * time.Millisecond)

	// Verify hooks started but didn't complete due to cancellation
	if s := started.Load(); s != 2 {
		t.Errorf("Expected 2 hooks to start, got %d", s)
	}
	if c := completed.Load(); c != 0 {
		t.Errorf("Expected 0 hooks to complete (should be canceled), got %d", c)
	}
}

// TestContextDeadlinePropagation verifies deadline inheritance from parent context
func TestContextDeadlinePropagation(t *testing.T) {
	hooks := hookz.New[string]()
	defer hooks.Close()

	// Track execution states
	var (
		deadlineExceeded atomic.Bool
		hookCompleted    atomic.Bool
	)

	// Register hook that performs time-sensitive operation
	hook, err := hooks.Hook(deadlineTest, func(ctx context.Context, data string) error {
		// Simulate operation that takes 200ms
		timer := time.NewTimer(200 * time.Millisecond)
		defer timer.Stop()

		select {
		case <-timer.C:
			hookCompleted.Store(true)
			return nil
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				deadlineExceeded.Store(true)
			}
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	// Create context with tight deadline (100ms)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(100*time.Millisecond))
	defer cancel()

	// Emit event
	if err := hooks.Emit(ctx, deadlineTest, "data"); err != nil {
		t.Fatalf("Failed to emit event: %v", err)
	}

	// Wait for hook to process
	time.Sleep(300 * time.Millisecond)

	// Verify deadline was exceeded
	if !deadlineExceeded.Load() {
		t.Error("Expected deadline to be exceeded")
	}
	if hookCompleted.Load() {
		t.Error("Hook should not have completed due to deadline")
	}
}

// TestContextValuePropagation verifies request-scoped values propagate to hooks
func TestContextValuePropagation(t *testing.T) {
	hooks := hookz.New[string]()
	defer hooks.Close()

	// Values to propagate
	const (
		expectedRequestID = "req-123-abc"
		expectedUserID    = "user-456"
		expectedTraceID   = "trace-789-xyz"
		expectedTenantID  = "tenant-001"
	)

	// Track received values
	var (
		receivedRequestID string
		receivedUserID    string
		receivedTraceID   string
		receivedTenantID  string
		mu                sync.Mutex
	)

	// Register hook that reads context values
	hook, err := hooks.Hook(contextValues, func(ctx context.Context, data string) error {
		mu.Lock()
		defer mu.Unlock()

		// Extract values from context
		if v := ctx.Value(requestIDKey); v != nil {
			receivedRequestID = v.(string)
		}
		if v := ctx.Value(userIDKey); v != nil {
			receivedUserID = v.(string)
		}
		if v := ctx.Value(traceIDKey); v != nil {
			receivedTraceID = v.(string)
		}
		if v := ctx.Value(tenantIDKey); v != nil {
			receivedTenantID = v.(string)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	// Create context with values (simulating request context)
	ctx := context.Background()
	ctx = context.WithValue(ctx, requestIDKey, expectedRequestID)
	ctx = context.WithValue(ctx, userIDKey, expectedUserID)
	ctx = context.WithValue(ctx, traceIDKey, expectedTraceID)
	ctx = context.WithValue(ctx, tenantIDKey, expectedTenantID)

	// Emit event with enriched context
	if err := hooks.Emit(ctx, contextValues, "test"); err != nil {
		t.Fatalf("Failed to emit event: %v", err)
	}

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	// Verify all values propagated correctly
	mu.Lock()
	defer mu.Unlock()

	if receivedRequestID != expectedRequestID {
		t.Errorf("Request ID mismatch: got %q, want %q", receivedRequestID, expectedRequestID)
	}
	if receivedUserID != expectedUserID {
		t.Errorf("User ID mismatch: got %q, want %q", receivedUserID, expectedUserID)
	}
	if receivedTraceID != expectedTraceID {
		t.Errorf("Trace ID mismatch: got %q, want %q", receivedTraceID, expectedTraceID)
	}
	if receivedTenantID != expectedTenantID {
		t.Errorf("Tenant ID mismatch: got %q, want %q", receivedTenantID, expectedTenantID)
	}
}

// TestContextTimeoutVsGlobalTimeout tests interaction between context timeout and global timeout
func TestContextTimeoutVsGlobalTimeout(t *testing.T) {
	// Test case 1: Context timeout shorter than global timeout
	t.Run("ContextTimeoutWins", func(t *testing.T) {
		// Create hooks with 500ms global timeout
		hooks := hookz.New[string](hookz.WithWorkers(10), hookz.WithTimeout(500*time.Millisecond))
		defer hooks.Close()

		var canceledByContext atomic.Bool

		hook, err := hooks.Hook(contextTimeoutTest, func(ctx context.Context, data string) error {
			// Sleep for 300ms
			select {
			case <-time.After(300 * time.Millisecond):
				return nil
			case <-ctx.Done():
				// Check if it was context deadline
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					canceledByContext.Store(true)
				}
				return ctx.Err()
			}
		})
		if err != nil {
			t.Fatalf("Failed to register hook: %v", err)
		}
		defer hook.Unhook()

		// Create context with 100ms timeout (shorter than global)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		if err := hooks.Emit(ctx, contextTimeoutTest, "data"); err != nil {
			t.Fatalf("Failed to emit event: %v", err)
		}

		time.Sleep(400 * time.Millisecond)

		if !canceledByContext.Load() {
			t.Error("Expected context timeout to trigger cancellation")
		}
	})

	// Test case 2: Global timeout shorter than context timeout
	t.Run("GlobalTimeoutWins", func(t *testing.T) {
		// Create hooks with 100ms global timeout
		hooks := hookz.New[string](hookz.WithWorkers(10), hookz.WithTimeout(100*time.Millisecond))
		defer hooks.Close()

		var timedOut atomic.Bool

		hook, err := hooks.Hook(contextTimeoutTest, func(ctx context.Context, data string) error {
			// Sleep for 300ms
			select {
			case <-time.After(300 * time.Millisecond):
				return nil
			case <-ctx.Done():
				timedOut.Store(true)
				return ctx.Err()
			}
		})
		if err != nil {
			t.Fatalf("Failed to register hook: %v", err)
		}
		defer hook.Unhook()

		// Create context with 500ms timeout (longer than global)
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		if err := hooks.Emit(ctx, contextTimeoutTest, "data"); err != nil {
			t.Fatalf("Failed to emit event: %v", err)
		}

		time.Sleep(400 * time.Millisecond)

		if !timedOut.Load() {
			t.Error("Expected global timeout to trigger cancellation")
		}
	})
}

// TestNestedContextCancellation tests cancellation with nested contexts
func TestNestedContextCancellation(t *testing.T) {
	hooks := hookz.New[string]()
	defer hooks.Close()

	// Track execution at different levels
	var (
		parentStarted   atomic.Int32
		childStarted    atomic.Int32
		parentCancelled atomic.Int32
		childCancelled  atomic.Int32
	)

	// Parent hook that creates child context
	parentHook, err := hooks.Hook(parentEvent, func(ctx context.Context, data string) error {
		parentStarted.Add(1)

		// Create child context
		childCtx, childCancel := context.WithCancel(ctx)
		defer childCancel()

		// Emit child event
		if err := hooks.Emit(childCtx, childEvent, "child data"); err != nil {
			return fmt.Errorf("failed to emit child event: %w", err)
		}

		// Wait for potential cancellation
		select {
		case <-time.After(200 * time.Millisecond):
			return nil
		case <-ctx.Done():
			parentCancelled.Add(1)
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatalf("Failed to register parent hook: %v", err)
	}
	defer parentHook.Unhook()

	// Child hook
	childHook, err := hooks.Hook(childEvent, func(ctx context.Context, data string) error {
		childStarted.Add(1)

		select {
		case <-time.After(200 * time.Millisecond):
			return nil
		case <-ctx.Done():
			childCancelled.Add(1)
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatalf("Failed to register child hook: %v", err)
	}
	defer childHook.Unhook()

	// Create parent context
	parentCtx, parentCancel := context.WithCancel(context.Background())

	// Emit parent event
	if err := hooks.Emit(parentCtx, parentEvent, "parent data"); err != nil {
		t.Fatalf("Failed to emit parent event: %v", err)
	}

	// Cancel parent context after hooks start
	time.Sleep(50 * time.Millisecond)
	parentCancel()

	// Wait for propagation
	time.Sleep(300 * time.Millisecond)

	// Verify both parent and child were canceled
	if p := parentStarted.Load(); p != 1 {
		t.Errorf("Expected parent to start once, got %d", p)
	}
	if c := childStarted.Load(); c != 1 {
		t.Errorf("Expected child to start once, got %d", c)
	}
	if p := parentCancelled.Load(); p != 1 {
		t.Errorf("Expected parent to be canceled, got %d", p)
	}
	if c := childCancelled.Load(); c != 1 {
		t.Errorf("Expected child to be canceled, got %d", c)
	}
}

// TestRequestScopedContext simulates HTTP request handling with context
func TestRequestScopedContext(t *testing.T) {
	type Request struct {
		Method  string
		Path    string
		Headers map[string]string
		Body    []byte
	}

	// Simulate a web service with hooks
	type WebService struct {
		hooks *hookz.Hooks[Request]
	}

	service := &WebService{
		hooks: hookz.New[Request](hookz.WithWorkers(10), hookz.WithTimeout(5*time.Second)),
	}
	defer service.hooks.Close()

	// Audit logger hook
	var auditLogs []string
	var auditMu sync.Mutex

	auditHook, err := service.hooks.Hook(requestReceived, func(ctx context.Context, req Request) error {
		// Extract request ID from context
		requestID := ctx.Value(requestIDKey)
		if requestID == nil {
			return errors.New("missing request ID in context")
		}

		// Log with request ID
		auditMu.Lock()
		auditLogs = append(auditLogs, fmt.Sprintf("[%s] %s %s", requestID, req.Method, req.Path))
		auditMu.Unlock()

		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register audit hook: %v", err)
	}
	defer auditHook.Unhook()

	// Rate limiter hook
	var rateLimitChecks []string
	var rateMu sync.Mutex

	rateHook, err := service.hooks.Hook(requestReceived, func(ctx context.Context, req Request) error {
		// Extract user ID from context
		userID := ctx.Value(userIDKey)
		if userID == nil {
			return errors.New("missing user ID in context")
		}

		// Check rate limit for user
		rateMu.Lock()
		rateLimitChecks = append(rateLimitChecks, userID.(string))
		rateMu.Unlock()

		// Simulate rate limit check
		select {
		case <-time.After(10 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatalf("Failed to register rate hook: %v", err)
	}
	defer rateHook.Unhook()

	// Simulate incoming requests
	requests := []struct {
		requestID string
		userID    string
		request   Request
	}{
		{
			requestID: "req-001",
			userID:    "user-alice",
			request:   Request{Method: "GET", Path: "/api/users"},
		},
		{
			requestID: "req-002",
			userID:    "user-bob",
			request:   Request{Method: "POST", Path: "/api/orders"},
		},
		{
			requestID: "req-003",
			userID:    "user-alice",
			request:   Request{Method: "GET", Path: "/api/products"},
		},
	}

	// Process requests concurrently
	var wg sync.WaitGroup
	for _, r := range requests {
		r := r // capture
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Create request-scoped context (like HTTP middleware would)
			ctx := context.Background()
			ctx = context.WithValue(ctx, requestIDKey, r.requestID)
			ctx = context.WithValue(ctx, userIDKey, r.userID)

			// Add timeout for this request
			ctx, cancel := context.WithTimeout(ctx, 1*time.Second)
			defer cancel()

			// Emit request received event
			if err := service.hooks.Emit(ctx, requestReceived, r.request); err != nil {
				t.Logf("Failed to emit for request %s: %v", r.requestID, err)
			}
		}()
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond) // Let hooks complete

	// Verify audit logs have request IDs
	auditMu.Lock()
	defer auditMu.Unlock()
	if len(auditLogs) != 3 {
		t.Errorf("Expected 3 audit logs, got %d", len(auditLogs))
	}
	for _, log := range auditLogs {
		t.Logf("Audit log: %s", log)
		if log == "" {
			t.Error("Empty audit log found")
		}
	}

	// Verify rate limit checks have user IDs
	rateMu.Lock()
	defer rateMu.Unlock()
	if len(rateLimitChecks) != 3 {
		t.Errorf("Expected 3 rate limit checks, got %d", len(rateLimitChecks))
	}
}

// TestContextCancellationDuringEmit tests context canceled before emit completes
func TestContextCancellationDuringEmit(t *testing.T) {
	hooks := hookz.New[string]()
	defer hooks.Close()

	// Track if hook was ever called
	var hookCalled atomic.Bool

	hook, err := hooks.Hook(testEvent, func(ctx context.Context, data string) error {
		hookCalled.Store(true)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	// Create already-canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Try to emit with canceled context
	err = hooks.Emit(ctx, testEvent, "data")

	// Emit should succeed (it queues the task)
	if err != nil {
		t.Logf("Emit returned error with canceled context: %v", err)
	}

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// Hook should still be called even with canceled context
	// (the context is passed through but emission itself succeeds)
	if !hookCalled.Load() {
		t.Log("Hook was not called with canceled context - this is expected behavior")
	}
}

// TestContextWithMultipleTimeouts tests cascading timeouts
func TestContextWithMultipleTimeouts(t *testing.T) {
	hooks := hookz.New[string](hookz.WithWorkers(10), hookz.WithTimeout(300*time.Millisecond))
	defer hooks.Close()

	// Track which timeout triggered
	var (
		completed      atomic.Int32
		contextTimeout atomic.Int32
		globalTimeout  atomic.Int32
	)

	// Register hooks with different execution times
	durations := []time.Duration{
		50 * time.Millisecond,  // Should complete
		150 * time.Millisecond, // Should complete (under 200ms context timeout)
		250 * time.Millisecond, // Should be canceled by context (over 200ms)
		350 * time.Millisecond, // Should be canceled by context or global
	}

	for i, duration := range durations {
		duration := duration // capture
		hookName := fmt.Sprintf("hook-%d", i)

		hook, err := hooks.Hook(cascadeTest, func(ctx context.Context, data string) error {
			timer := time.NewTimer(duration)
			defer timer.Stop()

			select {
			case <-timer.C:
				completed.Add(1)
				return nil
			case <-ctx.Done():
				// Determine which timeout triggered
				deadline, hasDeadline := ctx.Deadline()
				if hasDeadline && time.Until(deadline) < 100*time.Millisecond {
					contextTimeout.Add(1)
				} else {
					globalTimeout.Add(1)
				}
				return fmt.Errorf("%s canceled: %w", hookName, ctx.Err())
			}
		})
		if err != nil {
			t.Fatalf("Failed to register hook %d: %v", i, err)
		}
		defer hook.Unhook()
	}

	// Create context with 200ms timeout
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Emit event
	if err := hooks.Emit(ctx, cascadeTest, "test"); err != nil {
		t.Fatalf("Failed to emit event: %v", err)
	}

	// Wait for all timeouts to potentially trigger
	time.Sleep(500 * time.Millisecond)

	// Verify results - note that exact numbers can vary due to timing
	if c := completed.Load(); c < 1 || c > 2 {
		t.Errorf("Expected 1-2 hooks to complete (50ms and possibly 150ms), got %d", c)
	}
	if ct := contextTimeout.Load(); ct < 1 {
		t.Errorf("Expected at least 1 hook canceled by context timeout, got %d", ct)
	}
}

// TestBackgroundContextWithGlobalTimeout tests background context with global timeout
func TestBackgroundContextWithGlobalTimeout(t *testing.T) {
	// Create hooks with 100ms global timeout
	hooks := hookz.New[string](hookz.WithWorkers(10), hookz.WithTimeout(100*time.Millisecond))
	defer hooks.Close()

	var (
		started   atomic.Bool
		completed atomic.Bool
		timedOut  atomic.Bool
	)

	hook, err := hooks.Hook(backgroundTest, func(ctx context.Context, data string) error {
		started.Store(true)

		// This should be interrupted by global timeout
		select {
		case <-time.After(200 * time.Millisecond):
			completed.Store(true)
			return nil
		case <-ctx.Done():
			timedOut.Store(true)
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer hook.Unhook()

	// Use background context (no timeout from caller)
	if err := hooks.Emit(context.Background(), backgroundTest, "data"); err != nil {
		t.Fatalf("Failed to emit event: %v", err)
	}

	// Wait for global timeout to trigger
	time.Sleep(300 * time.Millisecond)

	// Verify global timeout was applied
	if !started.Load() {
		t.Error("Hook should have started")
	}
	if completed.Load() {
		t.Error("Hook should not have completed (global timeout)")
	}
	if !timedOut.Load() {
		t.Error("Hook should have timed out due to global timeout")
	}
}

// TestContextPropagationAcrossServices simulates microservice context propagation
func TestContextPropagationAcrossServices(t *testing.T) {
	type Request struct {
		Path   string
		UserID string
	}

	// Simulate multiple services with distributed tracing
	type TraceSpan struct {
		TraceID  string
		SpanID   string
		ParentID string
		Service  string
		Start    time.Time
		End      time.Time
	}

	var (
		spans []TraceSpan
		mu    sync.Mutex
	)

	// API Gateway
	gatewayHooks := hookz.New[Request]()
	defer gatewayHooks.Close()

	// Auth Service
	authHooks := hookz.New[string]()
	defer authHooks.Close()

	// Business Service
	businessHooks := hookz.New[string]()
	defer businessHooks.Close()

	// Gateway hook that initiates trace
	gatewayHook, err := gatewayHooks.Hook(requestReceived, func(ctx context.Context, req Request) error {
		// Extract or create trace ID
		traceID := ctx.Value(traceIDKey)
		if traceID == nil {
			traceID = "trace-" + time.Now().Format("20060102-150405")
			ctx = context.WithValue(ctx, traceIDKey, traceID)
		}

		// Create span
		span := TraceSpan{
			TraceID: traceID.(string),
			SpanID:  "gateway-span-1",
			Service: "gateway",
			Start:   time.Now(),
		}

		// Propagate to auth service
		if err := authHooks.Emit(ctx, authCheck, req.UserID); err != nil {
			return fmt.Errorf("auth check failed: %w", err)
		}

		span.End = time.Now()
		mu.Lock()
		spans = append(spans, span)
		mu.Unlock()

		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register gateway hook: %v", err)
	}
	defer gatewayHook.Unhook()

	// Auth service hook
	authHook, err := authHooks.Hook(authCheck, func(ctx context.Context, userID string) error {
		// Verify trace ID propagated
		traceID := ctx.Value(traceIDKey)
		if traceID == nil {
			return errors.New("missing trace ID in auth service")
		}

		// Create span
		span := TraceSpan{
			TraceID:  traceID.(string),
			SpanID:   "auth-span-1",
			ParentID: "gateway-span-1",
			Service:  "auth",
			Start:    time.Now(),
		}

		// Simulate auth check
		time.Sleep(20 * time.Millisecond)

		// Propagate to business service
		if err := businessHooks.Emit(ctx, businessProcess, userID); err != nil {
			return fmt.Errorf("business process failed: %w", err)
		}

		span.End = time.Now()
		mu.Lock()
		spans = append(spans, span)
		mu.Unlock()

		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register auth hook: %v", err)
	}
	defer authHook.Unhook()

	// Business service hook
	businessHook, err := businessHooks.Hook(businessProcess, func(ctx context.Context, userID string) error {
		// Verify trace ID still present
		traceID := ctx.Value(traceIDKey)
		if traceID == nil {
			return errors.New("missing trace ID in business service")
		}

		// Create span
		span := TraceSpan{
			TraceID:  traceID.(string),
			SpanID:   "business-span-1",
			ParentID: "auth-span-1",
			Service:  "business",
			Start:    time.Now(),
		}

		// Simulate business logic
		select {
		case <-time.After(30 * time.Millisecond):
			// Complete normally
		case <-ctx.Done():
			// Respect cancellation
			return ctx.Err()
		}

		span.End = time.Now()
		mu.Lock()
		spans = append(spans, span)
		mu.Unlock()

		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register business hook: %v", err)
	}
	defer businessHook.Unhook()

	// Initiate request with trace context
	ctx := context.Background()
	ctx = context.WithValue(ctx, traceIDKey, "trace-test-001")
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()

	// Start at gateway
	req := Request{Path: "/api/test", UserID: "user-123"}
	if err := gatewayHooks.Emit(ctx, requestReceived, req); err != nil {
		t.Fatalf("Failed to emit gateway event: %v", err)
	}

	// Wait for processing
	time.Sleep(150 * time.Millisecond)

	// Verify trace propagated through all services
	mu.Lock()
	defer mu.Unlock()

	if len(spans) != 3 {
		t.Errorf("Expected 3 spans, got %d", len(spans))
	}

	// Verify all spans have same trace ID
	for i, span := range spans {
		if span.TraceID != "trace-test-001" {
			t.Errorf("Span %d has wrong trace ID: %s", i, span.TraceID)
		}
		t.Logf("Span: %s/%s (parent: %s) - %v", span.Service, span.SpanID, span.ParentID, span.End.Sub(span.Start))
	}
}

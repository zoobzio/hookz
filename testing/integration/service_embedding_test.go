// Package integration provides comprehensive integration tests for the hookz package.
// These tests demonstrate real-world usage patterns and verify that the interface
// segregation works correctly in practice.
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

// Event keys for service embedding testing
const (
	orderCreatedEvent      hookz.Key = "order.created"
	paymentFailedEvent     hookz.Key = "payment.failed"
	paymentSucceededEvent  hookz.Key = "payment.succeeded"
	orderStatusPending     hookz.Key = "order.status.pending"
	serviceOrderStatusPaid hookz.Key = "order.status.paid"
)

// OrderService demonstrates service embedding patterns with hookz.
// The service maintains ownership of its lifecycle while exposing
// only the registry interface to consumers.
type OrderService struct {
	// Private fields - service owns its state
	db     *mockDatabase
	logger *mockLogger

	// Hook system - service owns emission and lifecycle
	hooks *hookz.Hooks[Order]

	// Internal state
	mu     sync.RWMutex
	orders map[string]*Order
}

// Order represents a business entity
type Order struct {
	ID         string
	CustomerID string
	Total      float64
	Status     string
	Items      []OrderItem
	CreatedAt  time.Time
}

// OrderItem represents order line items
type OrderItem struct {
	ProductID string
	Quantity  int
	Price     float64
}

// NewOrderService creates a new order service with hook support
func NewOrderService() *OrderService {
	svc := &OrderService{
		db:     &mockDatabase{},
		logger: &mockLogger{},
		hooks:  hookz.New[Order](hookz.WithWorkers(10), hookz.WithTimeout(5*time.Second)),
		orders: make(map[string]*Order),
	}
	return svc
}

// Events exposes the hook registry for consumers.
// This is the key interface segregation - consumers can only
// register/unregister hooks, not emit events or control lifecycle.
func (s *OrderService) Events() *hookz.Hooks[Order] {
	return s.hooks
}

// CreateOrder demonstrates internal event emission
func (s *OrderService) CreateOrder(ctx context.Context, order Order) (*Order, error) {
	// Validate order
	if order.CustomerID == "" {
		return nil, errors.New("customer ID required")
	}
	if len(order.Items) == 0 {
		return nil, errors.New("order must have items")
	}

	// Generate ID and timestamp
	order.ID = generateOrderID()
	order.CreatedAt = time.Now()
	order.Status = "pending"

	// Store order
	s.mu.Lock()
	s.orders[order.ID] = &order
	s.mu.Unlock()

	// Log the operation
	s.logger.Log("order.created", order.ID)

	// Emit event - only the service can emit
	// This is fire-and-forget for performance
	if err := s.hooks.Emit(context.Background(), orderCreatedEvent, order); err != nil {
		// Log emission failure but don't fail the order
		s.logger.Log("emission.failed", err.Error())
	}

	return &order, nil
}

// UpdateOrderStatus demonstrates another internal emission pattern
func (s *OrderService) UpdateOrderStatus(ctx context.Context, orderID, status string) error {
	s.mu.Lock()
	order, exists := s.orders[orderID]
	if !exists {
		s.mu.Unlock()
		return fmt.Errorf("order %s not found", orderID)
	}

	oldStatus := order.Status
	order.Status = status
	s.mu.Unlock()

	// Emit status change event with context for tracing
	var statusEvent hookz.Key
	switch status {
	case "pending":
		statusEvent = orderStatusPending
	case "paid":
		statusEvent = serviceOrderStatusPaid
	default:
		// Log unknown status but continue - defensive programming
		s.logger.Log("unknown.status", fmt.Sprintf("status=%s", status))
		return nil // Don't emit for unknown statuses
	}

	if err := s.hooks.Emit(ctx, statusEvent, *order); err != nil {
		// For critical events, we might want to retry or queue
		s.logger.Log("critical.emission.failed", fmt.Sprintf("%s->%s: %v", oldStatus, status, err))
		// In production, might want to queue for retry
	}

	return nil
}

// ProcessPayment demonstrates high-volume event scenarios
func (s *OrderService) ProcessPayment(ctx context.Context, orderID string, amount float64) error {
	s.mu.RLock()
	order, exists := s.orders[orderID]
	s.mu.RUnlock()

	if !exists {
		return fmt.Errorf("order %s not found", orderID)
	}

	// Simulate payment processing
	if amount < order.Total {
		// Emit payment failure event
		if err := s.hooks.Emit(ctx, paymentFailedEvent, *order); err != nil {
			s.logger.Log("emission.failed", err.Error())
		}
		return fmt.Errorf("insufficient payment: %f < %f", amount, order.Total)
	}

	// Update order status
	if err := s.UpdateOrderStatus(ctx, orderID, "paid"); err != nil {
		return err
	}

	// Emit payment success event
	if err := s.hooks.Emit(ctx, paymentSucceededEvent, *order); err != nil {
		s.logger.Log("emission.failed", err.Error())
	}

	return nil
}

// Shutdown demonstrates proper service lifecycle management
func (s *OrderService) Shutdown() error {
	// First stop accepting new orders (not shown)

	// Then close the hook system to drain pending hooks
	if err := s.hooks.Close(); err != nil {
		return fmt.Errorf("failed to close hooks: %w", err)
	}

	// Finally close other resources
	s.db.Close()
	s.logger.Close()

	return nil
}

// Mock dependencies for testing
type mockDatabase struct {
	closed bool
}

func (m *mockDatabase) Close() {
	m.closed = true
}

type mockLogger struct {
	logs   []string
	mu     sync.Mutex
	closed bool
}

func (m *mockLogger) Log(event, msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, fmt.Sprintf("[%s] %s", event, msg))
}

func (m *mockLogger) Close() {
	m.closed = true
}

var orderCounter atomic.Int64

func generateOrderID() string {
	return fmt.Sprintf("ORD-%06d", orderCounter.Add(1))
}

// TestServiceEmbedding verifies the service embedding pattern works correctly
func TestServiceEmbedding(t *testing.T) {
	// Create service
	svc := NewOrderService()
	defer svc.Shutdown()

	// Consumer can only access the registry interface
	events := svc.Events()

	// Track events received
	var receivedEvents sync.Map

	// Register hook as a consumer would
	hook, err := events.Hook(orderCreatedEvent, func(ctx context.Context, order Order) error {
		receivedEvents.Store(order.ID, &order)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}

	// Create an order - triggers internal emission
	ctx := context.Background()
	order, err := svc.CreateOrder(ctx, Order{
		CustomerID: "CUST-001",
		Total:      99.99,
		Items: []OrderItem{
			{ProductID: "PROD-001", Quantity: 2, Price: 49.99},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create order: %v", err)
	}

	// Wait briefly for async hook execution
	time.Sleep(10 * time.Millisecond)

	// Verify hook received the event
	received, ok := receivedEvents.Load(order.ID)
	if !ok {
		t.Fatal("Hook did not receive order.created event")
	}

	receivedOrder := received.(*Order)
	if receivedOrder.CustomerID != order.CustomerID {
		t.Errorf("Received order has wrong customer: %s != %s",
			receivedOrder.CustomerID, order.CustomerID)
	}

	// Consumer can unhook
	if err := events.Unhook(hook); err != nil {
		t.Errorf("Failed to unhook: %v", err)
	}

	// Create another order - should not trigger unhooked callback
	order2, err := svc.CreateOrder(ctx, Order{
		CustomerID: "CUST-002",
		Total:      149.99,
		Items: []OrderItem{
			{ProductID: "PROD-002", Quantity: 1, Price: 149.99},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create second order: %v", err)
	}

	// Wait briefly
	time.Sleep(10 * time.Millisecond)

	// Verify second order was not received
	_, ok = receivedEvents.Load(order2.ID)
	if ok {
		t.Error("Unhooked callback still received event")
	}
}

// TestMultipleConsumers verifies multiple consumers can register hooks
func TestMultipleConsumers(t *testing.T) {
	svc := NewOrderService()
	defer svc.Shutdown()

	ctx := context.Background()
	events := svc.Events()

	// Simulate multiple consumers
	var analyticsCount atomic.Int32
	var auditCount atomic.Int32
	var notificationCount atomic.Int32

	// Analytics consumer
	analyticsHook, err := events.Hook(orderCreatedEvent, func(ctx context.Context, order Order) error {
		analyticsCount.Add(1)
		// Simulate analytics processing
		time.Sleep(5 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("Analytics hook failed: %v", err)
	}

	// Audit consumer
	auditHook, err := events.Hook(orderCreatedEvent, func(ctx context.Context, order Order) error {
		auditCount.Add(1)
		// Simulate audit logging
		time.Sleep(3 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("Audit hook failed: %v", err)
	}

	// Notification consumer
	notificationHook, err := events.Hook(orderCreatedEvent, func(ctx context.Context, order Order) error {
		notificationCount.Add(1)
		// Simulate notification sending
		time.Sleep(10 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("Notification hook failed: %v", err)
	}

	// Create multiple orders
	for i := 0; i < 5; i++ {
		_, err := svc.CreateOrder(ctx, Order{
			CustomerID: fmt.Sprintf("CUST-%03d", i),
			Total:      float64(100 + i*10),
			Items: []OrderItem{
				{ProductID: fmt.Sprintf("PROD-%03d", i), Quantity: 1, Price: float64(100 + i*10)},
			},
		})
		if err != nil {
			t.Fatalf("Failed to create order %d: %v", i, err)
		}
	}

	// Wait for all hooks to complete
	time.Sleep(100 * time.Millisecond)

	// Verify all consumers received all events
	if count := analyticsCount.Load(); count != 5 {
		t.Errorf("Analytics received %d events, expected 5", count)
	}
	if count := auditCount.Load(); count != 5 {
		t.Errorf("Audit received %d events, expected 5", count)
	}
	if count := notificationCount.Load(); count != 5 {
		t.Errorf("Notification received %d events, expected 5", count)
	}

	// Clean up hooks
	events.Unhook(analyticsHook)
	events.Unhook(auditHook)
	events.Unhook(notificationHook)
}

// TestLifecycleManagement verifies proper service lifecycle handling
func TestLifecycleManagement(t *testing.T) {
	svc := NewOrderService()
	ctx := context.Background()

	var hookExecuted atomic.Bool
	var hookCompleted atomic.Bool

	// Register a slow hook
	hook, err := svc.Events().Hook(orderCreatedEvent, func(ctx context.Context, order Order) error {
		hookExecuted.Store(true)
		// Simulate slow processing
		time.Sleep(50 * time.Millisecond)
		hookCompleted.Store(true)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to register hook: %v", err)
	}
	defer svc.Events().Unhook(hook)

	// Create an order to trigger the hook
	_, err = svc.CreateOrder(ctx, Order{
		CustomerID: "CUST-999",
		Total:      999.99,
		Items: []OrderItem{
			{ProductID: "PROD-999", Quantity: 1, Price: 999.99},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create order: %v", err)
	}

	// Verify hook started executing
	time.Sleep(10 * time.Millisecond)
	if !hookExecuted.Load() {
		t.Fatal("Hook did not start executing")
	}

	// Shutdown service - should wait for hook to complete
	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- svc.Shutdown()
	}()

	// Shutdown should block until hook completes
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Errorf("Shutdown failed: %v", err)
		}
		// Verify hook completed before shutdown
		if !hookCompleted.Load() {
			t.Error("Service shutdown before hook completed")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Shutdown timed out")
	}

	// Verify service is closed
	_, err = svc.CreateOrder(ctx, Order{
		CustomerID: "CUST-AFTER",
		Total:      100.00,
		Items: []OrderItem{
			{ProductID: "PROD-AFTER", Quantity: 1, Price: 100.00},
		},
	})
	// Should still succeed as CreateOrder doesn't check if hooks are closed
	if err != nil {
		t.Errorf("CreateOrder should not fail after shutdown: %v", err)
	}

	// But registering new hooks should fail
	_, err = svc.Events().Hook(orderCreatedEvent, func(ctx context.Context, order Order) error {
		return nil
	})
	if err != hookz.ErrServiceClosed {
		t.Errorf("Expected ErrServiceClosed, got: %v", err)
	}
}

// TestErrorPropagation verifies that hook errors are handled correctly
func TestErrorPropagation(t *testing.T) {
	svc := NewOrderService()
	defer svc.Shutdown()

	ctx := context.Background()
	events := svc.Events()

	var hook1Called atomic.Bool
	var hook2Called atomic.Bool
	var hook3Called atomic.Bool

	// Hook 1: Succeeds
	events.Hook(paymentSucceededEvent, func(ctx context.Context, order Order) error {
		hook1Called.Store(true)
		return nil
	})

	// Hook 2: Returns error
	events.Hook(paymentSucceededEvent, func(ctx context.Context, order Order) error {
		hook2Called.Store(true)
		return errors.New("payment validation failed")
	})

	// Hook 3: Should still execute even if hook 2 fails
	events.Hook(paymentSucceededEvent, func(ctx context.Context, order Order) error {
		hook3Called.Store(true)
		return nil
	})

	// Create and pay for an order
	order, _ := svc.CreateOrder(ctx, Order{
		CustomerID: "CUST-PAY",
		Total:      200.00,
		Items: []OrderItem{
			{ProductID: "PROD-PAY", Quantity: 2, Price: 100.00},
		},
	})

	// Process payment - triggers payment.succeeded hooks
	err := svc.ProcessPayment(ctx, order.ID, 200.00)
	if err != nil {
		t.Fatalf("Payment processing failed: %v", err)
	}

	// Wait for hooks to execute
	time.Sleep(50 * time.Millisecond)

	// Verify all hooks were called despite hook 2 error
	if !hook1Called.Load() {
		t.Error("Hook 1 was not called")
	}
	if !hook2Called.Load() {
		t.Error("Hook 2 was not called")
	}
	if !hook3Called.Load() {
		t.Error("Hook 3 was not called despite hook 2 error")
	}
}

// TestPanicRecovery verifies that panicking hooks don't crash the service
func TestPanicRecovery(t *testing.T) {
	svc := NewOrderService()
	defer svc.Shutdown()

	ctx := context.Background()
	events := svc.Events()

	var beforePanicCalled atomic.Bool
	var afterPanicCalled atomic.Bool

	// Hook 1: Normal hook before panic
	events.Hook(orderCreatedEvent, func(ctx context.Context, order Order) error {
		beforePanicCalled.Store(true)
		return nil
	})

	// Hook 2: Panics
	events.Hook(orderCreatedEvent, func(ctx context.Context, order Order) error {
		panic("simulated panic in hook")
	})

	// Hook 3: Should still execute after panic
	events.Hook(orderCreatedEvent, func(ctx context.Context, order Order) error {
		afterPanicCalled.Store(true)
		return nil
	})

	// Create order - should not panic despite hook 2
	_, err := svc.CreateOrder(ctx, Order{
		CustomerID: "CUST-PANIC",
		Total:      300.00,
		Items: []OrderItem{
			{ProductID: "PROD-PANIC", Quantity: 1, Price: 300.00},
		},
	})
	if err != nil {
		t.Fatalf("Order creation failed: %v", err)
	}

	// Wait for hooks
	time.Sleep(50 * time.Millisecond)

	// Verify hooks before and after panic were called
	if !beforePanicCalled.Load() {
		t.Error("Hook before panic was not called")
	}
	if !afterPanicCalled.Load() {
		t.Error("Hook after panic was not called")
	}

	// Service should still be operational
	_, err = svc.CreateOrder(ctx, Order{
		CustomerID: "CUST-AFTER-PANIC",
		Total:      400.00,
		Items: []OrderItem{
			{ProductID: "PROD-AFTER", Quantity: 1, Price: 400.00},
		},
	})
	if err != nil {
		t.Fatalf("Service is not operational after panic: %v", err)
	}
}

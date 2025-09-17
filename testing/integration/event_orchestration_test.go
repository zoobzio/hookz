package integration

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zoobzio/hookz"
)

// Event keys for orchestration testing
const (
	orderCreated      hookz.Key = "order.created"
	orderStatusPaid   hookz.Key = "order.status.paid"
	inventoryReserved hookz.Key = "inventory.reserved"
	shipmentCreated   hookz.Key = "shipment.created"
	userUpgraded      hookz.Key = "user.upgraded"
	userCreated       hookz.Key = "user.created"
	inventoryConsumed hookz.Key = "inventory.consumed"
	shipmentUpgraded  hookz.Key = "shipment.upgraded"
)

// EventOrchestrator demonstrates complex event-driven workflows
// where multiple services interact through hooks.
type EventOrchestrator struct {
	// Services that participate in orchestration
	userService      *UserService
	orderService     *OrderService
	inventoryService *InventoryService
	shippingService  *ShippingService

	// Orchestration state
	workflows map[string]*Workflow
	mu        sync.RWMutex
}

// UserService manages users with hook support
type UserService struct {
	hooks *hookz.Hooks[User]
	users map[string]*User
	mu    sync.RWMutex
}

// User represents a user entity
type User struct {
	ID        string
	Email     string
	Name      string
	Tier      string // "bronze", "silver", "gold", "platinum"
	CreatedAt time.Time
}

// InventoryService manages inventory with hook support
type InventoryService struct {
	hooks     *hookz.Hooks[InventoryEvent]
	inventory map[string]*InventoryItem
	mu        sync.RWMutex
}

// InventoryEvent represents inventory changes
type InventoryEvent struct {
	ProductID string
	Quantity  int
	Operation string // "reserve", "release", "consume"
	OrderID   string
	Timestamp time.Time
}

// InventoryItem tracks product stock
type InventoryItem struct {
	ProductID string
	Available int
	Reserved  int
}

// ShippingService manages shipping with hook support
type ShippingService struct {
	hooks     *hookz.Hooks[ShippingEvent]
	shipments map[string]*Shipment
	mu        sync.RWMutex
}

// ShippingEvent represents shipping updates
type ShippingEvent struct {
	ShipmentID string
	OrderID    string
	Status     string // "pending", "processing", "shipped", "delivered"
	Carrier    string
	Tracking   string
	Timestamp  time.Time
}

// Shipment tracks a shipment
type Shipment struct {
	ID       string
	OrderID  string
	Status   string
	Carrier  string
	Tracking string
}

// Workflow tracks a multi-step process
type Workflow struct {
	ID        string
	Type      string
	Status    string
	Steps     []WorkflowStep
	StartedAt time.Time
	UpdatedAt time.Time
}

// WorkflowStep represents a step in a workflow
type WorkflowStep struct {
	Name      string
	Status    string // "pending", "running", "completed", "failed"
	StartedAt time.Time
	EndedAt   time.Time
	Error     string
}

// NewEventOrchestrator creates a new orchestrator with all services
func NewEventOrchestrator() *EventOrchestrator {
	orch := &EventOrchestrator{
		userService:      NewUserService(),
		orderService:     NewOrderService(),
		inventoryService: NewInventoryService(),
		shippingService:  NewShippingService(),
		workflows:        make(map[string]*Workflow),
	}

	// Wire up cross-service event handlers
	orch.setupEventHandlers()

	return orch
}

// NewUserService creates a user service
func NewUserService() *UserService {
	return &UserService{
		hooks: hookz.New[User](hookz.WithWorkers(10), hookz.WithQueueSize(100), hookz.WithTimeout(5*time.Second)),
		users: make(map[string]*User),
	}
}

// NewInventoryService creates an inventory service
func NewInventoryService() *InventoryService {
	svc := &InventoryService{
		hooks:     hookz.New[InventoryEvent](hookz.WithWorkers(10), hookz.WithQueueSize(100), hookz.WithTimeout(5*time.Second)),
		inventory: make(map[string]*InventoryItem),
	}

	// Initialize some inventory
	svc.inventory["PROD-001"] = &InventoryItem{ProductID: "PROD-001", Available: 100}
	svc.inventory["PROD-002"] = &InventoryItem{ProductID: "PROD-002", Available: 50}
	svc.inventory["PROD-003"] = &InventoryItem{ProductID: "PROD-003", Available: 25}

	return svc
}

// NewShippingService creates a shipping service
func NewShippingService() *ShippingService {
	return &ShippingService{
		hooks:     hookz.New[ShippingEvent](hookz.WithWorkers(10), hookz.WithQueueSize(100), hookz.WithTimeout(5*time.Second)),
		shipments: make(map[string]*Shipment),
	}
}

// setupEventHandlers wires up the cross-service event handlers
func (o *EventOrchestrator) setupEventHandlers() {
	// When order is created, check inventory
	o.orderService.Events().Hook(orderCreated, func(ctx context.Context, order Order) error {
		return o.handleOrderCreated(ctx, order)
	})

	// When order is paid, create shipment
	o.orderService.Events().Hook(orderStatusPaid, func(ctx context.Context, order Order) error {
		return o.handleOrderPaid(ctx, order)
	})

	// When inventory is reserved, update order
	o.inventoryService.Events().Hook(inventoryReserved, func(ctx context.Context, event InventoryEvent) error {
		return o.handleInventoryReserved(ctx, event)
	})

	// When shipment is created, consume inventory
	o.shippingService.Events().Hook(shipmentCreated, func(ctx context.Context, event ShippingEvent) error {
		return o.handleShipmentCreated(ctx, event)
	})

	// Premium user gets expedited shipping
	o.userService.Events().Hook(userUpgraded, func(ctx context.Context, user User) error {
		return o.handleUserUpgraded(ctx, user)
	})
}

// handleOrderCreated reserves inventory when order is created
func (o *EventOrchestrator) handleOrderCreated(ctx context.Context, order Order) error {
	// Start workflow
	workflow := &Workflow{
		ID:        fmt.Sprintf("WF-%s", order.ID),
		Type:      "order_fulfillment",
		Status:    "running",
		StartedAt: time.Now(),
		Steps: []WorkflowStep{
			{Name: "inventory_check", Status: "pending"},
			{Name: "payment_processing", Status: "pending"},
			{Name: "shipping_creation", Status: "pending"},
			{Name: "notification_sent", Status: "pending"},
		},
	}

	o.mu.Lock()
	o.workflows[workflow.ID] = workflow
	o.mu.Unlock()

	// Reserve inventory for each item
	for _, item := range order.Items {
		if err := o.inventoryService.ReserveInventory(ctx, item.ProductID, item.Quantity, order.ID); err != nil {
			// Update workflow step
			o.updateWorkflowStep(workflow.ID, "inventory_check", "failed", err.Error())
			return fmt.Errorf("failed to reserve inventory: %w", err)
		}
	}

	o.updateWorkflowStep(workflow.ID, "inventory_check", "completed", "")
	return nil
}

// handleOrderPaid creates shipment when order is paid
func (o *EventOrchestrator) handleOrderPaid(ctx context.Context, order Order) error {
	workflowID := fmt.Sprintf("WF-%s", order.ID)
	o.updateWorkflowStep(workflowID, "payment_processing", "completed", "")

	// Create shipment
	shipment, err := o.shippingService.CreateShipment(ctx, order.ID)
	if err != nil {
		o.updateWorkflowStep(workflowID, "shipping_creation", "failed", err.Error())
		return fmt.Errorf("failed to create shipment: %w", err)
	}

	o.updateWorkflowStep(workflowID, "shipping_creation", "completed", "")

	// Check user tier for expedited shipping
	o.userService.mu.RLock()
	user, exists := o.userService.users[order.CustomerID]
	o.userService.mu.RUnlock()

	if exists && (user.Tier == "gold" || user.Tier == "platinum") {
		// Upgrade shipping for premium users
		o.shippingService.UpgradeShipping(shipment.ID, "express")
	}

	return nil
}

// handleInventoryReserved updates order when inventory is reserved
func (o *EventOrchestrator) handleInventoryReserved(ctx context.Context, event InventoryEvent) error {
	// Could update order with reservation confirmation
	// For now, just log the reservation
	return nil
}

// handleShipmentCreated consumes inventory when shipment is created
func (o *EventOrchestrator) handleShipmentCreated(ctx context.Context, event ShippingEvent) error {
	// Get order details
	o.orderService.mu.RLock()
	order, exists := o.orderService.orders[event.OrderID]
	o.orderService.mu.RUnlock()

	if !exists {
		return fmt.Errorf("order %s not found", event.OrderID)
	}

	// Consume reserved inventory
	for _, item := range order.Items {
		if err := o.inventoryService.ConsumeReserved(ctx, item.ProductID, item.Quantity, order.ID); err != nil {
			return fmt.Errorf("failed to consume inventory: %w", err)
		}
	}

	// Mark workflow complete
	workflowID := fmt.Sprintf("WF-%s", event.OrderID)
	o.updateWorkflowStep(workflowID, "notification_sent", "completed", "")

	o.mu.Lock()
	if workflow, exists := o.workflows[workflowID]; exists {
		workflow.Status = "completed"
		workflow.UpdatedAt = time.Now()
	}
	o.mu.Unlock()

	return nil
}

// handleUserUpgraded handles user tier upgrades
func (o *EventOrchestrator) handleUserUpgraded(ctx context.Context, user User) error {
	// Could trigger special promotions or notifications
	return nil
}

// updateWorkflowStep updates a workflow step status
func (o *EventOrchestrator) updateWorkflowStep(workflowID, stepName, status, errorMsg string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	workflow, exists := o.workflows[workflowID]
	if !exists {
		return
	}

	for i := range workflow.Steps {
		if workflow.Steps[i].Name == stepName {
			workflow.Steps[i].Status = status
			workflow.Steps[i].Error = errorMsg
			if status == "running" {
				workflow.Steps[i].StartedAt = time.Now()
			} else if status == "completed" || status == "failed" {
				workflow.Steps[i].EndedAt = time.Now()
			}
			break
		}
	}

	workflow.UpdatedAt = time.Now()
}

// Events returns the user service event registry
func (s *UserService) Events() *hookz.Hooks[User] {
	return s.hooks
}

// CreateUser creates a new user
func (s *UserService) CreateUser(ctx context.Context, user User) (*User, error) {
	user.ID = fmt.Sprintf("USER-%d", time.Now().UnixNano())
	user.CreatedAt = time.Now()
	if user.Tier == "" {
		user.Tier = "bronze"
	}

	s.mu.Lock()
	s.users[user.ID] = &user
	s.mu.Unlock()

	// Non-critical event - log but don't fail user creation
	if err := s.hooks.Emit(context.Background(), userCreated, user); err != nil {
		// In production: log error, increment metrics
		// Don't fail user creation for event emission failure
		_ = err // Explicitly ignore error for event emission
	}
	return &user, nil
}

// UpgradeUser upgrades a user's tier
func (s *UserService) UpgradeUser(ctx context.Context, userID, newTier string) error {
	s.mu.Lock()
	user, exists := s.users[userID]
	if !exists {
		s.mu.Unlock()
		return fmt.Errorf("user %s not found", userID)
	}

	oldTier := user.Tier
	user.Tier = newTier
	s.mu.Unlock()

	if oldTier != newTier {
		// Non-critical event - log but don't fail upgrade
		if err := s.hooks.Emit(ctx, userUpgraded, *user); err != nil {
			// In production: log error, increment metrics
			// Don't fail upgrade for event emission failure
			_ = err // Explicitly ignore error for event emission
		}
	}

	return nil
}

// Events returns the inventory service event registry
func (s *InventoryService) Events() *hookz.Hooks[InventoryEvent] {
	return s.hooks
}

// ReserveInventory reserves inventory for an order
func (s *InventoryService) ReserveInventory(ctx context.Context, productID string, quantity int, orderID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	item, exists := s.inventory[productID]
	if !exists {
		return fmt.Errorf("product %s not found", productID)
	}

	if item.Available < quantity {
		return fmt.Errorf("insufficient inventory: %d < %d", item.Available, quantity)
	}

	item.Available -= quantity
	item.Reserved += quantity

	event := InventoryEvent{
		ProductID: productID,
		Quantity:  quantity,
		Operation: "reserve",
		OrderID:   orderID,
		Timestamp: time.Now(),
	}

	// Event emission is non-critical for inventory operations
	// The actual inventory state change has already happened
	if err := s.hooks.Emit(context.Background(), inventoryReserved, event); err != nil {
		// In production: log error, increment metrics, maybe queue for retry
		// Don't fail the inventory operation itself
		_ = err // Explicitly ignore error for event emission
	}
	return nil
}

// ConsumeReserved consumes reserved inventory
func (s *InventoryService) ConsumeReserved(ctx context.Context, productID string, quantity int, orderID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	item, exists := s.inventory[productID]
	if !exists {
		return fmt.Errorf("product %s not found", productID)
	}

	if item.Reserved < quantity {
		return fmt.Errorf("insufficient reserved inventory: %d < %d", item.Reserved, quantity)
	}

	item.Reserved -= quantity

	event := InventoryEvent{
		ProductID: productID,
		Quantity:  quantity,
		Operation: "consume",
		OrderID:   orderID,
		Timestamp: time.Now(),
	}

	// Event emission is non-critical for inventory operations
	// The actual inventory state change has already happened
	if err := s.hooks.Emit(context.Background(), inventoryConsumed, event); err != nil {
		// In production: log error, increment metrics, maybe queue for retry
		// Don't fail the inventory operation itself
		_ = err // Placeholder for error handling
	}
	return nil
}

// Events returns the shipping service event registry
func (s *ShippingService) Events() *hookz.Hooks[ShippingEvent] {
	return s.hooks
}

// CreateShipment creates a new shipment
func (s *ShippingService) CreateShipment(ctx context.Context, orderID string) (*Shipment, error) {
	shipment := &Shipment{
		ID:       fmt.Sprintf("SHIP-%d", time.Now().UnixNano()),
		OrderID:  orderID,
		Status:   "pending",
		Carrier:  "standard",
		Tracking: fmt.Sprintf("TRK-%d", time.Now().UnixNano()),
	}

	s.mu.Lock()
	s.shipments[shipment.ID] = shipment
	s.mu.Unlock()

	event := ShippingEvent{
		ShipmentID: shipment.ID,
		OrderID:    orderID,
		Status:     "pending",
		Carrier:    "standard",
		Tracking:   shipment.Tracking,
		Timestamp:  time.Now(),
	}

	// Non-critical event - log but don't fail shipment creation
	if err := s.hooks.Emit(context.Background(), shipmentCreated, event); err != nil {
		// In production: log error, increment metrics
		// Don't fail shipment creation for event emission failure
		_ = err // Placeholder for error handling
	}
	return shipment, nil
}

// UpgradeShipping upgrades shipping method
func (s *ShippingService) UpgradeShipping(shipmentID, carrier string) error {
	s.mu.Lock()
	shipment, exists := s.shipments[shipmentID]
	if !exists {
		s.mu.Unlock()
		return fmt.Errorf("shipment %s not found", shipmentID)
	}

	shipment.Carrier = carrier
	s.mu.Unlock()

	event := ShippingEvent{
		ShipmentID: shipmentID,
		OrderID:    shipment.OrderID,
		Status:     shipment.Status,
		Carrier:    carrier,
		Tracking:   shipment.Tracking,
		Timestamp:  time.Now(),
	}

	s.hooks.Emit(context.Background(), shipmentUpgraded, event)
	return nil
}

// Shutdown shuts down all services
func (o *EventOrchestrator) Shutdown() error {
	// Shutdown all services
	if err := o.userService.hooks.Close(); err != nil {
		return err
	}
	if err := o.orderService.hooks.Close(); err != nil {
		return err
	}
	if err := o.inventoryService.hooks.Close(); err != nil {
		return err
	}
	return o.shippingService.hooks.Close()
}

// TestEventOrchestration tests complex multi-service workflows
func TestEventOrchestration(t *testing.T) {
	orch := NewEventOrchestrator()
	defer orch.Shutdown()

	ctx := context.Background()

	// Create a user
	user, err := orch.userService.CreateUser(ctx, User{
		Email: "test@example.com",
		Name:  "Test User",
		Tier:  "silver",
	})
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}

	// Create an order for the user
	order, err := orch.orderService.CreateOrder(ctx, Order{
		CustomerID: user.ID,
		Total:      150.00,
		Items: []OrderItem{
			{ProductID: "PROD-001", Quantity: 2, Price: 50.00},
			{ProductID: "PROD-002", Quantity: 1, Price: 50.00},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create order: %v", err)
	}

	// Wait for inventory reservation
	time.Sleep(50 * time.Millisecond)

	// Check inventory was reserved
	orch.inventoryService.mu.RLock()
	item1 := orch.inventoryService.inventory["PROD-001"]
	item2 := orch.inventoryService.inventory["PROD-002"]
	orch.inventoryService.mu.RUnlock()

	if item1.Available != 98 || item1.Reserved != 2 {
		t.Errorf("PROD-001 inventory incorrect: available=%d, reserved=%d",
			item1.Available, item1.Reserved)
	}
	if item2.Available != 49 || item2.Reserved != 1 {
		t.Errorf("PROD-002 inventory incorrect: available=%d, reserved=%d",
			item2.Available, item2.Reserved)
	}

	// Process payment
	err = orch.orderService.ProcessPayment(ctx, order.ID, 150.00)
	if err != nil {
		t.Fatalf("Failed to process payment: %v", err)
	}

	// Wait for shipment creation and inventory consumption
	time.Sleep(100 * time.Millisecond)

	// Check shipment was created
	orch.shippingService.mu.RLock()
	shipmentCount := len(orch.shippingService.shipments)
	orch.shippingService.mu.RUnlock()

	if shipmentCount != 1 {
		t.Errorf("Expected 1 shipment, got %d", shipmentCount)
	}

	// Check inventory was consumed
	orch.inventoryService.mu.RLock()
	item1 = orch.inventoryService.inventory["PROD-001"]
	item2 = orch.inventoryService.inventory["PROD-002"]
	orch.inventoryService.mu.RUnlock()

	if item1.Reserved != 0 {
		t.Errorf("PROD-001 reserved should be 0, got %d", item1.Reserved)
	}
	if item2.Reserved != 0 {
		t.Errorf("PROD-002 reserved should be 0, got %d", item2.Reserved)
	}

	// Check workflow completed
	orch.mu.RLock()
	workflow := orch.workflows[fmt.Sprintf("WF-%s", order.ID)]
	orch.mu.RUnlock()

	if workflow == nil {
		t.Fatal("Workflow not found")
	}
	if workflow.Status != "completed" {
		t.Errorf("Workflow status should be completed, got %s", workflow.Status)
	}

	// Verify all steps completed
	for _, step := range workflow.Steps {
		if step.Status != "completed" {
			t.Errorf("Step %s not completed: %s", step.Name, step.Status)
			if step.Error != "" {
				t.Errorf("Step %s error: %s", step.Name, step.Error)
			}
		}
	}
}

// TestPremiumUserShipping tests expedited shipping for premium users
func TestPremiumUserShipping(t *testing.T) {
	orch := NewEventOrchestrator()
	defer orch.Shutdown()

	ctx := context.Background()

	// Create a gold tier user
	user, _ := orch.userService.CreateUser(ctx, User{
		Email: "gold@example.com",
		Name:  "Gold User",
		Tier:  "gold",
	})

	// Create and pay for an order
	order, _ := orch.orderService.CreateOrder(ctx, Order{
		CustomerID: user.ID,
		Total:      100.00,
		Items: []OrderItem{
			{ProductID: "PROD-001", Quantity: 1, Price: 100.00},
		},
	})

	orch.orderService.ProcessPayment(ctx, order.ID, 100.00)

	// Wait for workflow
	time.Sleep(100 * time.Millisecond)

	// Check shipment was upgraded
	orch.shippingService.mu.RLock()
	var shipment *Shipment
	for _, s := range orch.shippingService.shipments {
		if s.OrderID == order.ID {
			shipment = s
			break
		}
	}
	orch.shippingService.mu.RUnlock()

	if shipment == nil {
		t.Fatal("Shipment not found")
	}
	if shipment.Carrier != "express" {
		t.Errorf("Premium user should have express shipping, got %s", shipment.Carrier)
	}
}

// TestConcurrentWorkflows tests multiple concurrent order workflows
func TestConcurrentWorkflows(t *testing.T) {
	orch := NewEventOrchestrator()
	defer orch.Shutdown()

	ctx := context.Background()

	// Create multiple users
	users := make([]*User, 10)
	for i := 0; i < 10; i++ {
		user, _ := orch.userService.CreateUser(ctx, User{
			Email: fmt.Sprintf("user%d@example.com", i),
			Name:  fmt.Sprintf("User %d", i),
			Tier:  "bronze",
		})
		users[i] = user
	}

	// Create orders concurrently
	var wg sync.WaitGroup
	var successCount atomic.Int32

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			// Create order
			order, err := orch.orderService.CreateOrder(ctx, Order{
				CustomerID: users[idx].ID,
				Total:      float64(100 + idx*10),
				Items: []OrderItem{
					{ProductID: "PROD-001", Quantity: 1, Price: float64(100 + idx*10)},
				},
			})
			if err != nil {
				t.Logf("Failed to create order %d: %v", idx, err)
				return
			}

			// Wait for inventory reservation to complete
			// This simulates real-world delay between order creation and payment
			time.Sleep(10 * time.Millisecond)

			// Process payment
			err = orch.orderService.ProcessPayment(ctx, order.ID, order.Total)
			if err != nil {
				t.Logf("Failed to process payment %d: %v", idx, err)
				return
			}

			successCount.Add(1)
		}(i)
	}

	wg.Wait()

	// Wait for all workflows to complete (including async event handlers)
	time.Sleep(500 * time.Millisecond)

	// Verify workflows
	if count := successCount.Load(); count < 8 {
		t.Errorf("Too few successful workflows: %d/10", count)
	}

	// Check final inventory state
	orch.inventoryService.mu.RLock()
	item := orch.inventoryService.inventory["PROD-001"]
	orch.inventoryService.mu.RUnlock()

	t.Logf("Final inventory state: Available=%d, Reserved=%d", item.Available, item.Reserved)
	t.Logf("Successful orders: %d", successCount.Load())

	consumed := 100 - item.Available - item.Reserved
	// Allow some tolerance due to concurrent processing
	if consumed < int(successCount.Load())-2 || consumed > int(successCount.Load())+2 {
		t.Errorf("Inventory consumed (%d) doesn't match successful orders (%d ±2)",
			consumed, successCount.Load())
	}
}

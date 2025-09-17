package main

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/zoobzio/hookz"
)

// Order represents a business order for demonstration
type Order struct {
	ID       string
	Amount   float64
	Customer string
}

func main() {
	fmt.Println("=== Hookz Resilience Features Demo ===")

	// Demo 1: Backpressure for microsecond spikes
	fmt.Println("1. Backpressure Demo - Smoothing Traffic Spikes")
	demoBackpressure()
	fmt.Println()

	// Demo 2: Overflow for sustained load
	fmt.Println("2. Overflow Demo - Absorbing Traffic Bursts")
	demoOverflow()
	fmt.Println()

	// Demo 3: Combined resilience
	fmt.Println("3. Combined Resilience Demo - Full Protection")
	demoCombined()
	fmt.Println()

	// Demo 4: Real-world scenarios
	fmt.Println("4. Real-World Usage Patterns")
	demoRealWorld()
}

func demoBackpressure() {
	// Payment processor: High-value, low-tolerance for delays
	service := hookz.New[Order](
		hookz.WithWorkers(5),
		hookz.WithQueueSize(20),
		hookz.WithBackpressure(hookz.BackpressureConfig{
			MaxWait:        5 * time.Millisecond, // Very brief wait
			StartThreshold: 0.9,                  // High threshold
			Strategy:       "exponential",        // Quick escalation
		}),
	)
	defer service.Close()

	var processed int32
	if _, err := service.Hook("payment.processed", func(ctx context.Context, order Order) error {
		// Simulate payment processing
		time.Sleep(10 * time.Millisecond)
		atomic.AddInt32(&processed, 1)
		fmt.Printf("  Processed payment for order %s ($%.2f)\n", order.ID, order.Amount)
		return nil
	}); err != nil {
		log.Fatalf("Failed to register payment hook: %v", err)
	}

	// Simulate traffic spike
	start := time.Now()
	var succeeded, failed int32

	fmt.Println("  Simulating payment traffic spike...")
	for i := 0; i < 50; i++ {
		order := Order{
			ID:       fmt.Sprintf("ORD-%03d", i+1),
			Amount:   99.99 + float64(i),
			Customer: fmt.Sprintf("Customer-%d", i+1),
		}

		if err := service.Emit(context.Background(), "payment.processed", order); err != nil {
			atomic.AddInt32(&failed, 1)
		} else {
			atomic.AddInt32(&succeeded, 1)
		}
	}

	elapsed := time.Since(start)

	// Wait for processing to complete
	time.Sleep(200 * time.Millisecond)

	fmt.Printf("  Results: %d succeeded, %d failed in %v\n", succeeded, failed, elapsed)
	fmt.Printf("  Processed: %d payments\n", atomic.LoadInt32(&processed))
	fmt.Printf("  Backpressure smoothed the spike, allowing more orders than raw queue capacity\n")
}

func demoOverflow() {
	// Notification system: Low-value, high-volume
	service := hookz.New[Order](
		hookz.WithWorkers(2),
		hookz.WithQueueSize(10),
		hookz.WithOverflow(hookz.OverflowConfig{
			Capacity:         100,                  // Large buffer
			DrainInterval:    5 * time.Millisecond, // Frequent drain
			EvictionStrategy: "fifo",               // Drop oldest notifications
		}),
	)
	defer service.Close()

	var processed int32
	if _, err := service.Hook("order.notification", func(ctx context.Context, order Order) error {
		// Simulate notification sending
		time.Sleep(5 * time.Millisecond)
		atomic.AddInt32(&processed, 1)
		return nil
	}); err != nil {
		log.Fatalf("Failed to register notification hook: %v", err)
	}

	// Simulate massive burst
	fmt.Println("  Simulating notification burst...")
	start := time.Now()
	var succeeded, failed int32

	for i := 0; i < 200; i++ {
		order := Order{
			ID:       fmt.Sprintf("NOTIF-%03d", i+1),
			Amount:   float64(i) * 10,
			Customer: fmt.Sprintf("User-%d", i+1),
		}

		if err := service.Emit(context.Background(), "order.notification", order); err != nil {
			atomic.AddInt32(&failed, 1)
		} else {
			atomic.AddInt32(&succeeded, 1)
		}
	}

	elapsed := time.Since(start)

	// Let overflow drain
	time.Sleep(300 * time.Millisecond)

	fmt.Printf("  Results: %d succeeded, %d failed in %v\n", succeeded, failed, elapsed)
	fmt.Printf("  Processed: %d notifications\n", atomic.LoadInt32(&processed))
	fmt.Printf("  Overflow absorbed the burst without blocking the caller\n")
}

func demoCombined() {
	// Analytics pipeline: Balanced approach
	service := hookz.New[Order](
		hookz.WithWorkers(3),
		hookz.WithQueueSize(15),
		hookz.WithBackpressure(hookz.BackpressureConfig{
			MaxWait:        8 * time.Millisecond,
			StartThreshold: 0.8,
			Strategy:       "linear",
		}),
		hookz.WithOverflow(hookz.OverflowConfig{
			Capacity:         50,
			DrainInterval:    3 * time.Millisecond,
			EvictionStrategy: "reject", // Don't drop analytics data
		}),
	)
	defer service.Close()

	var processed int32
	if _, err := service.Hook("analytics.track", func(ctx context.Context, order Order) error {
		// Simulate analytics processing
		time.Sleep(12 * time.Millisecond)
		atomic.AddInt32(&processed, 1)
		return nil
	}); err != nil {
		log.Fatalf("Failed to register analytics hook: %v", err)
	}

	fmt.Println("  Simulating analytics workload with combined resilience...")

	// Phase 1: Normal load
	fmt.Println("  Phase 1: Normal load")
	for i := 0; i < 10; i++ {
		order := Order{ID: fmt.Sprintf("NORMAL-%d", i), Amount: 50.0}
		if err := service.Emit(context.Background(), "analytics.track", order); err != nil {
			fmt.Printf("  Failed to emit analytics event: %v\n", err)
		}
	}

	// Phase 2: Traffic spike
	fmt.Println("  Phase 2: Traffic spike (triggers backpressure)")
	start := time.Now()
	var succeeded, failed int32

	for i := 0; i < 80; i++ {
		order := Order{
			ID:     fmt.Sprintf("SPIKE-%d", i),
			Amount: 75.0 + float64(i),
		}

		if err := service.Emit(context.Background(), "analytics.track", order); err != nil {
			atomic.AddInt32(&failed, 1)
		} else {
			atomic.AddInt32(&succeeded, 1)
		}
	}

	elapsed := time.Since(start)

	// Wait for processing
	time.Sleep(500 * time.Millisecond)

	fmt.Printf("  Results: %d succeeded, %d failed in %v\n", succeeded, failed, elapsed)
	fmt.Printf("  Processed: %d analytics events\n", atomic.LoadInt32(&processed))
	fmt.Printf("  Combined resilience: Backpressure smoothed spikes, overflow absorbed sustained load\n")
}

func demoRealWorld() {
	// E-commerce order processing with different resilience strategies per event type

	fmt.Println("  Setting up e-commerce order processing system...")

	// Critical payments: Prefer fast failure over delays
	paymentService := hookz.New[Order](
		hookz.WithWorkers(10),
		hookz.WithQueueSize(50),
		hookz.WithBackpressure(hookz.BackpressureConfig{
			MaxWait:        2 * time.Millisecond, // Very brief
			StartThreshold: 0.95,                 // Only under extreme load
			Strategy:       "fixed",              // Predictable timing
		}),
		// No overflow - prefer failure over delayed payments
	)
	defer paymentService.Close()

	// Non-critical notifications: Absorb everything
	notificationService := hookz.New[Order](
		hookz.WithWorkers(5),
		hookz.WithQueueSize(25),
		hookz.WithOverflow(hookz.OverflowConfig{
			Capacity:         500,
			DrainInterval:    10 * time.Millisecond,
			EvictionStrategy: "fifo", // Drop oldest notifications if needed
		}),
		// No backpressure - never block caller
	)
	defer notificationService.Close()

	// Analytics: Balanced approach
	analyticsService := hookz.New[Order](
		hookz.WithWorkers(8),
		hookz.WithQueueSize(40),
		hookz.WithBackpressure(hookz.BackpressureConfig{
			MaxWait:        10 * time.Millisecond,
			StartThreshold: 0.8,
			Strategy:       "linear",
		}),
		hookz.WithOverflow(hookz.OverflowConfig{
			Capacity:         200,
			DrainInterval:    5 * time.Millisecond,
			EvictionStrategy: "reject", // Don't lose analytics data
		}),
	)
	defer analyticsService.Close()

	// Register handlers
	var paymentCount, notificationCount, analyticsCount int32

	if _, err := paymentService.Hook("payment", func(ctx context.Context, order Order) error {
		time.Sleep(8 * time.Millisecond) // Payment processing
		atomic.AddInt32(&paymentCount, 1)
		return nil
	}); err != nil {
		log.Fatalf("Failed to register payment service hook: %v", err)
	}

	if _, err := notificationService.Hook("notification", func(ctx context.Context, order Order) error {
		time.Sleep(3 * time.Millisecond) // Fast notification
		atomic.AddInt32(&notificationCount, 1)
		return nil
	}); err != nil {
		log.Fatalf("Failed to register notification service hook: %v", err)
	}

	if _, err := analyticsService.Hook("analytics", func(ctx context.Context, order Order) error {
		time.Sleep(15 * time.Millisecond) // Complex analytics
		atomic.AddInt32(&analyticsCount, 1)
		return nil
	}); err != nil {
		log.Fatalf("Failed to register analytics service hook: %v", err)
	}

	// Simulate Black Friday traffic surge
	fmt.Println("  Simulating Black Friday traffic surge...")
	start := time.Now()

	var paymentErrors, notificationErrors, analyticsErrors int32

	for i := 0; i < 150; i++ {
		order := Order{
			ID:       fmt.Sprintf("BF-%04d", i+1),
			Amount:   199.99 + float64(i%100),
			Customer: fmt.Sprintf("BlackFridayCustomer-%d", i+1),
		}

		// Process payment (critical)
		if err := paymentService.Emit(context.Background(), "payment", order); err != nil {
			atomic.AddInt32(&paymentErrors, 1)
		}

		// Send notification (best effort)
		if err := notificationService.Emit(context.Background(), "notification", order); err != nil {
			atomic.AddInt32(&notificationErrors, 1)
		}

		// Track analytics (balanced)
		if err := analyticsService.Emit(context.Background(), "analytics", order); err != nil {
			atomic.AddInt32(&analyticsErrors, 1)
		}
	}

	processingTime := time.Since(start)

	// Wait for background processing
	time.Sleep(1 * time.Second)

	fmt.Printf("  Black Friday results (processing time: %v):\n", processingTime)
	fmt.Printf("    Payments: %d processed, %d errors\n",
		atomic.LoadInt32(&paymentCount), atomic.LoadInt32(&paymentErrors))
	fmt.Printf("    Notifications: %d sent, %d errors\n",
		atomic.LoadInt32(&notificationCount), atomic.LoadInt32(&notificationErrors))
	fmt.Printf("    Analytics: %d tracked, %d errors\n",
		atomic.LoadInt32(&analyticsCount), atomic.LoadInt32(&analyticsErrors))

	fmt.Println("\n  Key insights:")
	fmt.Println("    - Payments: Minimal backpressure, fast failure for reliability")
	fmt.Println("    - Notifications: Large overflow buffer, graceful degradation")
	fmt.Println("    - Analytics: Combined approach balances reliability and throughput")
	fmt.Println("    - Different resilience strategies per business requirement")
}

func init() {
	// Suppress log output for cleaner demo
	log.SetOutput(nil)
}

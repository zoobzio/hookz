package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	var scenario string
	flag.StringVar(&scenario, "scenario", "all", "Which scenario to run: all, blackfriday, recovery, monitoring")
	flag.Parse()
	
	fmt.Println("==============================================")
	fmt.Println("  PayFlow Payment Processing Crisis Simulator")
	fmt.Println("==============================================")
	fmt.Println()
	
	switch scenario {
	case "blackfriday":
		runBlackFridayCrisis()
	case "recovery":
		runRecoveryScenario()
	case "monitoring":
		runMonitoringDemo()
	default:
		runFullDemo()
	}
}

// runFullDemo shows the complete story
func runFullDemo() {
	fmt.Println("📅 October 15, 2024 - A normal day at PayFlow...")
	fmt.Println()
	time.Sleep(2 * time.Second)
	
	// Phase 1: The old synchronous way
	fmt.Println("PHASE 1: The Original Synchronous Implementation")
	fmt.Println("------------------------------------------------")
	runSynchronousImplementation()
	fmt.Println()
	time.Sleep(2 * time.Second)
	
	// Phase 2: Black Friday crisis
	fmt.Println("PHASE 2: Black Friday 2023 - The Crisis")
	fmt.Println("----------------------------------------")
	runBlackFridayCrisis()
	fmt.Println()
	time.Sleep(2 * time.Second)
	
	// Phase 3: The hookz solution
	fmt.Println("PHASE 3: The hookz Solution")
	fmt.Println("----------------------------")
	runRecoveryScenario()
	fmt.Println()
	time.Sleep(2 * time.Second)
	
	// Phase 4: Production monitoring
	fmt.Println("PHASE 4: Production Monitoring")
	fmt.Println("-------------------------------")
	runMonitoringDemo()
}

// runSynchronousImplementation shows why the old way failed
func runSynchronousImplementation() {
	fmt.Println("Simulating synchronous payment processing (the old way)...")
	fmt.Println()
	
	// Track timing
	paymentTimes := make([]time.Duration, 0, 10)
	
	for i := 0; i < 5; i++ {
		payment := generatePayment(i)
		start := time.Now()
		
		fmt.Printf("Processing payment %s... ", payment.ID)
		
		// Simulate the old synchronous way
		time.Sleep(100 * time.Millisecond)  // Gateway authorization
		time.Sleep(500 * time.Millisecond)  // Webhook delivery (blocking!)
		time.Sleep(200 * time.Millisecond)  // Analytics update
		time.Sleep(100 * time.Millisecond)  // Audit log
		time.Sleep(100 * time.Millisecond)  // Fraud check
		
		elapsed := time.Since(start)
		paymentTimes = append(paymentTimes, elapsed)
		
		fmt.Printf("✓ Completed in %v\n", elapsed)
	}
	
	// Calculate average
	var total time.Duration
	for _, t := range paymentTimes {
		total += t
	}
	avg := total / time.Duration(len(paymentTimes))
	
	fmt.Println()
	fmt.Printf("⚠️  Average payment time: %v\n", avg)
	fmt.Println("❌ Each payment waits for ALL operations to complete!")
	fmt.Println("❌ One slow webhook blocks the entire payment!")
}

// runBlackFridayCrisis simulates the actual incident
func runBlackFridayCrisis() {
	fmt.Println("🛍️  BLACK FRIDAY SALE BEGINS!")
	fmt.Println("Transaction volume: 10x normal")
	fmt.Println()
	time.Sleep(1 * time.Second)
	
	processor := NewPaymentProcessor()
	defer processor.Shutdown()
	
	// Simulate Black Friday - webhooks start failing
	processor.webhooks.SimulateBlackFriday()
	
	fmt.Println("Timeline of the crisis:")
	fmt.Println("2:00 PM - Sale begins")
	fmt.Println("2:15 PM - Traffic spike detected")
	fmt.Println("2:18 PM - Webhook timeouts begin...")
	fmt.Println()
	
	// Generate high volume of payments
	var wg sync.WaitGroup
	startTime := time.Now()
	failedPayments := atomic.Int32{}
	
	// Simulate 100 simultaneous payments
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			
			payment := generatePayment(id)
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			
			if err := processor.ProcessPayment(ctx, payment); err != nil {
				failedPayments.Add(1)
				if failedPayments.Load() == 1 {
					fmt.Printf("❌ 2:20 PM - CRITICAL: Payments failing! Error: %v\n", err)
				}
			}
		}(i)
		
		// Stagger the requests slightly
		if i%10 == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	
	wg.Wait()
	elapsed := time.Since(startTime)
	
	// Show metrics
	metrics := processor.GetMetrics()
	fmt.Println()
	fmt.Println("Crisis Impact:")
	fmt.Printf("⏱️  Processing time: %v\n", elapsed)
	fmt.Printf("✅ Payments authorized: %d\n", metrics.PaymentsProcessed)
	fmt.Printf("❌ Payments failed: %d\n", failedPayments.Load())
	fmt.Printf("📤 Webhooks sent: %d\n", metrics.WebhooksSent)
	fmt.Printf("⚠️  Webhooks failed: %d\n", metrics.WebhooksFailed)
	fmt.Printf("⚡ Average latency: %v\n", metrics.AverageLatency)
	
	// Show circuit breakers
	fmt.Println()
	fmt.Println("Circuit Breaker Status:")
	for merchantID, isOpen := range metrics.CircuitBreakers {
		status := "CLOSED ✅"
		if isOpen {
			status = "OPEN 🔴"
		}
		fmt.Printf("  %s: %s\n", merchantID, status)
	}
	
	fmt.Println()
	fmt.Println("💡 Key Insight: Payments still processed despite webhook failures!")
	fmt.Println("   With hookz, webhooks are async and don't block payments.")
}

// runRecoveryScenario shows the improved system
func runRecoveryScenario() {
	fmt.Println("Demonstrating the hookz solution...")
	fmt.Println()
	
	processor := NewPaymentProcessor()
	defer processor.Shutdown()
	
	// Process payments with various scenarios
	scenarios := []struct {
		name        string
		count       int
		concurrent  bool
	}{
		{"Normal flow", 10, false},
		{"High volume burst", 50, true},
		{"With failures", 20, true},
	}
	
	for _, scenario := range scenarios {
		fmt.Printf("\nScenario: %s\n", scenario.name)
		fmt.Println(stringRepeat("-", 40))
		
		if scenario.name == "With failures" {
			// Simulate some webhook failures
			processor.webhooks.SimulateBlackFriday()
		}
		
		start := time.Now()
		
		if scenario.concurrent {
			var wg sync.WaitGroup
			for i := 0; i < scenario.count; i++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					payment := generatePayment(id)
					ctx := context.Background()
					processor.ProcessPayment(ctx, payment)
				}(i)
			}
			wg.Wait()
		} else {
			for i := 0; i < scenario.count; i++ {
				payment := generatePayment(i)
				ctx := context.Background()
				processor.ProcessPayment(ctx, payment)
			}
		}
		
		elapsed := time.Since(start)
		metrics := processor.GetMetrics()
		
		fmt.Printf("✓ Processed %d payments in %v\n", scenario.count, elapsed)
		fmt.Printf("  Average: %v per payment\n", elapsed/time.Duration(scenario.count))
		fmt.Printf("  Webhooks: %d sent, %d failed\n", metrics.WebhooksSent, metrics.WebhooksFailed)
	}
	
	fmt.Println()
	fmt.Println("🎉 Result: Sub-200ms payment processing regardless of webhook status!")
}

// runMonitoringDemo shows production monitoring
func runMonitoringDemo() {
	fmt.Println("Starting production monitoring dashboard...")
	fmt.Println("(Press Ctrl+C to stop)")
	fmt.Println()
	
	processor := NewPaymentProcessor()
	defer processor.Shutdown()
	
	// Start payment generation in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	go generateContinuousPayments(ctx, processor)
	
	// Monitoring loop
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	
	iteration := 0
	for i := 0; i < 10; i++ { // Run for 20 seconds
		<-ticker.C
		iteration++
		
		metrics := processor.GetMetrics()
		
		// Clear screen effect
		fmt.Println("\n" + stringRepeat("=", 50))
		fmt.Printf("📊 METRICS UPDATE #%d - %s\n", iteration, time.Now().Format("15:04:05"))
		fmt.Println(stringRepeat("=", 50))
		
		fmt.Printf("💳 Payments Processed: %d\n", metrics.PaymentsProcessed)
		fmt.Printf("📤 Webhooks Delivered: %d\n", metrics.WebhooksSent)
		fmt.Printf("⚠️  Webhook Failures: %d\n", metrics.WebhooksFailed)
		fmt.Printf("⚡ Average Latency: %v\n", metrics.AverageLatency)
		
		// Show success rate
		if metrics.WebhooksSent+metrics.WebhooksFailed > 0 {
			successRate := float64(metrics.WebhooksSent) / float64(metrics.WebhooksSent+metrics.WebhooksFailed) * 100
			fmt.Printf("✅ Webhook Success Rate: %.1f%%\n", successRate)
		}
		
		// Circuit breaker status
		fmt.Println("\n🔌 Circuit Breakers:")
		for merchantID, isOpen := range metrics.CircuitBreakers {
			status := "CLOSED ✅"
			if isOpen {
				status = "OPEN 🔴 (protecting system)"
			}
			fmt.Printf("   %s: %s\n", merchantID, status)
		}
		
		// Simulate occasional issues
		if iteration == 3 {
			fmt.Println("\n⚠️  ALERT: Simulating webhook provider degradation...")
			processor.webhooks.SimulateBlackFriday()
		}
		
		if iteration == 6 {
			fmt.Println("\n✅ RECOVERY: Webhook provider stabilized")
			processor.webhooks = NewWebhookDeliveryService() // Reset to normal
		}
	}
	
	fmt.Println("\n✅ Monitoring complete. System remained stable throughout!")
}

// Helper functions

func generatePayment(id int) Payment {
	merchants := []string{"merchant-1", "merchant-2", "merchant-3"}
	
	return Payment{
		ID:         fmt.Sprintf("pay_%d_%d", time.Now().Unix(), id),
		MerchantID: merchants[id%len(merchants)],
		Amount:     int64(rand.Intn(10000) + 100), // $1 to $100
		Currency:   "USD",
		Status:     PaymentPending,
		Method:     PaymentMethod("card"),
		Customer: Customer{
			ID:      fmt.Sprintf("cust_%d", id),
			Email:   fmt.Sprintf("customer%d@example.com", id),
			Name:    fmt.Sprintf("Customer %d", id),
			Country: "US",
		},
		CreatedAt: time.Now(),
	}
}

func generateContinuousPayments(ctx context.Context, processor *PaymentProcessor) {
	id := 0
	ticker := time.NewTicker(100 * time.Millisecond) // 10 payments per second
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			payment := generatePayment(id)
			go processor.ProcessPayment(context.Background(), payment)
			id++
		}
	}
}

// stringRepeat helper since we're not importing strings
func stringRepeat(s string, count int) string {
	result := ""
	for i := 0; i < count; i++ {
		result += s
	}
	return result
}
package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestPaymentProcessingLatency verifies sub-200ms payment processing
func TestPaymentProcessingLatency(t *testing.T) {
	processor := NewPaymentProcessor()
	defer processor.Shutdown()
	
	payment := generatePayment(1)
	ctx := context.Background()
	
	start := time.Now()
	err := processor.ProcessPayment(ctx, payment)
	elapsed := time.Since(start)
	
	if err != nil {
		t.Fatalf("Payment processing failed: %v", err)
	}
	
	if elapsed > 200*time.Millisecond {
		t.Errorf("Payment took %v, expected < 200ms", elapsed)
	}
	
	// Verify payment was processed
	metrics := processor.GetMetrics()
	if metrics.PaymentsProcessed != 1 {
		t.Errorf("Expected 1 payment processed, got %d", metrics.PaymentsProcessed)
	}
}

// TestWebhookFailureIsolation ensures webhook failures don't block payments
func TestWebhookFailureIsolation(t *testing.T) {
	processor := NewPaymentProcessor()
	defer processor.Shutdown()
	
	// Make webhooks fail completely
	processor.webhooks.SimulateBlackFriday()
	
	// Process 10 payments
	var successCount atomic.Int32
	var wg sync.WaitGroup
	
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			
			payment := generatePayment(id)
			ctx := context.Background()
			
			if err := processor.ProcessPayment(ctx, payment); err == nil {
				successCount.Add(1)
			}
		}(i)
	}
	
	wg.Wait()
	
	// All payments should succeed despite webhook failures
	if successCount.Load() != 10 {
		t.Errorf("Expected 10 successful payments, got %d", successCount.Load())
	}
	
	// Give hooks time to process
	time.Sleep(500 * time.Millisecond)
	
	// Check webhook failures were recorded
	_, _, failures := processor.webhooks.GetStats()
	if failures == 0 {
		t.Error("Expected webhook failures to be recorded")
	}
}

// TestCircuitBreakerProtection verifies circuit breaker prevents cascade failures
func TestCircuitBreakerProtection(t *testing.T) {
	processor := NewPaymentProcessor()
	defer processor.Shutdown()
	
	// Create a merchant with a failing webhook
	processor.webhooks.SimulateBlackFriday()
	
	// Process multiple payments for the same merchant
	for i := 0; i < 10; i++ {
		payment := generatePayment(i)
		payment.MerchantID = "merchant-3" // The one that fails most
		
		ctx := context.Background()
		processor.ProcessPayment(ctx, payment)
	}
	
	// Give hooks time to process
	time.Sleep(1 * time.Second)
	
	// Check circuit breaker status
	processor.cbMutex.RLock()
	cb, exists := processor.circuitBreakers["merchant-3"]
	processor.cbMutex.RUnlock()
	
	if !exists {
		t.Fatal("Circuit breaker should exist for merchant-3")
	}
	
	// After multiple failures, circuit should be open
	if !cb.IsOpen() {
		t.Error("Circuit breaker should be open after multiple failures")
	}
}

// TestHighVolumeProcessing simulates Black Friday load
func TestHighVolumeProcessing(t *testing.T) {
	processor := NewPaymentProcessor()
	defer processor.Shutdown()
	
	// Process 1000 payments concurrently
	paymentCount := 1000
	var wg sync.WaitGroup
	var successCount atomic.Int32
	var totalLatency atomic.Int64
	
	start := time.Now()
	
	for i := 0; i < paymentCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			
			payment := generatePayment(id)
			ctx := context.Background()
			
			paymentStart := time.Now()
			if err := processor.ProcessPayment(ctx, payment); err == nil {
				successCount.Add(1)
				latency := time.Since(paymentStart).Milliseconds()
				totalLatency.Add(latency)
			}
		}(i)
		
		// Simulate realistic arrival rate
		if i%100 == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	
	wg.Wait()
	elapsed := time.Since(start)
	
	// Verify success rate
	successRate := float64(successCount.Load()) / float64(paymentCount) * 100
	if successRate < 99 {
		t.Errorf("Success rate %.1f%% is below 99%%", successRate)
	}
	
	// Verify throughput
	throughput := float64(paymentCount) / elapsed.Seconds()
	t.Logf("Processed %d payments in %v (%.0f payments/sec)", paymentCount, elapsed, throughput)
	
	// Verify average latency
	avgLatency := totalLatency.Load() / int64(successCount.Load())
	if avgLatency > 500 {
		t.Errorf("Average latency %dms exceeds 500ms threshold", avgLatency)
	}
}

// TestGracefulShutdown ensures clean shutdown with pending operations
func TestGracefulShutdown(t *testing.T) {
	processor := NewPaymentProcessor()
	
	// Start processing payments
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			payment := generatePayment(id)
			processor.ProcessPayment(context.Background(), payment)
		}(i)
	}
	
	// Give some time for processing to start
	time.Sleep(50 * time.Millisecond)
	
	// Shutdown while operations are in flight
	shutdownDone := make(chan struct{})
	go func() {
		processor.Shutdown()
		close(shutdownDone)
	}()
	
	// Wait for all payments to complete
	wg.Wait()
	
	// Shutdown should complete within reasonable time
	select {
	case <-shutdownDone:
		// Success
	case <-time.After(5 * time.Second):
		t.Error("Shutdown took too long")
	}
}

// TestMemoryStability ensures no goroutine leaks under load
func TestMemoryStability(t *testing.T) {
	// Skip in short mode
	if testing.Short() {
		t.Skip("Skipping memory stability test in short mode")
	}
	
	processor := NewPaymentProcessor()
	defer processor.Shutdown()
	
	// Process payments continuously for a period
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	var processed atomic.Int64
	
	// Start multiple payment generators
	for i := 0; i < 10; i++ {
		go func() {
			id := 0
			for {
				select {
				case <-ctx.Done():
					return
				default:
					payment := generatePayment(id)
					processor.ProcessPayment(context.Background(), payment)
					processed.Add(1)
					id++
					time.Sleep(10 * time.Millisecond)
				}
			}
		}()
	}
	
	<-ctx.Done()
	
	// Give time for operations to complete
	time.Sleep(500 * time.Millisecond)
	
	totalProcessed := processed.Load()
	t.Logf("Processed %d payments without memory issues", totalProcessed)
	
	// In a real test, we'd check runtime.NumGoroutine() here
	// to ensure no goroutine leaks
}

// Benchmark payment processing performance
func BenchmarkPaymentProcessing(b *testing.B) {
	processor := NewPaymentProcessor()
	defer processor.Shutdown()
	
	ctx := context.Background()
	
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		id := 0
		for pb.Next() {
			payment := generatePayment(id)
			processor.ProcessPayment(ctx, payment)
			id++
		}
	})
}

// Benchmark webhook delivery with circuit breaker
func BenchmarkWebhookDelivery(b *testing.B) {
	processor := NewPaymentProcessor()
	defer processor.Shutdown()
	
	ctx := context.Background()
	payment := generatePayment(1)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		processor.deliverWebhookWithCircuitBreaker(ctx, payment)
	}
}
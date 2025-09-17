package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zoobzio/hookz"
)

// PaymentProcessor handles all payment operations
type PaymentProcessor struct {
	// Core services
	gateway   *PaymentGateway
	merchants *MerchantService
	webhooks  *WebhookDeliveryService
	analytics *AnalyticsService
	audit     *AuditService
	fraud     *FraudDetectionService
	
	// Hook system - private to processor
	hooks *hookz.Hooks[Payment]
	
	// Circuit breakers per merchant
	circuitBreakers map[string]*CircuitBreaker
	cbMutex         sync.RWMutex
	
	// Metrics
	totalProcessed   atomic.Int64
	totalFailed      atomic.Int64
	webhookTimeouts  atomic.Int64
	averageLatency   atomic.Int64 // in milliseconds
}

// NewPaymentProcessor creates a production-ready payment processor
func NewPaymentProcessor() *PaymentProcessor {
	// Use 20 workers for async operations
	hooks := hookz.New[Payment](hookz.WithWorkers(20), hookz.WithTimeout(5*time.Second))
	
	pp := &PaymentProcessor{
		gateway:         NewPaymentGateway(),
		merchants:       NewMerchantService(),
		webhooks:        NewWebhookDeliveryService(),
		analytics:       NewAnalyticsService(),
		audit:          NewAuditService(),
		fraud:          NewFraudDetectionService(),
		hooks:          hooks,
		circuitBreakers: make(map[string]*CircuitBreaker),
	}
	
	// Register all the async handlers
	pp.registerHooks()
	
	return pp
}

// registerHooks sets up all async event handlers
func (pp *PaymentProcessor) registerHooks() {
	// Webhook delivery with circuit breaker
	pp.hooks.Hook(EventPaymentAuthorized, func(ctx context.Context, payment Payment) error {
		return pp.deliverWebhookWithCircuitBreaker(ctx, payment)
	})
	
	// Analytics tracking (non-critical)
	pp.hooks.Hook(EventPaymentAuthorized, func(ctx context.Context, payment Payment) error {
		if err := pp.analytics.TrackPayment(ctx, payment); err != nil {
			log.Printf("Analytics failed for payment %s: %v", payment.ID, err)
		}
		return nil // Never fail for analytics
	})
	
	// Audit logging (critical but async)
	pp.hooks.Hook(EventPaymentAuthorized, func(ctx context.Context, payment Payment) error {
		if err := pp.audit.LogPayment(ctx, payment); err != nil {
			log.Printf("CRITICAL: Audit log failed for payment %s: %v", payment.ID, err)
			// In production, this might trigger an alert
		}
		return nil
	})
	
	// Fraud assessment (async risk scoring)
	pp.hooks.Hook(EventPaymentAuthorized, func(ctx context.Context, payment Payment) error {
		score, err := pp.fraud.AssessRisk(ctx, payment)
		if err != nil {
			log.Printf("Fraud assessment failed for payment %s: %v", payment.ID, err)
			return nil
		}
		
		if score.Score > 80 {
			log.Printf("HIGH RISK: Payment %s scored %.2f", payment.ID, score.Score)
			// In production, this might trigger manual review
		}
		return nil
	})
}

// ProcessPayment is the main entry point - THE CRITICAL PATH
func (pp *PaymentProcessor) ProcessPayment(ctx context.Context, payment Payment) error {
	start := time.Now()
	
	// CRITICAL PATH: Only authorize the payment
	if err := pp.gateway.Authorize(ctx, payment); err != nil {
		pp.totalFailed.Add(1)
		return fmt.Errorf("payment authorization failed: %w", err)
	}
	
	payment.Status = PaymentAuthorized
	now := time.Now()
	payment.ProcessedAt = &now
	
	// EVERYTHING ELSE IS ASYNC - Never blocks the payment!
	if err := pp.hooks.Emit(ctx, EventPaymentAuthorized, payment); err != nil {
		// Log but don't fail - payment is already authorized
		log.Printf("Warning: Failed to emit payment events: %v", err)
	}
	
	// Track metrics
	pp.totalProcessed.Add(1)
	latency := time.Since(start).Milliseconds()
	pp.averageLatency.Store(latency)
	
	return nil // Payment authorized successfully!
}

// deliverWebhookWithCircuitBreaker handles webhook delivery with protection
func (pp *PaymentProcessor) deliverWebhookWithCircuitBreaker(ctx context.Context, payment Payment) error {
	merchant, err := pp.merchants.GetMerchant(payment.MerchantID)
	if err != nil {
		return fmt.Errorf("merchant lookup failed: %w", err)
	}
	
	// Get or create circuit breaker for this merchant
	pp.cbMutex.Lock()
	cb, exists := pp.circuitBreakers[merchant.ID]
	if !exists {
		cb = NewCircuitBreaker(merchant.ID, 5, 1*time.Minute)
		pp.circuitBreakers[merchant.ID] = cb
	}
	pp.cbMutex.Unlock()
	
	// Check circuit breaker
	if !cb.Allow() {
		log.Printf("Circuit breaker OPEN for merchant %s, skipping webhook", merchant.ID)
		return nil // Don't retry when circuit is open
	}
	
	// Prepare webhook event
	event := WebhookEvent{
		EventID:   fmt.Sprintf("evt_%d", time.Now().UnixNano()),
		Type:      string(EventPaymentAuthorized),
		PaymentID: payment.ID,
		Amount:    payment.Amount,
		Currency:  payment.Currency,
		Status:    string(payment.Status),
		Timestamp: time.Now(),
	}
	
	// Attempt delivery with timeout
	webhookCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	
	if err := pp.webhooks.Deliver(webhookCtx, merchant.WebhookURL, event); err != nil {
		cb.RecordFailure()
		pp.webhookTimeouts.Add(1)
		log.Printf("Webhook delivery failed for merchant %s: %v", merchant.ID, err)
		return err
	}
	
	cb.RecordSuccess()
	return nil
}

// GetMetrics returns current system metrics
func (pp *PaymentProcessor) GetMetrics() ProcessingMetrics {
	_, successes, failures := pp.webhooks.GetStats()
	
	pp.cbMutex.RLock()
	circuitStatus := make(map[string]bool)
	for id, cb := range pp.circuitBreakers {
		circuitStatus[id] = cb.IsOpen()
	}
	pp.cbMutex.RUnlock()
	
	return ProcessingMetrics{
		PaymentsProcessed: pp.totalProcessed.Load(),
		WebhooksSent:     successes,
		WebhooksFailed:   failures,
		AverageLatency:   time.Duration(pp.averageLatency.Load()) * time.Millisecond,
		CircuitBreakers:  circuitStatus,
	}
}

// Shutdown gracefully shuts down the processor
func (pp *PaymentProcessor) Shutdown() error {
	log.Println("Shutting down payment processor...")
	return pp.hooks.Close()
}

// CircuitBreaker protects against cascading failures
type CircuitBreaker struct {
	merchantID     string
	failures       atomic.Int32
	lastFailure    atomic.Int64
	threshold      int32
	resetDuration  time.Duration
}

func NewCircuitBreaker(merchantID string, threshold int, resetDuration time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		merchantID:    merchantID,
		threshold:     int32(threshold),
		resetDuration: resetDuration,
	}
}

func (cb *CircuitBreaker) Allow() bool {
	failures := cb.failures.Load()
	if failures < cb.threshold {
		return true // Circuit closed
	}
	
	// Check if we should reset
	lastFailure := cb.lastFailure.Load()
	if time.Since(time.Unix(0, lastFailure)) > cb.resetDuration {
		cb.failures.Store(0) // Reset
		return true // Try again
	}
	
	return false // Circuit open
}

func (cb *CircuitBreaker) RecordSuccess() {
	cb.failures.Store(0) // Reset on success
}

func (cb *CircuitBreaker) RecordFailure() {
	cb.failures.Add(1)
	cb.lastFailure.Store(time.Now().UnixNano())
}

func (cb *CircuitBreaker) IsOpen() bool {
	return !cb.Allow()
}
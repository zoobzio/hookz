package main

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// PaymentGateway simulates external payment provider
type PaymentGateway struct {
	latency       time.Duration
	failureRate   float64
	processedCount atomic.Int64
}

func NewPaymentGateway() *PaymentGateway {
	return &PaymentGateway{
		latency:     100 * time.Millisecond,
		failureRate: 0.01, // 1% failure rate
	}
}

func (pg *PaymentGateway) Authorize(ctx context.Context, payment Payment) error {
	// Simulate network latency
	select {
	case <-time.After(pg.latency):
		// Simulate random failures
		if rand.Float64() < pg.failureRate {
			return fmt.Errorf("gateway timeout")
		}
		pg.processedCount.Add(1)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (pg *PaymentGateway) GetProcessedCount() int64 {
	return pg.processedCount.Load()
}

// MerchantService manages merchant configurations
type MerchantService struct {
	merchants map[string]*Merchant
	mu        sync.RWMutex
}

func NewMerchantService() *MerchantService {
	return &MerchantService{
		merchants: map[string]*Merchant{
			"merchant-1": {
				ID:         "merchant-1",
				Name:       "TechGadgets Inc",
				WebhookURL: "https://api.techgadgets.com/webhook",
				Priority:   PriorityEnterprise,
				RateLimit:  100,
			},
			"merchant-2": {
				ID:         "merchant-2", 
				Name:       "Fashion Forward",
				WebhookURL: "https://fashionforward.com/payments",
				Priority:   PriorityPremium,
				RateLimit:  50,
			},
			"merchant-3": {
				ID:         "merchant-3",
				Name:       "Local Coffee Shop",
				WebhookURL: "https://localcoffee.square.site/webhook",
				Priority:   PriorityStandard,
				RateLimit:  10,
			},
		},
	}
}

func (ms *MerchantService) GetMerchant(id string) (*Merchant, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	
	merchant, ok := ms.merchants[id]
	if !ok {
		return nil, fmt.Errorf("merchant not found: %s", id)
	}
	return merchant, nil
}

// WebhookDeliveryService simulates webhook delivery with realistic failures
type WebhookDeliveryService struct {
	attempts      atomic.Int64
	successes     atomic.Int64
	failures      atomic.Int64
	latencyByURL  map[string]time.Duration
	failureRates  map[string]float64
	mu            sync.RWMutex
}

func NewWebhookDeliveryService() *WebhookDeliveryService {
	return &WebhookDeliveryService{
		latencyByURL: map[string]time.Duration{
			"https://api.techgadgets.com/webhook":       50 * time.Millisecond,
			"https://fashionforward.com/payments":       200 * time.Millisecond,
			"https://localcoffee.square.site/webhook":   500 * time.Millisecond, // Slow!
		},
		failureRates: map[string]float64{
			"https://api.techgadgets.com/webhook":       0.01, // 1% failure
			"https://fashionforward.com/payments":       0.05, // 5% failure
			"https://localcoffee.square.site/webhook":   0.20, // 20% failure!
		},
	}
}

func (w *WebhookDeliveryService) Deliver(ctx context.Context, url string, event WebhookEvent) error {
	w.attempts.Add(1)
	
	// Get latency for this URL
	w.mu.RLock()
	latency := w.latencyByURL[url]
	failureRate := w.failureRates[url]
	w.mu.RUnlock()
	
	if latency == 0 {
		latency = 100 * time.Millisecond // Default
	}
	
	// Simulate network call
	select {
	case <-time.After(latency):
		// Simulate failures
		if rand.Float64() < failureRate {
			w.failures.Add(1)
			return fmt.Errorf("webhook delivery failed: connection timeout")
		}
		w.successes.Add(1)
		return nil
	case <-ctx.Done():
		w.failures.Add(1)
		return ctx.Err()
	}
}

func (w *WebhookDeliveryService) GetStats() (attempts, successes, failures int64) {
	return w.attempts.Load(), w.successes.Load(), w.failures.Load()
}

// SimulateBlackFriday makes the local coffee shop webhook completely fail
func (w *WebhookDeliveryService) SimulateBlackFriday() {
	w.mu.Lock()
	defer w.mu.Unlock()
	// Local coffee shop can't handle the load!
	w.latencyByURL["https://localcoffee.square.site/webhook"] = 5 * time.Second
	w.failureRates["https://localcoffee.square.site/webhook"] = 0.90 // 90% failure!
}

// AnalyticsService tracks payment metrics
type AnalyticsService struct {
	events atomic.Int64
	errors atomic.Int64
}

func NewAnalyticsService() *AnalyticsService {
	return &AnalyticsService{}
}

func (a *AnalyticsService) TrackPayment(ctx context.Context, payment Payment) error {
	// Simulate database write
	select {
	case <-time.After(50 * time.Millisecond):
		a.events.Add(1)
		return nil
	case <-ctx.Done():
		a.errors.Add(1)
		return ctx.Err()
	}
}

func (a *AnalyticsService) GetEventCount() int64 {
	return a.events.Load()
}

// AuditService for compliance logging
type AuditService struct {
	logs   []AuditLog
	mu     sync.Mutex
	writes atomic.Int64
}

func NewAuditService() *AuditService {
	return &AuditService{
		logs: make([]AuditLog, 0, 10000),
	}
}

func (a *AuditService) LogPayment(ctx context.Context, payment Payment) error {
	// Simulate audit log write
	select {
	case <-time.After(20 * time.Millisecond):
		a.mu.Lock()
		a.logs = append(a.logs, AuditLog{
			PaymentID: payment.ID,
			Action:    "payment.authorized",
			UserID:    payment.Customer.ID,
			Timestamp: time.Now(),
			Details: map[string]interface{}{
				"amount":   payment.Amount,
				"currency": payment.Currency,
			},
		})
		a.mu.Unlock()
		a.writes.Add(1)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *AuditService) GetLogCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.logs)
}

// FraudDetectionService simulates risk assessment
type FraudDetectionService struct {
	assessments atomic.Int64
	highRisk    atomic.Int64
}

func NewFraudDetectionService() *FraudDetectionService {
	return &FraudDetectionService{}
}

func (f *FraudDetectionService) AssessRisk(ctx context.Context, payment Payment) (*RiskScore, error) {
	// Simulate ML model inference
	select {
	case <-time.After(100 * time.Millisecond):
		score := rand.Float64() * 100
		f.assessments.Add(1)
		
		if score > 80 {
			f.highRisk.Add(1)
		}
		
		return &RiskScore{
			PaymentID:  payment.ID,
			Score:      score,
			Flags:      []string{},
			AssessedAt: time.Now(),
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *FraudDetectionService) GetAssessmentCount() (total, highRisk int64) {
	return f.assessments.Load(), f.highRisk.Load()
}
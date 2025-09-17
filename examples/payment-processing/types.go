package main

import (
	"time"
)

// Payment represents a financial transaction
type Payment struct {
	ID         string
	MerchantID string
	Amount     int64 // cents
	Currency   string
	Status     PaymentStatus
	Method     PaymentMethod
	Customer   Customer
	Metadata   map[string]string
	CreatedAt  time.Time
	ProcessedAt *time.Time
}

// PaymentStatus represents the current state of a payment
type PaymentStatus string

const (
	PaymentPending    PaymentStatus = "pending"
	PaymentAuthorized PaymentStatus = "authorized"
	PaymentCaptured   PaymentStatus = "captured"
	PaymentFailed     PaymentStatus = "failed"
	PaymentRefunded   PaymentStatus = "refunded"
)

// PaymentMethod represents how the payment was made
type PaymentMethod string

const (
	MethodCard   PaymentMethod = "card"
	MethodBank   PaymentMethod = "bank"
	MethodWallet PaymentMethod = "wallet"
)

// Customer represents the person making the payment
type Customer struct {
	ID      string
	Email   string
	Name    string
	Country string
}

// Merchant represents a business receiving payments
type Merchant struct {
	ID          string
	Name        string
	WebhookURL  string
	Priority    MerchantPriority
	RateLimit   int // webhooks per minute
	CircuitOpen bool
}

// MerchantPriority determines processing priority
type MerchantPriority int

const (
	PriorityStandard MerchantPriority = iota
	PriorityPremium
	PriorityEnterprise
)

// WebhookEvent sent to merchants
type WebhookEvent struct {
	EventID   string    `json:"event_id"`
	Type      string    `json:"type"`
	PaymentID string    `json:"payment_id"`
	Amount    int64     `json:"amount"`
	Currency  string    `json:"currency"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

// ProcessingMetrics tracks system performance
type ProcessingMetrics struct {
	PaymentsProcessed int64
	WebhooksSent      int64
	WebhooksFailed    int64
	AverageLatency    time.Duration
	CircuitBreakers   map[string]bool
	WorkerPoolSize    int
	QueueDepth        int
}

// RiskScore represents fraud risk assessment
type RiskScore struct {
	PaymentID  string
	Score      float64 // 0-100, higher = riskier
	Flags      []string
	AssessedAt time.Time
}

// AuditLog for compliance tracking
type AuditLog struct {
	PaymentID string
	Action    string
	UserID    string
	Timestamp time.Time
	Details   map[string]interface{}
}
package main

import "github.com/zoobzio/hookz"

// Key is a type alias for hookz.Key for cleaner usage within the package
type Key = hookz.Key

// Payment lifecycle events following the EventPaymentAuthorized naming pattern
const (
	EventPaymentAuthorized Key = "payment.authorized"
	EventPaymentCaptured   Key = "payment.captured"
	EventPaymentFailed     Key = "payment.failed"
	EventPaymentRefunded   Key = "payment.refunded"
	EventPaymentVoided     Key = "payment.voided"
)

// Webhook delivery events
const (
	EventWebhookDelivered Key = "webhook.delivered"
	EventWebhookFailed    Key = "webhook.failed"
	EventWebhookRetrying  Key = "webhook.retrying"
)

// Merchant events
const (
	EventMerchantCreated   Key = "merchant.created"
	EventMerchantSuspended Key = "merchant.suspended"
	EventMerchantActivated Key = "merchant.activated"
)

// Security and fraud events
const (
	EventFraudDetected    Key = "fraud.detected"
	EventRiskAssessment   Key = "risk.assessment"
	EventSecurityAlert    Key = "security.alert"
)

// Analytics events
const (
	EventAnalyticsPayment Key = "analytics.payment"
	EventAnalyticsRefund  Key = "analytics.refund"
	EventAnalyticsRevenue Key = "analytics.revenue"
)
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zoobzio/hookz"
)

// Event keys for realworld patterns testing
const (
	webhookGithubPush hookz.Key = "webhook.github.push"
	cacheInvalidate   hookz.Key = "cache.invalidate"
	cacheUpdate       hookz.Key = "cache.update"
	externalAPI       hookz.Key = "external.api"
	apiRequest        hookz.Key = "api.request"
)

// WebhookEvent represents an incoming webhook
type WebhookEvent struct {
	ID        string                 `json:"id"`
	Source    string                 `json:"source"`
	Type      string                 `json:"type"`
	Timestamp time.Time              `json:"timestamp"`
	Payload   map[string]interface{} `json:"payload"`
}

// WebhookProcessor handles incoming webhooks with multiple subscribers
type WebhookProcessor struct {
	hooks *hookz.Hooks[WebhookEvent]
	stats struct {
		received  atomic.Int64
		processed atomic.Int64
		failed    atomic.Int64
	}
}

func NewWebhookProcessor() *WebhookProcessor {
	return &WebhookProcessor{
		// Use more workers for webhook processing
		hooks: hookz.New[WebhookEvent](hookz.WithWorkers(20)),
	}
}

func (w *WebhookProcessor) Subscribe() *hookz.Hooks[WebhookEvent] {
	return w.hooks
}

func (w *WebhookProcessor) ProcessWebhook(ctx context.Context, webhook WebhookEvent) error {
	w.stats.received.Add(1)

	// Route based on webhook source
	eventType := fmt.Sprintf("webhook.%s.%s", webhook.Source, webhook.Type)
	if err := w.hooks.Emit(ctx, eventType, webhook); err != nil {
		w.stats.failed.Add(1)
		return err
	}

	w.stats.processed.Add(1)
	return nil
}

func (w *WebhookProcessor) Shutdown() {
	w.hooks.Close()
}

func (w *WebhookProcessor) Stats() (received, processed, failed int64) {
	return w.stats.received.Load(), w.stats.processed.Load(), w.stats.failed.Load()
}

// TestWebhookIntegration simulates a realistic webhook processing system
func TestWebhookIntegration(t *testing.T) {
	processor := NewWebhookProcessor()
	defer processor.Shutdown()

	ctx := context.Background()

	// Simulate different webhook subscribers
	var slackNotifications atomic.Int32
	var emailsSent atomic.Int32
	var databaseWrites atomic.Int32
	var analyticsEvents atomic.Int32

	// Slack notification subscriber
	processor.Subscribe().Hook(webhookGithubPush, func(ctx context.Context, event WebhookEvent) error {
		// Simulate Slack API call
		time.Sleep(30 * time.Millisecond)
		slackNotifications.Add(1)
		return nil
	})

	// Email notification subscriber
	processor.Subscribe().Hook(webhookGithubPush, func(ctx context.Context, event WebhookEvent) error {
		// Only send email for main branch
		if branch, ok := event.Payload["branch"].(string); ok && branch == "main" {
			time.Sleep(50 * time.Millisecond) // Simulate SMTP
			emailsSent.Add(1)
		}
		return nil
	})

	// Database logger
	processor.Subscribe().Hook(webhookGithubPush, func(ctx context.Context, event WebhookEvent) error {
		// Simulate database write
		time.Sleep(20 * time.Millisecond)
		databaseWrites.Add(1)
		return nil
	})

	// Analytics tracker
	processor.Subscribe().Hook(webhookGithubPush, func(ctx context.Context, event WebhookEvent) error {
		// Simulate analytics API
		time.Sleep(10 * time.Millisecond)
		analyticsEvents.Add(1)
		return nil
	})

	// Simulate incoming webhooks
	branches := []string{"main", "develop", "feature/test", "main", "develop"}
	for i, branch := range branches {
		webhook := WebhookEvent{
			ID:        fmt.Sprintf("push-%d", i),
			Source:    "github",
			Type:      "push",
			Timestamp: time.Now(),
			Payload: map[string]interface{}{
				"branch":     branch,
				"commits":    i + 1,
				"repository": "test-repo",
			},
		}

		if err := processor.ProcessWebhook(ctx, webhook); err != nil {
			t.Errorf("Failed to process webhook: %v", err)
		}
	}

	// Wait for async processing
	time.Sleep(200 * time.Millisecond)

	// Verify all subscribers processed events
	if n := slackNotifications.Load(); n != 5 {
		t.Errorf("Expected 5 Slack notifications, got %d", n)
	}

	if n := emailsSent.Load(); n != 2 { // Only main branch
		t.Errorf("Expected 2 emails (main branch only), got %d", n)
	}

	if n := databaseWrites.Load(); n != 5 {
		t.Errorf("Expected 5 database writes, got %d", n)
	}

	if n := analyticsEvents.Load(); n != 5 {
		t.Errorf("Expected 5 analytics events, got %d", n)
	}

	// Check processor stats
	received, processed, failed := processor.Stats()
	if received != 5 {
		t.Errorf("Expected 5 webhooks received, got %d", received)
	}
	if processed != 5 {
		t.Errorf("Expected 5 webhooks processed, got %d", processed)
	}
	if failed != 0 {
		t.Errorf("Expected 0 failures, got %d", failed)
	}
}

// CacheEntry represents a cached item
type CacheEntry struct {
	Key       string
	Value     interface{}
	ExpiresAt time.Time
	Version   int
}

// CacheManager handles cache invalidation across services
type CacheManager struct {
	hooks  *hookz.Hooks[CacheEntry]
	caches map[string]*ServiceCache
	mu     sync.RWMutex
}

type ServiceCache struct {
	name    string
	entries sync.Map
	hits    atomic.Int64
	misses  atomic.Int64
}

func NewCacheManager() *CacheManager {
	return &CacheManager{
		hooks:  hookz.New[CacheEntry](hookz.WithWorkers(10)),
		caches: make(map[string]*ServiceCache),
	}
}

func (c *CacheManager) RegisterCache(name string) *ServiceCache {
	c.mu.Lock()
	defer c.mu.Unlock()

	cache := &ServiceCache{name: name}
	c.caches[name] = cache

	// Subscribe to invalidation events
	c.hooks.Hook(cacheInvalidate, func(ctx context.Context, entry CacheEntry) error {
		cache.entries.Delete(entry.Key)
		return nil
	})

	return cache
}

func (c *CacheManager) InvalidateKey(ctx context.Context, key string) error {
	return c.hooks.Emit(ctx, cacheInvalidate, CacheEntry{Key: key})
}

func (c *CacheManager) UpdateEntry(ctx context.Context, entry CacheEntry) error {
	// Emit update event that all caches will receive
	return c.hooks.Emit(ctx, cacheUpdate, entry)
}

func (c *CacheManager) Shutdown() {
	c.hooks.Close()
}

// TestDistributedCacheInvalidation simulates cache invalidation across services
func TestDistributedCacheInvalidation(t *testing.T) {
	manager := NewCacheManager()
	defer manager.Shutdown()

	ctx := context.Background()

	// Register multiple service caches
	apiCache := manager.RegisterCache("api")
	webCache := manager.RegisterCache("web")
	mobileCache := manager.RegisterCache("mobile")

	// Populate caches
	testData := map[string]interface{}{
		"user:123":    map[string]string{"name": "Alice", "role": "admin"},
		"product:456": map[string]string{"name": "Widget", "price": "$99"},
		"config:app":  map[string]string{"theme": "dark", "lang": "en"},
	}

	for key, value := range testData {
		apiCache.entries.Store(key, value)
		webCache.entries.Store(key, value)
		mobileCache.entries.Store(key, value)
	}

	// Verify all caches have the data
	for key := range testData {
		if _, ok := apiCache.entries.Load(key); !ok {
			t.Errorf("API cache missing key: %s", key)
		}
		if _, ok := webCache.entries.Load(key); !ok {
			t.Errorf("Web cache missing key: %s", key)
		}
		if _, ok := mobileCache.entries.Load(key); !ok {
			t.Errorf("Mobile cache missing key: %s", key)
		}
	}

	// Invalidate a key across all caches
	if err := manager.InvalidateKey(ctx, "user:123"); err != nil {
		t.Fatalf("Failed to invalidate key: %v", err)
	}

	// Wait for async invalidation
	time.Sleep(50 * time.Millisecond)

	// Verify key was removed from all caches
	if _, ok := apiCache.entries.Load("user:123"); ok {
		t.Error("API cache still has invalidated key")
	}
	if _, ok := webCache.entries.Load("user:123"); ok {
		t.Error("Web cache still has invalidated key")
	}
	if _, ok := mobileCache.entries.Load("user:123"); ok {
		t.Error("Mobile cache still has invalidated key")
	}

	// Other keys should still exist
	if _, ok := apiCache.entries.Load("product:456"); !ok {
		t.Error("API cache lost non-invalidated key")
	}
}

// MessageBus simulates a message bus with topic-based routing
type MessageBus struct {
	hooks *hookz.Hooks[Message]
	stats struct {
		published atomic.Int64
		delivered atomic.Int64
		dlq       atomic.Int64
	}
}

type Message struct {
	ID          string
	Topic       string
	Payload     json.RawMessage
	Metadata    map[string]string
	PublishedAt time.Time
	RetryCount  int
}

func NewMessageBus() *MessageBus {
	return &MessageBus{
		hooks: hookz.New[Message](hookz.WithWorkers(15), hookz.WithTimeout(5*time.Second)),
	}
}

func (m *MessageBus) Subscribe(topic string, handler func(context.Context, Message) error) (hookz.Hook, error) {
	return m.hooks.Hook(topic, handler)
}

func (m *MessageBus) Publish(ctx context.Context, topic string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	msg := Message{
		ID:          fmt.Sprintf("msg-%d", time.Now().UnixNano()),
		Topic:       topic,
		Payload:     data,
		PublishedAt: time.Now(),
	}

	m.stats.published.Add(1)

	if err := m.hooks.Emit(ctx, topic, msg); err != nil {
		m.stats.dlq.Add(1)
		return err
	}

	m.stats.delivered.Add(1)
	return nil
}

func (m *MessageBus) Shutdown() {
	m.hooks.Close()
}

// TestMessageBusRouting tests topic-based message routing
func TestMessageBusRouting(t *testing.T) {
	bus := NewMessageBus()
	defer bus.Shutdown()

	ctx := context.Background()

	// Track received messages by subscriber
	var orderServiceMessages atomic.Int32
	var inventoryServiceMessages atomic.Int32
	var shippingServiceMessages atomic.Int32
	var analyticsMessages atomic.Int32

	// Order service subscribes to order events
	_, _ = bus.Subscribe("order.created", func(ctx context.Context, msg Message) error {
		orderServiceMessages.Add(1)
		// Simulate order processing
		time.Sleep(20 * time.Millisecond)
		return nil
	})

	// Inventory service subscribes to order events
	_, _ = bus.Subscribe("order.created", func(ctx context.Context, msg Message) error {
		inventoryServiceMessages.Add(1)
		// Simulate inventory update
		time.Sleep(30 * time.Millisecond)
		return nil
	})

	// Shipping service subscribes to order events
	_, _ = bus.Subscribe("order.shipped", func(ctx context.Context, msg Message) error {
		shippingServiceMessages.Add(1)
		// Simulate shipping label creation
		time.Sleep(40 * time.Millisecond)
		return nil
	})

	// Analytics subscribes to all events
	_, _ = bus.Subscribe("order.created", func(ctx context.Context, msg Message) error {
		analyticsMessages.Add(1)
		return nil
	})
	_, _ = bus.Subscribe("order.shipped", func(ctx context.Context, msg Message) error {
		analyticsMessages.Add(1)
		return nil
	})

	// Publish various events
	orderPayload := map[string]interface{}{
		"orderID": "ORD-123",
		"amount":  99.99,
		"items":   3,
	}

	// Publish order created events
	for i := 0; i < 5; i++ {
		if err := bus.Publish(ctx, "order.created", orderPayload); err != nil {
			t.Errorf("Failed to publish order.created: %v", err)
		}
	}

	// Publish order shipped events
	for i := 0; i < 3; i++ {
		if err := bus.Publish(ctx, "order.shipped", orderPayload); err != nil {
			t.Errorf("Failed to publish order.shipped: %v", err)
		}
	}

	// Wait for async processing
	time.Sleep(200 * time.Millisecond)

	// Verify routing
	if n := orderServiceMessages.Load(); n != 5 {
		t.Errorf("Order service expected 5 messages, got %d", n)
	}

	if n := inventoryServiceMessages.Load(); n != 5 {
		t.Errorf("Inventory service expected 5 messages, got %d", n)
	}

	if n := shippingServiceMessages.Load(); n != 3 {
		t.Errorf("Shipping service expected 3 messages, got %d", n)
	}

	if n := analyticsMessages.Load(); n != 8 { // 5 created + 3 shipped
		t.Errorf("Analytics expected 8 messages, got %d", n)
	}
}

// TestCircuitBreakerPattern demonstrates circuit breaker with hooks
func TestCircuitBreakerPattern(t *testing.T) {
	processor := NewAsyncProcessor(5)
	defer processor.Shutdown()

	ctx := context.Background()

	// Circuit breaker state
	type CircuitBreaker struct {
		failures     atomic.Int32
		isOpen       atomic.Bool
		lastFailTime atomic.Int64
	}

	breaker := &CircuitBreaker{}
	var successCount atomic.Int32
	var rejectedCount atomic.Int32

	// Register hook with circuit breaker
	processor.Events().Hook(externalAPI, func(ctx context.Context, event AsyncEvent) error {
		// Check if circuit is open
		if breaker.isOpen.Load() {
			// Check if we should try to close
			lastFail := breaker.lastFailTime.Load()
			if time.Since(time.Unix(0, lastFail)) > 100*time.Millisecond {
				// Try to close circuit
				breaker.isOpen.Store(false)
				breaker.failures.Store(0)
			} else {
				rejectedCount.Add(1)
				return fmt.Errorf("circuit breaker open")
			}
		}

		// Simulate API call with 40% failure rate initially
		if event.Priority < 40 {
			// Failure
			failures := breaker.failures.Add(1)
			breaker.lastFailTime.Store(time.Now().UnixNano())

			if failures >= 3 {
				// Open circuit
				breaker.isOpen.Store(true)
			}

			return fmt.Errorf("api call failed")
		}

		// Success
		successCount.Add(1)
		breaker.failures.Store(0) // Reset on success
		return nil
	})

	// Send 20 requests with varying success rates
	for i := 0; i < 20; i++ {
		processor.Process(ctx, AsyncEvent{
			ID:       fmt.Sprintf("request-%d", i),
			Type:     string(externalAPI),
			Priority: i * 5, // Gradually increasing success rate
		})

		// Small delay between requests
		time.Sleep(20 * time.Millisecond)
	}

	// Wait for processing
	time.Sleep(200 * time.Millisecond)

	// Verify circuit breaker behavior
	if success := successCount.Load(); success == 0 {
		t.Error("No successful requests processed")
	}

	if rejected := rejectedCount.Load(); rejected == 0 {
		t.Error("Circuit breaker didn't reject any requests when open")
	}

	t.Logf("Circuit breaker stats: %d successful, %d rejected",
		successCount.Load(), rejectedCount.Load())
}

// TestRateLimitingPattern demonstrates rate limiting with hooks
func TestRateLimitingPattern(t *testing.T) {
	processor := NewAsyncProcessor(10)
	defer processor.Shutdown()

	ctx := context.Background()

	// Rate limiter state
	type RateLimiter struct {
		tokens    atomic.Int32
		lastReset atomic.Int64
	}

	limiter := &RateLimiter{}
	limiter.tokens.Store(5) // 5 requests per window

	var processedCount atomic.Int32
	var rateLimitedCount atomic.Int32

	// Register hook with rate limiting
	processor.Events().Hook(apiRequest, func(ctx context.Context, event AsyncEvent) error {
		// Check and reset token bucket
		now := time.Now().UnixNano()
		lastReset := limiter.lastReset.Load()
		if time.Duration(now-lastReset) > 100*time.Millisecond {
			// Reset bucket
			limiter.tokens.Store(5)
			limiter.lastReset.Store(now)
		}

		// Try to acquire token
		tokens := limiter.tokens.Add(-1)
		if tokens < 0 {
			// No tokens available
			limiter.tokens.Add(1) // Return token
			rateLimitedCount.Add(1)
			return fmt.Errorf("rate limit exceeded")
		}

		// Process request
		time.Sleep(10 * time.Millisecond)
		processedCount.Add(1)
		return nil
	})

	// Send burst of requests
	for i := 0; i < 15; i++ {
		processor.Process(ctx, AsyncEvent{
			ID:   fmt.Sprintf("req-%d", i),
			Type: string(apiRequest),
		})
	}

	// Wait for first window
	time.Sleep(120 * time.Millisecond)

	// Send more requests (should get new tokens)
	for i := 15; i < 20; i++ {
		processor.Process(ctx, AsyncEvent{
			ID:   fmt.Sprintf("req-%d", i),
			Type: string(apiRequest),
		})
	}

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	// Verify rate limiting worked
	if processed := processedCount.Load(); processed > 10 {
		t.Errorf("Processed too many requests: %d (should be <= 10)", processed)
	}

	if limited := rateLimitedCount.Load(); limited < 10 {
		t.Errorf("Not enough requests were rate limited: %d", limited)
	}

	t.Logf("Rate limiting stats: %d processed, %d rate limited",
		processedCount.Load(), rateLimitedCount.Load())
}

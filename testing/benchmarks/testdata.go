package benchmarks

import (
	"math/rand"
	"time"
)

// TestEvent represents realistic event data for benchmarking
type TestEvent struct {
	ID        int
	Timestamp int64
	Payload   []byte
	Type      string
}

// generateRealisticEvents creates events with realistic size distribution
func generateRealisticEvents(n int) []TestEvent {
	events := make([]TestEvent, n)
	types := []string{"user.action", "order.created", "payment.processed", "system.alert"}
	
	for i := range events {
		events[i] = TestEvent{
			ID:        i,
			Timestamp: time.Now().UnixNano(),
			Type:      types[rand.Intn(len(types))],
			Payload:   make([]byte, 256+rand.Intn(768)), // 256-1024 bytes, realistic distribution
		}
		// Fill payload with data to prevent compiler optimization
		for j := range events[i].Payload {
			events[i].Payload[j] = byte(i + j)
		}
	}
	return events
}

// generateFixedSizeEvents creates events with specific payload size
func generateFixedSizeEvents(n, size int) []TestEvent {
	events := make([]TestEvent, n)
	
	for i := range events {
		events[i] = TestEvent{
			ID:        i,
			Timestamp: time.Now().UnixNano(),
			Type:      "benchmark.event",
			Payload:   make([]byte, size),
		}
		// Fill payload to prevent optimization
		for j := range events[i].Payload {
			events[i].Payload[j] = byte(i + j)
		}
	}
	return events
}
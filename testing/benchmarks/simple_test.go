package benchmarks

import (
	"context"
	"testing"

	"github.com/zoobzio/hookz"
)

// Simple test to verify benchmark compilation and basic functionality
func TestBenchmarkSetup(t *testing.T) {
	service := hookz.New[TestEvent](
		hookz.WithWorkers(5),
		hookz.WithQueueSize(10),
	)
	defer service.Close()

	// Register a simple hook
	_, err := service.Hook("test.event", func(ctx context.Context, evt TestEvent) error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Generate test events
	events := generateRealisticEvents(10)
	if len(events) != 10 {
		t.Errorf("Expected 10 events, got %d", len(events))
	}

	// Emit events
	for _, event := range events {
		err := service.Emit(context.Background(), "test.event", event)
		if err != nil {
			t.Fatal(err)
		}
	}

	t.Log("Benchmark setup test passed")
}
package benchmarks

import (
	"context"
	"testing"
	"time"

	. "github.com/zoobzio/hookz"
)

// BenchmarkResilienceOverhead measures performance impact of resilience features
func BenchmarkResilienceOverhead(b *testing.B) {
	b.Run("Default", func(b *testing.B) {
		service := New[int](WithWorkers(10), WithQueueSize(100))
		defer service.Close()

		hook, err := service.Hook("test", func(ctx context.Context, v int) error { return nil })
		if err != nil {
			b.Fatalf("failed to register hook: %v", err)
		}
		defer hook.Unhook()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			service.Emit(context.Background(), "test", i)
		}
	})

	b.Run("WithBackpressure", func(b *testing.B) {
		service := New[int](
			WithWorkers(10),
			WithQueueSize(100),
			WithBackpressure(BackpressureConfig{
				MaxWait:        1 * time.Millisecond,
				StartThreshold: 0.9,
				Strategy:       "linear",
			}),
		)
		defer service.Close()

		hook, err := service.Hook("test", func(ctx context.Context, v int) error { return nil })
		if err != nil {
			b.Fatalf("failed to register hook: %v", err)
		}
		defer hook.Unhook()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			service.Emit(context.Background(), "test", i)
		}
	})

	b.Run("WithOverflow", func(b *testing.B) {
		service := New[int](
			WithWorkers(10),
			WithQueueSize(100),
			WithOverflow(OverflowConfig{
				Capacity:         1000,
				DrainInterval:    1 * time.Millisecond,
				EvictionStrategy: "fifo",
			}),
		)
		defer service.Close()

		hook, err := service.Hook("test", func(ctx context.Context, v int) error { return nil })
		if err != nil {
			b.Fatalf("failed to register hook: %v", err)
		}
		defer hook.Unhook()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			service.Emit(context.Background(), "test", i)
		}
	})

	b.Run("WithBoth", func(b *testing.B) {
		service := New[int](
			WithWorkers(10),
			WithQueueSize(100),
			WithBackpressure(BackpressureConfig{
				MaxWait:        1 * time.Millisecond,
				StartThreshold: 0.9,
				Strategy:       "linear",
			}),
			WithOverflow(OverflowConfig{
				Capacity:         1000,
				DrainInterval:    1 * time.Millisecond,
				EvictionStrategy: "fifo",
			}),
		)
		defer service.Close()

		hook, err := service.Hook("test", func(ctx context.Context, v int) error { return nil })
		if err != nil {
			b.Fatalf("failed to register hook: %v", err)
		}
		defer hook.Unhook()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			service.Emit(context.Background(), "test", i)
		}
	})
}
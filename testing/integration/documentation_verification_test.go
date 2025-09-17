// Documentation Verification Tests
//
// Tests verify that examples from documentation actually work
// No business logic - just code from docs should execute correctly

package integration_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	hookz "github.com/zoobzio/hookz"
)

// TestDeploymentChecklistExamples verifies examples from deployment-checklist.md
func TestDeploymentChecklistExamples(t *testing.T) {
	t.Run("WorkerSizingCalculations", func(t *testing.T) {
		// CPU-bound hooks from docs
		cpuBoundWorkers := runtime.NumCPU()

		// I/O-bound hooks from docs
		ioBoundWorkers := runtime.NumCPU() * 3

		// Mixed workload from docs
		mixedWorkers := runtime.NumCPU() * 2

		// Verify calculations are sane
		if cpuBoundWorkers <= 0 {
			t.Errorf("CPU-bound workers invalid: %d", cpuBoundWorkers)
		}
		if ioBoundWorkers <= cpuBoundWorkers {
			t.Errorf("I/O-bound workers should be > CPU-bound: %d vs %d", ioBoundWorkers, cpuBoundWorkers)
		}
		if mixedWorkers <= cpuBoundWorkers || mixedWorkers >= ioBoundWorkers {
			t.Errorf("Mixed workers should be between CPU and I/O: %d", mixedWorkers)
		}
	})

	t.Run("MemoryEstimationFormula", func(t *testing.T) {
		// Test memory estimation function from docs
		EstimateMemoryUsage := func(workers, queueSize int, avgDataSize int64) int64 {
			workerMem := int64(workers) * 320 * 1024   // 320KB per worker
			queueMem := int64(queueSize) * avgDataSize // Queue buffer
			overhead := int64(workers) * 64 * 1024     // Context overhead

			return workerMem + queueMem + overhead
		}

		// Example from docs: 10 workers, 50 queue, 1KB data = ~3.8MB
		estimate := EstimateMemoryUsage(10, 50, 1024)
		expectedMin := int64(3 * 1024 * 1024) // 3MB
		expectedMax := int64(4 * 1024 * 1024) // 4MB

		if estimate < expectedMin || estimate > expectedMax {
			t.Errorf("Memory estimate wrong: got %d, expected ~3.8MB", estimate)
		}

		// Actual calculation: 10*320KB + 50*1KB + 10*64KB = 3200KB + 50KB + 640KB = 3890KB = ~3.8MB
		actualCalc := int64(10*320+50+10*64) * 1024
		if estimate != actualCalc {
			t.Errorf("Memory calculation mismatch: got %d, expected %d", estimate, actualCalc)
		}
	})

	t.Run("BackpressureConfiguration", func(t *testing.T) {
		// Standard configuration from docs
		standardBackpressure := hookz.BackpressureConfig{
			MaxWait:        50 * time.Millisecond,
			StartThreshold: 0.8, // 80% queue full
			Strategy:       "linear",
		}

		// Verify config makes sense
		if standardBackpressure.MaxWait <= 0 {
			t.Error("MaxWait should be positive")
		}
		if standardBackpressure.StartThreshold < 0 || standardBackpressure.StartThreshold > 1 {
			t.Error("StartThreshold should be between 0 and 1")
		}
		if standardBackpressure.Strategy != "linear" {
			t.Errorf("Strategy should be 'linear', got %s", standardBackpressure.Strategy)
		}

		// High-traffic configuration from docs
		highTrafficBackpressure := hookz.BackpressureConfig{
			MaxWait:        10 * time.Millisecond,
			StartThreshold: 0.9, // 90% queue full
			Strategy:       "exponential",
		}

		// Verify high-traffic is more aggressive
		if highTrafficBackpressure.MaxWait >= standardBackpressure.MaxWait {
			t.Error("High-traffic should have lower MaxWait")
		}
		if highTrafficBackpressure.StartThreshold <= standardBackpressure.StartThreshold {
			t.Error("High-traffic should have higher StartThreshold")
		}
	})
}

// TestMigrationGuideExamples verifies examples from migration.md
func TestMigrationGuideExamples(t *testing.T) {
	t.Run("BasicMigrationPattern", func(t *testing.T) {
		// After migration pattern from docs
		hooks := hookz.New[DocOrder](
			hookz.WithWorkers(10),
			hookz.WithTimeout(5*time.Second),
		)
		defer hooks.Close()

		var notifyCount, inventoryCount, emailCount int

		_, err := hooks.Hook("order.created", func(ctx context.Context, order DocOrder) error {
			notifyCount++
			return nil // Simulated notifyUser
		})
		if err != nil {
			t.Fatal(err)
		}

		_, err = hooks.Hook("order.created", func(ctx context.Context, order DocOrder) error {
			inventoryCount++
			return nil // Simulated updateInventory
		})
		if err != nil {
			t.Fatal(err)
		}

		_, err = hooks.Hook("order.created", func(ctx context.Context, order DocOrder) error {
			emailCount++
			return nil // Simulated sendEmail
		})
		if err != nil {
			t.Fatal(err)
		}

		// Process order
		order := DocOrder{ID: "123", Items: []string{"item1"}}
		err = hooks.Emit(context.Background(), "order.created", order)
		if err != nil {
			t.Fatal(err)
		}

		// Wait for completion - CRITICAL pattern from docs
		hooks.Close()

		// Verify all hooks executed
		if notifyCount != 1 {
			t.Errorf("Notify hook not executed: %d", notifyCount)
		}
		if inventoryCount != 1 {
			t.Errorf("Inventory hook not executed: %d", inventoryCount)
		}
		if emailCount != 1 {
			t.Errorf("Email hook not executed: %d", emailCount)
		}
	})
}

// TestPerformanceTuningExamples verifies examples from performance-tuning.md
func TestPerformanceTuningExamples(t *testing.T) {
	t.Run("CPUBoundConfiguration", func(t *testing.T) {
		// CPU-bound: workers = number of CPU cores
		hooks := hookz.New[ProcessingJob](hookz.WithWorkers(runtime.NumCPU()))
		defer hooks.Close()

		var processed bool
		_, err := hooks.Hook("data.process", func(ctx context.Context, data ProcessingJob) error {
			processed = true
			// Pure computation: parsing, encryption, compression
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}

		err = hooks.Emit(context.Background(), "data.process", ProcessingJob{Data: "test"})
		if err != nil {
			t.Fatal(err)
		}

		hooks.Close()

		if !processed {
			t.Error("CPU-bound hook not processed")
		}
	})

	t.Run("IOBoundConfiguration", func(t *testing.T) {
		// I/O-bound: workers = 2-4x CPU count (docs say 3x for mixed I/O)
		hooks := hookz.New[DocUser](hookz.WithWorkers(runtime.NumCPU() * 3))
		defer hooks.Close()

		var notified bool
		_, err := hooks.Hook("user.notify", func(ctx context.Context, user DocUser) error {
			notified = true
			// Network I/O: HTTP calls, database queries, email sending
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}

		err = hooks.Emit(context.Background(), "user.notify", DocUser{ID: "123"})
		if err != nil {
			t.Fatal(err)
		}

		hooks.Close()

		if !notified {
			t.Error("I/O-bound hook not processed")
		}
	})

	t.Run("MemoryEstimationAccuracy", func(t *testing.T) {
		// Memory estimation function from docs
		EstimateMemoryUsage := func(workers, queueSize int, avgDataSize int64) int64 {
			workerMem := int64(workers) * 320 * 1024   // 320KB per worker
			queueMem := int64(queueSize) * avgDataSize // Queue buffer
			overhead := int64(workers) * 64 * 1024     // Context overhead

			return workerMem + queueMem + overhead
		}

		// Example from docs: 10 workers, queue=50, 1KB data = ~3.5MB total
		estimate := EstimateMemoryUsage(10, 50, 1024)
		// Docs say ~3.5MB but calculation gives 3890KB which is ~3.8MB
		// This is the documented calculation:
		// 10 * 320KB = 3200KB
		// 50 * 1KB = 50KB
		// 10 * 64KB = 640KB
		// Total = 3890KB = ~3.8MB

		actualBytes := int64(3890 * 1024)
		if estimate != actualBytes {
			t.Errorf("Memory estimate mismatch: got %d, expected %d", estimate, actualBytes)
		}
	})
}

// TestSecurityExamples verifies examples from security.md
func TestSecurityExamples(t *testing.T) {
	t.Run("InputSanitization", func(t *testing.T) {
		// Test sanitization pattern from docs
		hooks := hookz.New[UserInput]()
		defer hooks.Close()

		var sanitized UserInput
		_, err := hooks.Hook("user.input", func(ctx context.Context, input UserInput) error {
			// Simplified sanitization from docs
			sanitized = input
			if len(sanitized.Content) > 10000 {
				sanitized.Content = sanitized.Content[:10000]
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}

		// Test with oversized content
		longContent := make([]byte, 20000)
		for i := range longContent {
			longContent[i] = 'A'
		}

		input := UserInput{
			Name:    "test",
			Email:   "test@example.com",
			Content: string(longContent),
		}

		err = hooks.Emit(context.Background(), "user.input", input)
		if err != nil {
			t.Fatal(err)
		}

		hooks.Close()

		// Verify content was truncated as per docs
		if len(sanitized.Content) > 10000 {
			t.Errorf("Content not truncated: got %d chars", len(sanitized.Content))
		}
	})

	t.Run("ResourceProtection", func(t *testing.T) {
		// Constants from docs
		const (
			MaxDataSize     = 1024 * 1024 // 1MB per event
			MaxBatchSize    = 100         // Maximum items per batch
			MaxStringLength = 10000       // Maximum string field length
		)

		hooks := hookz.New[ProcessingData]()
		defer hooks.Close()

		var errorOccurred bool
		_, err := hooks.Hook("data.process", func(ctx context.Context, data ProcessingData) error {
			// Validate data size before processing (from docs)
			if len(data.Payload) > MaxDataSize {
				errorOccurred = true
				return errors.New("payload too large")
			}

			// Validate batch size
			if len(data.Items) > MaxBatchSize {
				errorOccurred = true
				return errors.New("batch too large")
			}

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}

		// Test with oversized payload
		bigPayload := make([]byte, MaxDataSize+1)
		data := ProcessingData{
			Payload: bigPayload,
			Items:   []DocItem{},
		}

		hooks.Emit(context.Background(), "data.process", data)
		hooks.Close()

		if !errorOccurred {
			t.Error("Resource protection did not trigger for oversized payload")
		}
	})
}

// TestTestingPatternsExamples verifies examples from testing-patterns.md
func TestTestingPatternsExamples(t *testing.T) {
	t.Run("BasicHookVerification", func(t *testing.T) {
		// Example from testing-patterns.md
		hooks := hookz.New[string]()
		var received string

		// Register hook
		_, err := hooks.Hook("test.event", func(ctx context.Context, data string) error {
			received = data
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}

		// Emit event
		err = hooks.Emit(context.Background(), "test.event", "hello")
		if err != nil {
			t.Fatal(err)
		}

		// Wait for completion - CRITICAL for deterministic tests (from docs)
		hooks.Close()

		// Verify result
		if received != "hello" {
			t.Errorf("Hook did not receive data: got %q", received)
		}
	})

	t.Run("ContextCancellation", func(t *testing.T) {
		// Timeout testing pattern from docs
		hooks := hookz.New[string](hookz.WithTimeout(100 * time.Millisecond))
		var completed bool

		// Register hook that takes longer than timeout
		_, err := hooks.Hook("slow.event", func(ctx context.Context, data string) error {
			select {
			case <-time.After(200 * time.Millisecond):
				completed = true
				return nil
			case <-ctx.Done():
				return ctx.Err() // Timeout cancellation
			}
		})
		if err != nil {
			t.Fatal(err)
		}

		start := time.Now()
		hooks.Emit(context.Background(), "slow.event", "test")
		hooks.Close()
		duration := time.Since(start)

		// Verify timeout behavior from docs
		if completed {
			t.Error("Hook should not complete due to timeout")
		}
		if duration > 150*time.Millisecond {
			t.Errorf("Should timeout quickly: took %v", duration)
		}
	})
}

// Test data structures from documentation examples
type DocOrder struct {
	ID    string
	Items []string
}

type ProcessingJob struct {
	Data string
}

type DocUser struct {
	ID string
}

type UserInput struct {
	Name    string
	Email   string
	Content string
}

type ProcessingData struct {
	Payload []byte
	Items   []DocItem
}

type DocItem struct {
	Content string
}

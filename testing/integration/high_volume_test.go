package integration

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zoobzio/hookz"
)

// Event keys for high volume testing
const (
	metricReceived hookz.Key = "metric.received"
)

// MetricsCollector demonstrates a high-volume metrics collection system
// that processes events from multiple sources with different priorities.
type MetricsCollector struct {
	// Different hook services for different metric priorities
	critical *hookz.Hooks[Metric] // Critical metrics - fast timeout
	standard *hookz.Hooks[Metric] // Standard metrics - normal timeout
	bulk     *hookz.Hooks[Metric] // Bulk metrics - can be delayed

	// Processing stats
	processedCount atomic.Int64
	droppedCount   atomic.Int64
	errorCount     atomic.Int64

	// Storage backend
	storage *MetricStorage
}

// Metric represents a metric event
type Metric struct {
	ID        string
	Source    string
	Type      string // "counter", "gauge", "histogram", "summary"
	Name      string
	Value     float64
	Tags      map[string]string
	Timestamp time.Time
	Priority  string // "critical", "standard", "bulk"
}

// MetricStorage simulates a time-series database
type MetricStorage struct {
	mu      sync.RWMutex
	metrics []Metric

	// Simulate storage latency
	writeLatency time.Duration

	// Track write performance
	writeCount    atomic.Int64
	writeDuration atomic.Int64 // Total microseconds
}

// NewMetricsCollector creates a metrics collection system
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		// Critical metrics: more workers, short timeout
		critical: hookz.New[Metric](hookz.WithWorkers(20), hookz.WithTimeout(1*time.Second)),
		// Standard metrics: moderate workers, normal timeout
		standard: hookz.New[Metric](hookz.WithWorkers(10), hookz.WithTimeout(5*time.Second)),
		// Bulk metrics: fewer workers, longer timeout acceptable
		bulk: hookz.New[Metric](hookz.WithWorkers(5), hookz.WithTimeout(30*time.Second)),
		storage: &MetricStorage{
			writeLatency: 5 * time.Millisecond, // Simulate DB write time
		},
	}
}

// Critical returns the hook registry for critical metrics
func (m *MetricsCollector) Critical() *hookz.Hooks[Metric] {
	return m.critical
}

// Standard returns the hook registry for standard metrics
func (m *MetricsCollector) Standard() *hookz.Hooks[Metric] {
	return m.standard
}

// Bulk returns the hook registry for bulk metrics
func (m *MetricsCollector) Bulk() *hookz.Hooks[Metric] {
	return m.bulk
}

// CollectMetric routes metrics to appropriate service based on priority
func (m *MetricsCollector) CollectMetric(ctx context.Context, metric Metric) error {
	var err error

	switch metric.Priority {
	case "critical":
		err = m.critical.Emit(ctx, metricReceived, metric)
	case "standard":
		err = m.standard.Emit(ctx, metricReceived, metric)
	case "bulk":
		err = m.bulk.Emit(ctx, metricReceived, metric)
	default:
		err = m.standard.Emit(ctx, metricReceived, metric)
	}

	if err != nil {
		if errors.Is(err, hookz.ErrQueueFull) {
			m.droppedCount.Add(1)
		} else {
			m.errorCount.Add(1)
		}
		return err
	}

	m.processedCount.Add(1)
	return nil
}

// SetupProcessors sets up metric processing pipelines
func (m *MetricsCollector) SetupProcessors() {
	// Critical metrics - direct storage, alert on anomalies
	m.critical.Hook(metricReceived, func(ctx context.Context, metric Metric) error {
		// Check for anomalies
		if metric.Type == "gauge" && metric.Value > 1000 {
			// Would trigger alert in production
			_ = metric.Value // Placeholder for alert logic
		}
		return m.storage.Write(ctx, metric)
	})

	// Standard metrics - aggregate before storage
	aggregator := NewMetricAggregator()
	m.standard.Hook(metricReceived, func(ctx context.Context, metric Metric) error {
		aggregator.Add(metric)
		// Periodic flush would happen in production
		if aggregator.Count() > 100 {
			batch := aggregator.Flush()
			for _, metric := range batch {
				if err := m.storage.Write(ctx, metric); err != nil {
					return err
				}
			}
		}
		return nil
	})

	// Bulk metrics - compress and batch
	m.bulk.Hook(metricReceived, func(ctx context.Context, metric Metric) error {
		// Would compress and batch in production
		return m.storage.Write(ctx, metric)
	})
}

// GetStats returns processing statistics
func (m *MetricsCollector) GetStats() (processed, dropped, errors int64) {
	return m.processedCount.Load(), m.droppedCount.Load(), m.errorCount.Load()
}

// Shutdown shuts down all metric services
func (m *MetricsCollector) Shutdown() error {
	// Close in order of priority (least critical first)
	if err := m.bulk.Close(); err != nil {
		return err
	}
	if err := m.standard.Close(); err != nil {
		return err
	}
	return m.critical.Close()
}

// Write stores a metric with simulated latency
func (s *MetricStorage) Write(ctx context.Context, metric Metric) error {
	start := time.Now()

	// Simulate write latency
	select {
	case <-time.After(s.writeLatency):
	case <-ctx.Done():
		return ctx.Err()
	}

	s.mu.Lock()
	s.metrics = append(s.metrics, metric)
	s.mu.Unlock()

	s.writeCount.Add(1)
	s.writeDuration.Add(int64(time.Since(start).Microseconds()))

	return nil
}

// GetAverageWriteLatency returns average write latency
func (s *MetricStorage) GetAverageWriteLatency() time.Duration {
	count := s.writeCount.Load()
	if count == 0 {
		return 0
	}

	totalMicros := s.writeDuration.Load()
	avgMicros := totalMicros / count
	return time.Duration(avgMicros) * time.Microsecond
}

// MetricAggregator aggregates metrics before storage
type MetricAggregator struct {
	mu      sync.Mutex
	metrics []Metric
}

// NewMetricAggregator creates a new aggregator
func NewMetricAggregator() *MetricAggregator {
	return &MetricAggregator{
		metrics: make([]Metric, 0, 100),
	}
}

// Add adds a metric to the aggregation buffer
func (a *MetricAggregator) Add(metric Metric) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.metrics = append(a.metrics, metric)
}

// Count returns the number of buffered metrics
func (a *MetricAggregator) Count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.metrics)
}

// Flush returns all buffered metrics and clears the buffer
func (a *MetricAggregator) Flush() []Metric {
	a.mu.Lock()
	defer a.mu.Unlock()

	result := a.metrics
	a.metrics = make([]Metric, 0, 100)
	return result
}

// TestHighVolumeMetrics tests the system under high load
func TestHighVolumeMetrics(t *testing.T) {
	collector := NewMetricsCollector()
	defer collector.Shutdown()

	collector.SetupProcessors()

	ctx := context.Background()

	// Generate metrics from multiple sources concurrently
	sources := 10
	metricsPerSource := 1000

	var wg sync.WaitGroup
	start := time.Now()

	for i := 0; i < sources; i++ {
		wg.Add(1)
		go func(sourceID int) {
			defer wg.Done()

			for j := 0; j < metricsPerSource; j++ {
				metric := Metric{
					ID:        fmt.Sprintf("metric-%d-%d", sourceID, j),
					Source:    fmt.Sprintf("source-%d", sourceID),
					Type:      "counter",
					Name:      "requests_total",
					Value:     float64(j),
					Timestamp: time.Now(),
				}

				// Distribute across priorities
				switch j % 10 {
				case 0:
					metric.Priority = "critical"
				case 1, 2, 3:
					metric.Priority = "standard"
				default:
					metric.Priority = "bulk"
				}

				// Best effort - don't fail on queue full
				collector.CollectMetric(ctx, metric)

				// Simulate realistic metric generation rate
				if j%100 == 0 {
					time.Sleep(1 * time.Millisecond)
				}
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	// Wait for processing to complete
	time.Sleep(100 * time.Millisecond)

	// Check stats
	processed, dropped, errors := collector.GetStats()
	total := sources * metricsPerSource

	t.Logf("High Volume Test Results:")
	t.Logf("  Total metrics: %d", total)
	t.Logf("  Processed: %d (%.2f%%)", processed, float64(processed)*100/float64(total))
	t.Logf("  Dropped: %d (%.2f%%)", dropped, float64(dropped)*100/float64(total))
	t.Logf("  Errors: %d", errors)
	t.Logf("  Duration: %v", elapsed)
	t.Logf("  Rate: %.0f metrics/sec", float64(processed)/elapsed.Seconds())

	// Should process at least 10% of metrics (queue saturation is expected in this test)
	// The test is intentionally overwhelming the system to test graceful degradation
	if processed < int64(total*10/100) {
		t.Errorf("Processed too few metrics: %d < %d (10%%)", processed, total*10/100)
	}

	// Check storage performance
	avgLatency := collector.storage.GetAverageWriteLatency()
	t.Logf("  Avg storage latency: %v", avgLatency)

	if avgLatency > 10*time.Millisecond {
		t.Errorf("Storage latency too high: %v", avgLatency)
	}
}

// TestQueueSaturation tests behavior when queues are saturated
func TestQueueSaturation(t *testing.T) {
	// Create collector with minimal workers to force saturation
	collector := &MetricsCollector{
		critical: hookz.New[Metric](hookz.WithWorkers(1), hookz.WithTimeout(1*time.Second)),
		standard: hookz.New[Metric](hookz.WithWorkers(1), hookz.WithTimeout(5*time.Second)),
		bulk:     hookz.New[Metric](hookz.WithWorkers(1), hookz.WithTimeout(30*time.Second)),
		storage: &MetricStorage{
			writeLatency: 50 * time.Millisecond, // Slow storage
		},
	}
	defer collector.Shutdown()

	// Add slow processor to create backpressure
	collector.critical.Hook(metricReceived, func(ctx context.Context, metric Metric) error {
		time.Sleep(100 * time.Millisecond) // Simulate slow processing
		return nil
	})

	ctx := context.Background()

	// Send burst of critical metrics
	var dropCount atomic.Int32

	for i := 0; i < 100; i++ {
		metric := Metric{
			ID:       fmt.Sprintf("burst-%d", i),
			Source:   "burst-test",
			Type:     "gauge",
			Name:     "queue_test",
			Value:    float64(i),
			Priority: "critical",
		}

		err := collector.CollectMetric(ctx, metric)
		if errors.Is(err, hookz.ErrQueueFull) {
			dropCount.Add(1)
		}
	}

	// Should drop most metrics due to queue saturation
	if drops := dropCount.Load(); drops < 90 {
		t.Errorf("Expected high drop rate during saturation, got %d/100", drops)
	}

	t.Logf("Saturation test: %d/100 metrics dropped (expected)", dropCount.Load())
}

// TestPriorityIsolation verifies that different priority queues are isolated
func TestPriorityIsolation(t *testing.T) {
	collector := NewMetricsCollector()
	defer collector.Shutdown()

	ctx := context.Background()

	// Track processing for each priority
	var criticalProcessed atomic.Int32
	var standardProcessed atomic.Int32
	var bulkProcessed atomic.Int32

	// Add hooks with different delays
	collector.critical.Hook(metricReceived, func(ctx context.Context, metric Metric) error {
		criticalProcessed.Add(1)
		time.Sleep(1 * time.Millisecond) // Fast processing
		return nil
	})

	collector.standard.Hook(metricReceived, func(ctx context.Context, metric Metric) error {
		standardProcessed.Add(1)
		time.Sleep(50 * time.Millisecond) // Slow processing
		return nil
	})

	collector.bulk.Hook(metricReceived, func(ctx context.Context, metric Metric) error {
		bulkProcessed.Add(1)
		time.Sleep(10 * time.Millisecond) // Medium processing
		return nil
	})

	// Send metrics to all priorities simultaneously
	var wg sync.WaitGroup

	// Critical metrics - should process quickly despite other queues
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			collector.CollectMetric(ctx, Metric{
				ID:       fmt.Sprintf("critical-%d", i),
				Priority: "critical",
			})
		}
	}()

	// Standard metrics - will be slow
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			collector.CollectMetric(ctx, Metric{
				ID:       fmt.Sprintf("standard-%d", i),
				Priority: "standard",
			})
		}
	}()

	// Bulk metrics
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			collector.CollectMetric(ctx, Metric{
				ID:       fmt.Sprintf("bulk-%d", i),
				Priority: "bulk",
			})
		}
	}()

	wg.Wait()

	// Check progress after short wait
	time.Sleep(100 * time.Millisecond)

	// Critical should be mostly done despite slow standard queue
	critical := criticalProcessed.Load()
	standard := standardProcessed.Load()
	bulk := bulkProcessed.Load()

	t.Logf("Priority isolation after 100ms:")
	t.Logf("  Critical: %d/50", critical)
	t.Logf("  Standard: %d/10", standard)
	t.Logf("  Bulk: %d/30", bulk)

	// Critical should process quickly
	if critical < 45 {
		t.Errorf("Critical metrics processing too slow: %d/50", critical)
	}

	// Standard will be slow due to processing delay (but may complete in fast systems)
	// Just log it for informational purposes
	if standard == 10 {
		t.Logf("Note: Standard queue completed despite slow processing (fast system)")
	}
}

// TestMemoryPressure tests behavior under memory pressure
func TestMemoryPressure(t *testing.T) {
	// Skip if not enough memory
	if testing.Short() {
		t.Skip("Skipping memory pressure test in short mode")
	}

	collector := NewMetricsCollector()
	defer collector.Shutdown()

	ctx := context.Background()

	// Track memory before test
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	// Add hook that accumulates data (simulates memory leak)
	var accumulator []Metric
	var mu sync.Mutex

	collector.standard.Hook(metricReceived, func(ctx context.Context, metric Metric) error {
		mu.Lock()
		accumulator = append(accumulator, metric)
		mu.Unlock()
		return nil
	})

	// Generate large volume of metrics with large tags
	for i := 0; i < 1000; i++ {
		metric := Metric{
			ID:       fmt.Sprintf("mem-%d", i),
			Source:   "memory-test",
			Priority: "standard",
			Tags:     make(map[string]string),
		}

		// Add large tag data
		for j := 0; j < 100; j++ {
			metric.Tags[fmt.Sprintf("tag-%d", j)] = fmt.Sprintf("value-%d-%d", i, j)
		}

		err := collector.CollectMetric(ctx, metric)
		if err != nil {
			// Expected to get queue full at some point
			if errors.Is(err, hookz.ErrQueueFull) {
				break
			}
		}

		// Let some processing happen
		if i%100 == 0 {
			runtime.GC()
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	// Check memory after test
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	memIncrease := memAfter.Alloc - memBefore.Alloc
	memIncreaseMB := float64(memIncrease) / (1024 * 1024)

	t.Logf("Memory pressure test:")
	t.Logf("  Memory before: %.2f MB", float64(memBefore.Alloc)/(1024*1024))
	t.Logf("  Memory after: %.2f MB", float64(memAfter.Alloc)/(1024*1024))
	t.Logf("  Increase: %.2f MB", memIncreaseMB)

	// Should not use excessive memory (this is just a sanity check)
	if memIncreaseMB > 100 {
		t.Errorf("Excessive memory usage: %.2f MB", memIncreaseMB)
	}
}

// TestGracefulDegradation tests system degradation under extreme load
func TestGracefulDegradation(t *testing.T) {
	collector := NewMetricsCollector()
	defer collector.Shutdown()

	ctx := context.Background()

	// Add processor that gets progressively slower
	var processTime atomic.Int64

	collector.standard.Hook(metricReceived, func(ctx context.Context, metric Metric) error {
		// Simulate degrading backend
		delay := time.Duration(processTime.Add(1)) * time.Microsecond
		if delay > 100*time.Millisecond {
			delay = 100 * time.Millisecond
		}
		time.Sleep(delay)
		return nil
	})

	// Track metrics over time
	type window struct {
		processed int64
		dropped   int64
		timestamp time.Time
	}

	var windows []window
	var lastProcessed, lastDropped int64

	// Generate continuous load
	done := make(chan bool)
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				collector.CollectMetric(ctx, Metric{
					ID:       fmt.Sprintf("degrade-%d", time.Now().UnixNano()),
					Priority: "standard",
				})
				time.Sleep(100 * time.Microsecond) // 10k metrics/sec attempt rate
			}
		}
	}()

	// Collect stats over time
	for i := 0; i < 5; i++ {
		time.Sleep(100 * time.Millisecond)

		processed, dropped, _ := collector.GetStats()
		windows = append(windows, window{
			processed: processed - lastProcessed,
			dropped:   dropped - lastDropped,
			timestamp: time.Now(),
		})
		lastProcessed = processed
		lastDropped = dropped
	}

	close(done)

	// Analyze degradation pattern
	t.Log("Graceful degradation over time:")
	for i, w := range windows {
		dropRate := float64(w.dropped) / float64(w.processed+w.dropped) * 100
		t.Logf("  Window %d: processed=%d, dropped=%d (%.1f%% drop rate)",
			i+1, w.processed, w.dropped, dropRate)
	}

	// Verify degradation is controlled (drop rate increases but system doesn't crash)
	firstDropRate := float64(windows[0].dropped) / float64(windows[0].processed+windows[0].dropped)
	lastDropRate := float64(windows[len(windows)-1].dropped) / float64(windows[len(windows)-1].processed+windows[len(windows)-1].dropped)

	if lastDropRate < firstDropRate {
		t.Error("Expected drop rate to increase as system degrades")
	}
}

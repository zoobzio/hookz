package hookz

// Metrics provides observability data for hookz service monitoring.
// All counter fields use atomic operations for thread safety.
// Capacity fields are static and don't require atomics.
type Metrics struct {
	// Primary Queue Metrics
	QueueDepth    int64 // Current tasks in primary queue (atomic)
	QueueCapacity int64 // Primary queue capacity (static)

	// Throughput Counters (atomic operations required)
	TasksProcessed int64 // Successfully completed hooks
	TasksRejected  int64 // Tasks rejected due to full queue
	TasksFailed    int64 // Hook executions that failed or panicked
	TasksExpired   int64 // Tasks discarded due to context cancellation

	// Registration Metrics
	RegisteredHooks int64 // Current registered hooks (requires mutex read)

	// Optional Overflow Metrics (Phase 2)
	OverflowDepth    int64 // Current overflow queue depth
	OverflowCapacity int64 // Overflow queue capacity
	OverflowDrained  int64 // Tasks successfully moved from overflow to primary
}

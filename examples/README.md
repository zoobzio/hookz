# hookz Examples - Real Production Scenarios

These examples demonstrate how development teams actually use hookz to solve real-world problems. Each scenario comes from actual production incidents and implementations, showing the evolution from "just ship it" to battle-tested systems.

## Examples Overview

### 1. [Payment Processing Pipeline](payment-processing/)
**The Team**: FinTech startup processing $10M daily  
**The Crisis**: Payment gateway callbacks causing cascade failures  
**The Solution**: Async hooks with circuit breakers prevent payment disasters

### 2. [Real-time Collaboration Platform](collaboration-platform/)
**The Team**: Remote work tool with 50k concurrent users  
**The Crisis**: User actions blocking UI updates, causing 5-second delays  
**The Solution**: Non-blocking event propagation with selective subscription

### 3. [Inventory Management System](inventory-management/)
**The Team**: E-commerce platform during Black Friday  
**The Crisis**: Stock updates cascading through 12 services, causing timeouts  
**The Solution**: Parallel hook execution with timeout control

### 4. [Audit & Compliance System](audit-compliance/)
**The Team**: Healthcare SaaS under HIPAA compliance  
**The Crisis**: Audit logs missing critical events during high load  
**The Solution**: Guaranteed delivery with resilient worker pools

### 5. [Gaming Platform Events](gaming-platform/)
**The Team**: Mobile gaming backend with 1M daily active users  
**The Crisis**: Achievement notifications blocking game state updates  
**The Solution**: Priority-based event routing with graceful degradation

## How These Examples Work

Each example follows a narrative structure:

1. **The Problem**: Real production crisis that happened
2. **The Journey**: How the team discovered and solved it
3. **The Implementation**: Complete working code with tests
4. **The Metrics**: Before/after performance and reliability data
5. **The Lessons**: What the team learned

## Running the Examples

Each example is a complete Go module with:
- Runnable main.go showing the progression
- Mock services simulating real-world behavior
- Tests demonstrating failure scenarios
- Benchmarks showing performance characteristics

```bash
# Run any example
cd payment-processing
go run .

# Run with specific scenarios
go run . -scenario=failure  # Show what happens when things break
go run . -scenario=recovery  # Show recovery mechanisms
go run . -scenario=scale     # Show high-load behavior

# Run tests to see edge cases
go test -v

# Run benchmarks for performance
go test -bench=. -benchmem
```

## Common Patterns

These examples demonstrate key patterns teams use with hookz:

- **Service Isolation**: Keep hook systems private, expose only registration
- **Graceful Degradation**: Non-critical hooks shouldn't break critical paths
- **Timeout Management**: Global timeouts prevent cascading failures
- **Resource Protection**: Limits prevent memory exhaustion attacks
- **Async Execution**: UI/API responses never wait for hooks
- **Error Recovery**: Failed hooks get logged, not propagated

## The Evolution Pattern

Most teams follow this progression:

1. **Week 1**: Direct function calls causing blocking
2. **Week 2**: Channels causing goroutine leaks
3. **Week 3**: Callback hell with no cleanup
4. **Week 4**: Discovery of hookz
5. **Week 5**: Production deployment
6. **Week 6+**: Sleeping soundly

## Learn More

- Start with [payment-processing](payment-processing/) for financial systems patterns
- Try [collaboration-platform](collaboration-platform/) for real-time UI patterns
- Explore [inventory-management](inventory-management/) for distributed system patterns
- Review [audit-compliance](audit-compliance/) for reliability patterns
- Check [gaming-platform](gaming-platform/) for high-performance patterns
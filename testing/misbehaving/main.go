package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zoobzio/hookz"
)

// TestMisbehavingHooks demonstrates hooks that ignore context cancellation
func main() {
	fmt.Println("Testing misbehaving hooks that ignore context cancellation...")

	// Create hook system with 50ms timeout
	hooks := hookz.New[string](hookz.WithWorkers(5), hookz.WithTimeout(50*time.Millisecond))
	defer hooks.Close()

	var hookStarted, hookCompleted, hookTimedOut atomic.Bool
	var misbehavingHookDone sync.WaitGroup
	misbehavingHookDone.Add(1)

	// Register a BADLY BEHAVED hook that ignores ctx.Done()
	hooks.Hook("test.event", func(ctx context.Context, data string) error {
		hookStarted.Store(true)
		defer misbehavingHookDone.Done()

		fmt.Println("Hook started - will ignore context cancellation")
		
		// BAD PATTERN: Completely ignores ctx.Done()
		// This simulates a hook that does blocking I/O without context awareness
		time.Sleep(200 * time.Millisecond) // Way longer than 50ms timeout
		
		hookCompleted.Store(true)
		fmt.Println("Hook completed (this should not happen with proper timeout)")
		return nil
	})

	// Register a WELL-BEHAVED hook that respects context
	hooks.Hook("test.event", func(ctx context.Context, data string) error {
		fmt.Println("Well-behaved hook checking context...")
		select {
		case <-time.After(200 * time.Millisecond):
			fmt.Println("Well-behaved hook completed")
			return nil
		case <-ctx.Done():
			hookTimedOut.Store(true)
			fmt.Println("Well-behaved hook respects timeout:", ctx.Err())
			return ctx.Err()
		}
	})

	// Emit event
	ctx := context.Background()
	start := time.Now()
	
	fmt.Println("Emitting event...")
	err := hooks.Emit(ctx, "test.event", "test data")
	if err != nil {
		log.Printf("Emit failed: %v", err)
	}

	// Wait for the misbehaving hook to complete (it will ignore timeout)
	misbehavingHookDone.Wait()
	
	elapsed := time.Since(start)
	
	fmt.Printf("\nResults after %v:\n", elapsed)
	fmt.Printf("- Hook started: %v\n", hookStarted.Load())
	fmt.Printf("- Hook completed: %v\n", hookCompleted.Load())
	fmt.Printf("- Hook timed out: %v\n", hookTimedOut.Load())
	
	if hookCompleted.Load() {
		fmt.Printf("\n❌ PROBLEM CONFIRMED: Misbehaving hook ran for %v despite 50ms timeout\n", elapsed)
		fmt.Println("The timeout context was canceled, but the goroutine kept running")
		fmt.Println("This is a fundamental limitation - Go cannot force-kill goroutines")
	} else {
		fmt.Println("✅ Timeout worked correctly")
	}
}
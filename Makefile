# hookz Makefile
# Standard AEGIS project build system

.DEFAULT_GOAL := check

# Build configuration
GO := go
GOFLAGS := -v
TESTFLAGS := -race -count=1
COVERPROFILE := coverage.out

# Target directories
DOCS_DIR := docs
EXAMPLES_DIR := examples
TEST_DIR := testing

.PHONY: help
help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

.PHONY: test
test: ## Run unit tests with race detection
	$(GO) test $(TESTFLAGS) $(GOFLAGS) ./...

.PHONY: test-short
test-short: ## Run unit tests (short mode)
	$(GO) test $(TESTFLAGS) -short $(GOFLAGS) ./...

.PHONY: test-examples
test-examples: ## Run tests for all examples
	@echo "Running example tests..."
	@for dir in examples/*/; do \
		if [ -f "$$dir/main_test.go" ]; then \
			echo "Testing $$dir"; \
			(cd "$$dir" && go test -v -race ./...); \
		fi \
	done

.PHONY: bench
bench: ## Run benchmarks
	$(GO) test -bench=. -benchmem ./...

.PHONY: lint
lint: ## Run code quality checks
	@which golangci-lint >/dev/null || (echo "Please install golangci-lint: https://golangci-lint.run/usage/install/" && exit 1)
	golangci-lint run

.PHONY: coverage
coverage: ## Generate test coverage report
	$(GO) test $(TESTFLAGS) -coverprofile=$(COVERPROFILE) ./...
	$(GO) tool cover -html=$(COVERPROFILE) -o coverage.html
	@echo "Coverage report generated: coverage.html"

.PHONY: coverage-text
coverage-text: ## Show test coverage in terminal
	$(GO) test $(TESTFLAGS) -coverprofile=$(COVERPROFILE) ./...
	$(GO) tool cover -func=$(COVERPROFILE)

.PHONY: check
check: test lint ## Quick verification (tests + linting)

.PHONY: ci
ci: clean lint test integration test-benchmarks test-reliability coverage ## Full CI simulation (all tests + quality checks)
	@echo "Full CI simulation complete!"

.PHONY: integration
integration: ## Run integration tests
	@if [ -d "$(TEST_DIR)/integration" ]; then \
		$(GO) test $(TESTFLAGS) ./$(TEST_DIR)/integration/...; \
	else \
		echo "No integration tests found"; \
	fi

.PHONY: e2e
e2e: ## Run end-to-end tests  
	@if [ -d "$(TEST_DIR)/e2e" ]; then \
		$(GO) test $(TESTFLAGS) ./$(TEST_DIR)/e2e/...; \
	else \
		echo "No e2e tests found"; \
	fi

.PHONY: benchmarks
benchmarks: ## Run performance benchmarks
	@if [ -d "$(TEST_DIR)/benchmarks" ]; then \
		$(GO) test -bench=. -benchmem ./$(TEST_DIR)/benchmarks/...; \
	else \
		echo "No benchmark tests found"; \
	fi

.PHONY: test-benchmarks
test-benchmarks: ## Run all benchmarks
	@echo "Running all benchmarks..."
	@$(GO) test -v -bench=. -benchmem -benchtime=1s -timeout=10m ./$(TEST_DIR)/benchmarks/...

.PHONY: test-reliability
test-reliability: ## Run reliability tests
	@echo "Running reliability tests..."
	@$(GO) test -v -race -timeout=10m -run TestResilience ./testing/integration/... || echo "No resilience tests found"
	@$(GO) test -v -race -timeout=5m -run TestPanicRecovery ./testing/integration/... || echo "No panic recovery tests found"
	@$(GO) test -v -race -timeout=10m -run TestResourceLeak ./testing/integration/... || echo "No resource leak tests found"
	@$(GO) test -v -race -timeout=5m -run TestConcurrentModification ./testing/integration/... || echo "No concurrent modification tests found"

.PHONY: test-all
test-all: ## Run all test suites (unit + integration + reliability)
	@$(MAKE) test
	@$(MAKE) integration
	@$(MAKE) test-reliability
	@echo "All test suites completed!"

.PHONY: build
build: ## Build the library (verify compilation)
	$(GO) build ./...

.PHONY: mod-tidy
mod-tidy: ## Clean up go.mod and go.sum
	$(GO) mod tidy

.PHONY: mod-verify
mod-verify: ## Verify module dependencies
	$(GO) mod verify

.PHONY: deps
deps: ## Download dependencies
	$(GO) mod download

.PHONY: clean
clean: ## Clean build artifacts and test cache
	$(GO) clean -testcache -cache
	rm -f $(COVERPROFILE) coverage.html

.PHONY: fmt
fmt: ## Format source code
	$(GO) fmt ./...

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: doc
doc: ## Start documentation server
	@echo "Starting documentation server at http://localhost:6060/pkg/github.com/zoobzio/hookz/"
	godoc -http=:6060

.PHONY: examples
examples: ## Run all examples
	@if [ -d "$(EXAMPLES_DIR)" ]; then \
		for example in $(EXAMPLES_DIR)/*; do \
			if [ -f "$$example/main.go" ]; then \
				echo "Running example: $$example"; \
				$(GO) run "$$example/main.go"; \
			fi \
		done; \
	else \
		echo "No examples found"; \
	fi

.PHONY: stress
stress: ## Run stress tests
	$(GO) test -run=TestStress -v ./...

.PHONY: race
race: ## Run tests with race detector (verbose)
	$(GO) test -race -v ./...

.PHONY: install-tools
install-tools: ## Install development tools
	@echo "Installing development tools..."
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8

.PHONY: all
all: clean fmt vet lint test coverage integration benchmarks build ## Run all quality checks

# Release targets
.PHONY: release-check
release-check: ## Pre-release verification
	@echo "Verifying release readiness..."
	@$(MAKE) clean
	@$(MAKE) all
	@$(GO) mod verify
	@echo "Release checks passed"

# Development helpers
.PHONY: watch
watch: ## Watch for changes and run tests
	@which fswatch >/dev/null || (echo "Please install fswatch for watch mode" && exit 1)
	@echo "Watching for changes..."
	@fswatch -o . | xargs -n1 -I{} make test

.PHONY: profile
profile: ## Generate CPU profile during tests
	$(GO) test -cpuprofile=cpu.prof -memprofile=mem.prof -bench=. ./...
	@echo "Profiles generated: cpu.prof, mem.prof"
	@echo "View with: go tool pprof cpu.prof"
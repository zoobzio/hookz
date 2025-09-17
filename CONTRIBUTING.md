# Contributing to hookz

Thank you for your interest in contributing to hookz! This guide will help you get started.

## Code of Conduct

By participating in this project, you agree to maintain a respectful and inclusive environment for all contributors.

## Getting Started

1. Fork the repository
2. Clone your fork: `git clone https://github.com/yourusername/hookz.git`
3. Create a feature branch: `git checkout -b feature/your-feature-name`
4. Make your changes
5. Run tests: `go test ./...`
6. Commit your changes with a descriptive message
7. Push to your fork: `git push origin feature/your-feature-name`
8. Create a Pull Request

## Development Guidelines

### Code Style

- Follow standard Go conventions
- Run `go fmt` before committing
- Add comments for exported functions and types
- Keep functions small and focused

### Testing

- Write tests for new functionality
- Ensure all tests pass: `go test ./...`
- Include benchmarks for performance-critical code
- Aim for good test coverage

### Documentation

- Update documentation for API changes
- Add examples for new features
- Keep doc comments clear and concise

## Types of Contributions

### Bug Reports

- Use GitHub Issues
- Include minimal reproduction code
- Describe expected vs actual behavior
- Include Go version and OS

### Feature Requests

- Open an issue for discussion first
- Explain the use case
- Consider backwards compatibility

### Code Contributions

#### Adding Hook Types

New hook types should:
- Follow the existing pattern (Hook interface implementation)
- Include comprehensive tests
- Add documentation with examples
- Handle context cancellation properly

#### Adding Service Features

New service features should:
- Maintain thread safety
- Handle concurrent operations properly
- Include tests for error cases
- Document behavior clearly

#### Examples

New examples should:
- Solve a real-world problem
- Include tests and benchmarks
- Have a descriptive README
- Follow the existing structure

## Pull Request Process

1. **Keep PRs focused** - One feature/fix per PR
2. **Write descriptive commit messages**
3. **Update tests and documentation**
4. **Ensure CI passes**
5. **Respond to review feedback**

## Testing

Run the full test suite:
```bash
go test ./...
```

Run with race detection:
```bash
go test -race ./...
```

Run benchmarks:
```bash
go test -bench=. ./...
```

Run integration tests:
```bash
go test ./testing/integration/...
```

## Project Structure

```
hookz/
├── *.go              # Core library files
├── *_test.go         # Unit tests
├── examples/         # Example implementations
│   └── */           # Individual examples
├── docs/            # Documentation
├── testing/         # Testing infrastructure
│   ├── benchmarks/  # Performance tests
│   ├── integration/ # Integration tests
│   ├── misbehaving/ # Resource leak tests
│   └── reliability/ # Reliability tests
└── worker_pool.go   # Worker pool implementation
```

## Commit Messages

Follow conventional commits:
- `feat:` New feature
- `fix:` Bug fix
- `docs:` Documentation changes
- `test:` Test additions/changes
- `refactor:` Code refactoring
- `perf:` Performance improvements
- `chore:` Maintenance tasks

## Release Process

### Automated Releases

This project uses automated release versioning. To create a release:

1. Go to Actions → Release → Run workflow
2. Leave "Version override" empty for automatic version inference
3. Click "Run workflow"

The system will:
- Automatically determine the next version from conventional commits
- Create a git tag
- Generate release notes via GoReleaser
- Publish the release to GitHub

### Manual Release (Legacy)

You can still create releases manually:
```bash
git tag v1.2.3
git push origin v1.2.3
```

### Known Limitations

- **Protected branches**: The automated release cannot bypass branch protection rules. This is by design for security.
- **Concurrent releases**: Rapid successive releases may fail. Simply retry after a moment.
- **Conventional commits required**: Version inference requires conventional commit format (`feat:`, `fix:`, etc.)

### Commit Conventions for Versioning
- `feat:` new features (minor version: 1.2.0 → 1.3.0)
- `fix:` bug fixes (patch version: 1.2.0 → 1.2.1)  
- `feat!:` breaking changes (major version: 1.2.0 → 2.0.0)
- `docs:`, `test:`, `chore:` no version change

Example: `feat(hooks): add timeout support for async hooks`

### Version Preview on Pull Requests
Every PR automatically shows the next version that will be created:
- Check PR comments for "Version Preview" 
- Updates automatically as you add commits
- Helps verify your commits have the intended effect

## Hook System Architecture

### Core Components

- **Hook Interface**: Defines the contract for hook implementations
- **Service**: Manages hook registration and execution
- **Worker Pool**: Handles concurrent hook execution
- **API Layer**: Provides high-level functions for common operations

### Hook Lifecycle

1. **Registration**: Hooks are registered with the service
2. **Validation**: Hook configuration is validated
3. **Execution**: Hooks are executed when events occur
4. **Error Handling**: Errors are captured and handled appropriately
5. **Cleanup**: Resources are properly cleaned up

### Best Practices

- Always handle context cancellation in hook handlers
- Use appropriate timeouts for hook operations
- Validate inputs before processing
- Log errors for debugging
- Clean up resources in defer statements

## Performance Considerations

- Hook execution is designed to be fast and non-blocking
- Use worker pools for CPU-intensive operations
- Monitor memory usage in long-running hooks
- Benchmark critical paths

## Questions?

- Open an issue for questions
- Check existing issues first
- Be patient and respectful

Thank you for contributing to hookz!
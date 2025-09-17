---
name: Bug report
about: Create a report to help us improve
title: '[BUG] '
labels: 'bug'
assignees: ''

---

**Describe the bug**
A clear and concise description of what the bug is.

**To Reproduce**
Steps to reproduce the behavior:
1. Register hook with '...'
2. Emit event '...'
3. See error

**Code Example**
```go
// Minimal code example that reproduces the issue
package main

import (
    "context"
    "github.com/zoobzio/hookz"
)

func main() {
    service := hookz.NewService()
    // Your code here
}
```

**Expected behavior**
A clear and concise description of what you expected to happen.

**Actual behavior**
What actually happened, including any error messages or stack traces.

**Environment:**
 - OS: [e.g. macOS, Linux, Windows]
 - Go version: [e.g. 1.21.0]
 - hookz version: [e.g. v1.0.0]

**Additional context**
Add any other context about the problem here.
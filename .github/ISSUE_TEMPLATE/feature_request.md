---
name: Feature request
about: Suggest an idea for hookz
title: '[FEATURE] '
labels: 'enhancement'
assignees: ''

---

**Is your feature request related to a problem? Please describe.**
A clear and concise description of what the problem is. Ex. I'm always frustrated when [...]

**Describe the solution you'd like**
A clear and concise description of what you want to happen.

**Describe alternatives you've considered**
A clear and concise description of any alternative solutions or features you've considered.

**Example usage**
```go
// Show how the feature would be used
service := hookz.NewService()
// Your proposed usage
service.Register("event", func(ctx context.Context, data interface{}) error {
    // Your proposed hook implementation
    return nil
})
```

**Additional context**
Add any other context, diagrams, or screenshots about the feature request here.
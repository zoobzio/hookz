package hookz

// Hook represents a handle to a registered callback function.
// It provides a way to unregister the callback from its associated event.
//
// Hook handles are returned by the Hook() and HookWithTimeout() methods
// and should be stored if you need to unregister the callback later.
//
// Thread Safety:
// Hook methods are safe for concurrent use, but each hook handle
// should only be used to unregister once. Multiple calls to Unhook()
// on the same handle will return ErrAlreadyUnhooked.
//
// Example:
//
//	hook, err := service.Hook("user.created", userHandler)
//	if err != nil {
//	    return err
//	}
//
//	// Later, unregister the hook
//	if err := hook.Unhook(); err != nil {
//	    log.Printf("Failed to unhook: %v", err)
//	}
type Hook struct {
	// unhook is an internal function that performs the actual
	// unregistration. It's set during hook creation and cleared
	// after the first successful unhook operation.
	unhook func() error
}

// Unhook removes this hook from its associated event.
//
// After calling Unhook(), this hook handle becomes invalid and
// subsequent calls will return ErrAlreadyUnhooked.
//
// The operation is atomic - the hook is either successfully removed
// or the call fails with an error. Partial removal cannot occur.
//
// Returns:
//   - nil: Hook successfully removed
//   - ErrAlreadyUnhooked: Hook was already unhooked or invalid
//   - ErrHookNotFound: Hook no longer exists (rare race condition)
//
// Example:
//
//	if err := hook.Unhook(); err != nil {
//	    if err == hookz.ErrAlreadyUnhooked {
//	        log.Println("Hook was already removed")
//	    } else {
//	        log.Printf("Failed to unhook: %v", err)
//	    }
//	}
func (h *Hook) Unhook() error {
	if h.unhook == nil {
		return ErrAlreadyUnhooked
	}
	err := h.unhook()
	h.unhook = nil
	return err
}

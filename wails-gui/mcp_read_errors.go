package main

// Only code-owned validation messages cross the bridge. Store, filesystem and
// driver errors are replaced with a generic availability message by the caller.
type mcpReadValidationError struct{ message string }

func (e *mcpReadValidationError) Error() string { return e.message }

func mcpReadInvalid(message string) error { return &mcpReadValidationError{message: message} }

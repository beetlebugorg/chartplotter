package iso8211

import "fmt"

// Error definitions for ISO 8211 parser
//
// All errors include context and byte offsets where applicable for debugging.
// For S-57/S-52 implementation details, see IHO S-57 Part 3:
// https://iho.int/uploads/user/pubs/standards/s-57/31Main.pdf

// ParseError represents a parsing error with context
type ParseError struct {
	Offset  int64  // Byte offset in file where error occurred
	Context string // What was being parsed (e.g., "leader", "directory", "field area")
	Err     error  // Underlying error
}

func (e *ParseError) Error() string {
	if e.Offset >= 0 {
		return fmt.Sprintf("parse error at offset %d (%s): %v", e.Offset, e.Context, e.Err)
	}
	return fmt.Sprintf("parse error (%s): %v", e.Context, e.Err)
}

func (e *ParseError) Unwrap() error {
	return e.Err
}

// NewParseError creates a new parse error with context
func NewParseError(offset int64, context string, err error) *ParseError {
	return &ParseError{
		Offset:  offset,
		Context: context,
		Err:     err,
	}
}

// ValidationError represents a validation error for invalid data
type ValidationError struct {
	Field   string // Field name that failed validation
	Value   string // Invalid value
	Message string // Error message
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error: field %s has invalid value %q: %s", e.Field, e.Value, e.Message)
}

// NewValidationError creates a new validation error
func NewValidationError(field, value, message string) *ValidationError {
	return &ValidationError{
		Field:   field,
		Value:   value,
		Message: message,
	}
}

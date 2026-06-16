package iso8211

import (
	"errors"
	"strings"
	"testing"
)

// TestParseError tests ParseError creation and methods
func TestParseError(t *testing.T) {
	baseErr := errors.New("test error")

	// Test with offset
	err := NewParseError(1234, "leader", baseErr)
	errStr := err.Error()

	if !strings.Contains(errStr, "1234") {
		t.Errorf("Error message should contain offset 1234, got: %s", errStr)
	}
	if !strings.Contains(errStr, "leader") {
		t.Errorf("Error message should contain context 'leader', got: %s", errStr)
	}
	if !strings.Contains(errStr, "test error") {
		t.Errorf("Error message should contain base error, got: %s", errStr)
	}

	// Test Unwrap
	if unwrapped := errors.Unwrap(err); unwrapped != baseErr {
		t.Errorf("Expected unwrapped error to be baseErr, got: %v", unwrapped)
	}

	// Test without offset
	err2 := NewParseError(-1, "directory", baseErr)
	errStr2 := err2.Error()

	if strings.Contains(errStr2, "offset -1") {
		t.Errorf("Error message should not contain negative offset, got: %s", errStr2)
	}
	if !strings.Contains(errStr2, "directory") {
		t.Errorf("Error message should contain context 'directory', got: %s", errStr2)
	}
}

// TestValidationError tests ValidationError creation and methods
func TestValidationError(t *testing.T) {
	err := NewValidationError("LeaderIdentifier", "X", "must be L or D")
	errStr := err.Error()

	if !strings.Contains(errStr, "LeaderIdentifier") {
		t.Errorf("Error message should contain field name, got: %s", errStr)
	}
	if !strings.Contains(errStr, "X") {
		t.Errorf("Error message should contain value, got: %s", errStr)
	}
	if !strings.Contains(errStr, "must be L or D") {
		t.Errorf("Error message should contain message, got: %s", errStr)
	}
}

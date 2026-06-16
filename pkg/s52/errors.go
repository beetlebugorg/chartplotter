package s52

import (
	"fmt"
)

// ParseError extends DAIError with parser-specific context
type ParseError struct {
	DAIError
	FileName   string
	LineNumber int
	Section    string // SYMB, PATT, LUPT, etc.
	RecordType string // SYMD, PXPO, etc.
}

// NewParseError creates a new parser-specific error
func NewParseError(code ErrorCode, message string, cause error) *ParseError {
	return &ParseError{
		DAIError: DAIError{
			Code:    code,
			Message: message,
			Cause:   cause,
		},
	}
}

// Error returns the formatted error message
func (e *ParseError) Error() string {
	msg := e.DAIError.Error()

	if e.FileName != "" {
		msg = fmt.Sprintf("%s (file: %s", msg, e.FileName)
		if e.LineNumber > 0 {
			msg = fmt.Sprintf("%s, line: %d", msg, e.LineNumber)
		}
		if e.Section != "" {
			msg = fmt.Sprintf("%s, section: %s", msg, e.Section)
		}
		if e.RecordType != "" {
			msg = fmt.Sprintf("%s, record: %s", msg, e.RecordType)
		}
		msg += ")"
	}

	return msg
}

// WithFile adds file context to the error
func (e *ParseError) WithFile(fileName string, lineNumber int) *ParseError {
	e.FileName = fileName
	e.LineNumber = lineNumber
	return e
}

// WithSection adds section context to the error
func (e *ParseError) WithSection(section string) *ParseError {
	e.Section = section
	return e
}

// WithRecord adds record type context to the error
func (e *ParseError) WithRecord(recordType string) *ParseError {
	e.RecordType = recordType
	return e
}

// ValidationError represents validation-specific errors
type ValidationError struct {
	*ParseError
	ValidationRule string
	ExpectedValue  interface{}
	ActualValue    interface{}
}

// NewValidationError creates a new validation error
func NewValidationError(rule string, expected, actual interface{}, message string) *ValidationError {
	return &ValidationError{
		ParseError: NewParseError(
			ErrorCodeValidationFailed,
			message,
			nil,
		),
		ValidationRule: rule,
		ExpectedValue:  expected,
		ActualValue:    actual,
	}
}

// Error returns the formatted validation error message
func (e *ValidationError) Error() string {
	msg := e.ParseError.Error()

	if e.ValidationRule != "" {
		msg = fmt.Sprintf("%s (rule: %s", msg, e.ValidationRule)

		if e.ExpectedValue != nil {
			msg = fmt.Sprintf("%s, expected: %v", msg, e.ExpectedValue)
		}

		if e.ActualValue != nil {
			msg = fmt.Sprintf("%s, actual: %v", msg, e.ActualValue)
		}

		msg += ")"
	}

	return msg
}

// S52ComplianceError represents S-52 standard compliance errors
type S52ComplianceError struct {
	*ParseError
	Standard   string // S-52 standard section
	Violation  string // Specific violation description
	Suggestion string // Suggested fix
}

// NewS52ComplianceError creates a new S-52 compliance error
func NewS52ComplianceError(standard, violation, suggestion string) *S52ComplianceError {
	return &S52ComplianceError{
		ParseError: NewParseError(
			ErrorCodeValidationS52NonCompliant,
			fmt.Sprintf("S-52 compliance violation: %s", violation),
			nil,
		),
		Standard:   standard,
		Violation:  violation,
		Suggestion: suggestion,
	}
}

// Error returns the formatted S-52 compliance error message
func (e *S52ComplianceError) Error() string {
	msg := e.ParseError.Error()

	if e.Standard != "" {
		msg = fmt.Sprintf("%s (standard: %s)", msg, e.Standard)
	}

	if e.Suggestion != "" {
		msg = fmt.Sprintf("%s - Suggestion: %s", msg, e.Suggestion)
	}

	return msg
}

// CoordinateError represents coordinate parsing errors
type CoordinateError struct {
	*ParseError
	Coordinate string
	Value      string
	Range      string
}

// NewCoordinateError creates a new coordinate parsing error
func NewCoordinateError(coordinate, value, validRange string) *CoordinateError {
	return &CoordinateError{
		ParseError: NewParseError(
			ErrorCodeParseInvalidCoordinate,
			fmt.Sprintf("Invalid coordinate %s: %s", coordinate, value),
			nil,
		),
		Coordinate: coordinate,
		Value:      value,
		Range:      validRange,
	}
}

// Error returns the formatted coordinate error message
func (e *CoordinateError) Error() string {
	msg := e.ParseError.Error()

	if e.Range != "" {
		msg = fmt.Sprintf("%s (valid range: %s)", msg, e.Range)
	}

	return msg
}

// ColorError represents color parsing errors
type ColorError struct {
	*ParseError
	ColorName  string
	ColorValue string
	ColorSpace string
}

// NewColorError creates a new color parsing error
func NewColorError(name, value, colorSpace string) *ColorError {
	return &ColorError{
		ParseError: NewParseError(
			ErrorCodeParseInvalidColor,
			fmt.Sprintf("Invalid color %s: %s", name, value),
			nil,
		),
		ColorName:  name,
		ColorValue: value,
		ColorSpace: colorSpace,
	}
}

// Error returns the formatted color error message
func (e *ColorError) Error() string {
	msg := e.ParseError.Error()

	if e.ColorSpace != "" {
		msg = fmt.Sprintf("%s (color space: %s)", msg, e.ColorSpace)
	}

	return msg
}

// CommandError represents vector command parsing errors
type CommandError struct {
	*ParseError
	Command    string
	Parameters []string
	Context    string
}

// NewCommandError creates a new vector command parsing error
func NewCommandError(command string, parameters []string, context string) *CommandError {
	return &CommandError{
		ParseError: NewParseError(
			ErrorCodeParseInvalidCommand,
			fmt.Sprintf("Invalid vector command: %s", command),
			nil,
		),
		Command:    command,
		Parameters: parameters,
		Context:    context,
	}
}

// Error returns the formatted command error message
func (e *CommandError) Error() string {
	msg := e.ParseError.Error()

	if len(e.Parameters) > 0 {
		msg = fmt.Sprintf("%s (parameters: %v)", msg, e.Parameters)
	}

	if e.Context != "" {
		msg = fmt.Sprintf("%s - Context: %s", msg, e.Context)
	}

	return msg
}

// IsParserError checks if an error is a parser-specific error
func IsParserError(err error) bool {
	_, ok := err.(*ParseError)
	return ok
}

// IsValidationError checks if an error is a validation error
func IsValidationError(err error) bool {
	_, ok := err.(*ValidationError)
	return ok
}

// IsS52ComplianceError checks if an error is an S-52 compliance error
func IsS52ComplianceError(err error) bool {
	_, ok := err.(*S52ComplianceError)
	return ok
}

// ExtractParseError extracts parser context from any error
func ExtractParseError(err error) (*ParseError, bool) {
	if parseErr, ok := err.(*ParseError); ok {
		return parseErr, true
	}

	if validationErr, ok := err.(*ValidationError); ok {
		return validationErr.ParseError, true
	}

	if s52Err, ok := err.(*S52ComplianceError); ok {
		return s52Err.ParseError, true
	}

	if coordErr, ok := err.(*CoordinateError); ok {
		return coordErr.ParseError, true
	}

	if colorErr, ok := err.(*ColorError); ok {
		return colorErr.ParseError, true
	}

	if cmdErr, ok := err.(*CommandError); ok {
		return cmdErr.ParseError, true
	}

	return nil, false
}

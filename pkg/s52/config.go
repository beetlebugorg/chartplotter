package s52

import "fmt"

// ParserConfig holds parser configuration
type ParserConfig struct {
	MaxErrors int // Maximum errors before stopping (0 = unlimited)
}

// DefaultParserConfig returns default configuration
func DefaultParserConfig() *ParserConfig {
	return &ParserConfig{
		MaxErrors: 0,
	}
}

// ErrorCode represents different types of parsing errors
type ErrorCode string

const (
	ErrorCodeParseFileNotFound         ErrorCode = "PARSE_FILE_NOT_FOUND"
	ErrorCodeParseInvalidColor         ErrorCode = "PARSE_INVALID_COLOR"
	ErrorCodeParseInvalidCommand       ErrorCode = "PARSE_INVALID_COMMAND"
	ErrorCodeParseInvalidCoordinate    ErrorCode = "PARSE_INVALID_COORDINATE"
	ErrorCodeValidationFailed          ErrorCode = "VALIDATION_FAILED"
	ErrorCodeValidationS52NonCompliant ErrorCode = "VALIDATION_S52_NON_COMPLIANT"
)

// DAIError represents a DAI parsing error
type DAIError struct {
	Code    ErrorCode
	Message string
	Cause   error
}

func (e DAIError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// ErrorAggregator collects multiple errors
type ErrorAggregator struct {
	Errors    []error
	maxErrors int
}

func NewErrorAggregator() *ErrorAggregator {
	return &ErrorAggregator{
		Errors:    make([]error, 0),
		maxErrors: 0,
	}
}

func (a *ErrorAggregator) Add(err error) {
	if err != nil {
		a.Errors = append(a.Errors, err)
	}
}

func (a *ErrorAggregator) HasErrors() bool {
	return len(a.Errors) > 0
}

func (a *ErrorAggregator) Error() string {
	if len(a.Errors) == 0 {
		return ""
	}
	if len(a.Errors) == 1 {
		return a.Errors[0].Error()
	}
	return fmt.Sprintf("%d errors occurred", len(a.Errors))
}

func WrapParseError(code ErrorCode, message string, cause error) error {
	return &DAIError{
		Code:    code,
		Message: message,
		Cause:   cause,
	}
}

// Cache stub for parser
type Cache struct{}

func NewCache() *Cache {
	return &Cache{}
}

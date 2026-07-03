package apperror

import "fmt"

// AppError is the single error shape returned by every 4xx/5xx response:
// { "error": "CODE", "message": "human readable", "details": <optional> }.
// It implements the standard `error` interface so it can be returned
// directly from services and matched with errors.As in handlers.
type AppError struct {
	Status  int         `json:"-"`
	Code    string      `json:"error"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

func (e *AppError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func New(status int, code, message string) *AppError {
	return &AppError{Status: status, Code: code, Message: message}
}

func WithDetails(status int, code, message string, details interface{}) *AppError {
	return &AppError{Status: status, Code: code, Message: message, Details: details}
}

// Common, reusable errors.
func BadRequest(code, message string) *AppError { return New(400, code, message) }
func Unauthorized(message string) *AppError     { return New(401, "UNAUTHORIZED", message) }
func Forbidden(code, message string) *AppError  { return New(403, code, message) }
func NotFound(code, message string) *AppError   { return New(404, code, message) }
func Conflict(code, message string) *AppError   { return New(409, code, message) }
func Internal(message string) *AppError         { return New(500, "INTERNAL_ERROR", message) }

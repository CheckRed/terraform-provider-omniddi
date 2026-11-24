package client

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

type notFounder interface{ NotFound() bool }

func IsNotFound(err error) bool {
	var nf notFounder
	return errors.As(err, &nf) && nf.NotFound()
}

type ObjectNotFoundError struct {
	What string
}

func NewNotFound(what string) error { return &ObjectNotFoundError{What: what} }

func (e *ObjectNotFoundError) Error() string {
	if e.What == "" {
		return "object not found"
	}
	return e.What + " not found"
}

func (e *ObjectNotFoundError) NotFound() bool { return true }

type ValidationIssue struct {
	Loc  []string
	Msg  string
	Type string
}

func (v ValidationIssue) String() string {
	if len(v.Loc) == 0 {
		return v.Msg
	}
	return strings.Join(v.Loc, ".") + ": " + v.Msg
}

type APIError struct {
	StatusCode       int
	Detail           string
	Code             string
	RequestID        string
	Method           string
	Path             string
	ValidationIssues []ValidationIssue
	structured       bool
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s returned HTTP %d", e.Method, e.Path, e.StatusCode)
	if e.Detail != "" {
		fmt.Fprintf(&b, ": %s", e.Detail)
	}
	if e.Code != "" {
		fmt.Fprintf(&b, " (code %s)", e.Code)
	}
	if e.RequestID != "" {
		fmt.Fprintf(&b, " [X-Request-ID %s]", e.RequestID)
	}
	return b.String()
}

func (e *APIError) NotFound() bool {
	if e.StatusCode != http.StatusNotFound {
		return false
	}
	synthetic := e.Method == "" && e.Path == ""
	object := e.structured && (e.Method == http.MethodGet || e.Method == http.MethodDelete) && e.Path != graphQLPath
	return synthetic || object
}

func (e *APIError) Duplicate() bool {
	switch e.StatusCode {
	case http.StatusConflict:
		return true
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return duplicateText.MatchString(e.Detail)
	}
	return false
}

var duplicateText = regexp.MustCompile(`(?i)already exists?|duplicate|already in use|must be unique`)

func IsDuplicate(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Duplicate()
}

func IsAmbiguousWriteFailure(err error) bool {
	var (
		transport *transportError
		apiErr    *APIError
		tooLarge  *ResponseTooLargeError
	)
	switch {
	case errors.As(err, &transport), errors.As(err, &tooLarge):
		return true
	case errors.As(err, &apiErr):
		return apiErr.StatusCode >= http.StatusInternalServerError
	}
	return false
}

type ApprovalRequiredError struct {
	RequestID     int64
	Detail        string
	Method        string
	Path          string
	HTTPRequestID string
	Adopts        bool
}

func (e *ApprovalRequiredError) Error() string {
	return fmt.Sprintf("approval required (request %d): %s", e.RequestID, e.Detail)
}

type PendingChangeError struct {
	RequestID int64
	Method    string
	Path      string
}

func (e *PendingChangeError) Error() string {
	return fmt.Sprintf("another change to this object is pending approval (request %d)", e.RequestID)
}

type ApprovalUntrackedError struct {
	Detail        string
	Method        string
	Path          string
	HTTPRequestID string
}

func (e *ApprovalUntrackedError) Error() string {
	return fmt.Sprintf("approval required but the backend returned no request id: %s", e.Detail)
}

type ApprovalRejectedError struct {
	RequestID int64
	Status    string
}

func (e *ApprovalRejectedError) Error() string {
	return fmt.Sprintf("approval request %d ended as %q", e.RequestID, e.Status)
}

type TaskFailedError struct {
	CorrelationID string
	TaskID        int64
	Logs          string
}

func (e *TaskFailedError) Error() string {
	msg := fmt.Sprintf("agent task %d (correlation id %s) failed", e.TaskID, e.CorrelationID)
	if e.Logs != "" {
		msg += ": " + e.Logs
	}
	return msg
}

type SyncTaskError struct {
	Err error
}

func (e *SyncTaskError) Error() string {
	return "the automatic follow-up sync did not finish: " + e.Err.Error()
}

func (e *SyncTaskError) Unwrap() error { return e.Err }

type NoTasksError struct {
	CorrelationID string
}

func (e *NoTasksError) Error() string {
	return fmt.Sprintf("no agent tasks found for correlation id %s", e.CorrelationID)
}

type TimeoutError struct {
	Waiting       string
	CorrelationID string
	RequestID     int64
	LastStatus    string
	Cause         error
}

func (e *TimeoutError) Error() string {
	msg := "timed out waiting for " + e.Waiting
	if e.LastStatus != "" {
		msg += " (last status: " + e.LastStatus + ")"
	}
	if e.Cause != nil {
		msg += ": " + e.Cause.Error()
	}
	return msg
}

func (e *TimeoutError) Unwrap() error { return e.Cause }

type MissingTaskHandleError struct {
	Method string
	Path   string
	Body   string
}

func (e *MissingTaskHandleError) Error() string {
	return fmt.Sprintf("%s %s was accepted but returned no correlation_id to confirm it was applied", e.Method, e.Path)
}

type ResponseTooLargeError struct {
	Method    string
	Path      string
	Limit     int64
	RequestID string
}

func (e *ResponseTooLargeError) Error() string {
	return fmt.Sprintf("%s %s: response exceeds %d bytes [X-Request-ID %s]", e.Method, e.Path, e.Limit, e.RequestID)
}

type PageOverflowError struct {
	Query    string
	PageSize int
	MaxPages int
}

func (e *PageOverflowError) Error() string {
	return fmt.Sprintf("%s returned more than %d items (%d pages of %d); narrow the query", e.Query, e.PageSize*e.MaxPages, e.MaxPages, e.PageSize)
}

type AuthError struct {
	Message    string
	StatusCode int
	RequestID  string
}

func (e *AuthError) Error() string {
	if e.RequestID != "" {
		return fmt.Sprintf("%s [X-Request-ID %s]", e.Message, e.RequestID)
	}
	return e.Message
}

type ConfigError struct {
	Message string
}

func (e *ConfigError) Error() string { return e.Message }

type GraphQLErrorItem struct {
	Message string
	Path    []any
	Code    string
}

type GraphQLError struct {
	Errors    []GraphQLErrorItem
	RequestID string
}

func (e *GraphQLError) Error() string {
	msgs := make([]string, 0, len(e.Errors))
	for _, item := range e.Errors {
		msgs = append(msgs, item.Message)
	}
	msg := "graphql: " + strings.Join(msgs, "; ")
	if e.RequestID != "" {
		msg += " [X-Request-ID " + e.RequestID + "]"
	}
	return msg
}

const graphQLNotFoundCode = "RESOURCE_NOT_FOUND"

func (e *GraphQLError) NotFound() bool {
	for _, item := range e.Errors {
		if item.Code == graphQLNotFoundCode {
			return true
		}
	}
	return false
}

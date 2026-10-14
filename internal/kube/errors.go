package kube

import (
	"encoding/json"
	"fmt"
	"strings"
)

// StatusError is a non-2xx response from the Kubernetes API.
type StatusError struct {
	Code    int
	Reason  string
	Message string
}

func (e *StatusError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("kubernetes API error (%d): %s", e.Code, e.Message)
	}
	return fmt.Sprintf("kubernetes API error (%d)", e.Code)
}

func statusCode(err error) int {
	if se, ok := err.(*StatusError); ok {
		return se.Code
	}
	return 0
}

func IsNotFound(err error) bool      { return statusCode(err) == 404 }
func IsAlreadyExists(err error) bool { return statusCode(err) == 409 }

func parseStatus(code int, body []byte) error {
	se := &StatusError{Code: code}
	var s struct {
		Reason  string `json:"reason"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &s) == nil {
		se.Reason = s.Reason
		se.Message = s.Message
	}
	if se.Message == "" && len(body) > 0 {
		se.Message = strings.TrimSpace(string(body))
	}
	return se
}

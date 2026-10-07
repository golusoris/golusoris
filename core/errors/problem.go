// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package errors

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"net/url"
)

const (
	// ProblemJSONMediaType is the RFC 9457 response media type.
	ProblemJSONMediaType  = "application/problem+json"
	problemTypeBase       = "https://golusoris.dev/errors/"
	internalProblemDetail = "internal server error"
)

// ProblemDetails is an RFC 9457 error response. Code, Message, and
// ErrorMessage preserve the framework's legacy response fields.
type ProblemDetails struct {
	Type         string `json:"type"`
	Title        string `json:"title"`
	Status       int    `json:"status"`
	Detail       string `json:"detail"`
	Instance     string `json:"instance"`
	Code         string `json:"code,omitempty"`
	Message      string `json:"message,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// ProblemFromError maps err to RFC 9457 Problem Details. fallbackStatus is
// used for errors without a golusoris Code. Server-error details are always
// replaced so causes and unsafe messages cannot reach clients.
func ProblemFromError(err error, fallbackStatus int, instance string) ProblemDetails {
	status := fallbackStatus
	if status < http.StatusBadRequest || status > 599 {
		status = http.StatusInternalServerError
	}
	detail := http.StatusText(status)
	if err != nil {
		detail = err.Error()
	}
	if status >= http.StatusInternalServerError {
		detail = internalProblemDetail
	}
	problem := ProblemDetails{
		Type:         "about:blank",
		Title:        http.StatusText(status),
		Status:       status,
		Detail:       detail,
		Instance:     instance,
		ErrorMessage: detail,
	}

	var coded *Error
	if err != nil && stderrors.As(err, &coded) {
		status = coded.Status()
		detail = coded.Message
		if status >= http.StatusInternalServerError {
			detail = internalProblemDetail
		}
		code := string(coded.Code)
		problem = ProblemDetails{
			Type:         problemTypeBase + url.PathEscape(code),
			Title:        http.StatusText(status),
			Status:       status,
			Detail:       detail,
			Instance:     instance,
			Code:         code,
			Message:      detail,
			ErrorMessage: detail,
		}
	}
	return problem
}

// WriteProblem writes a ProblemDetails response with the RFC 9457 media type.
func WriteProblem(w http.ResponseWriter, problem ProblemDetails) error {
	w.Header().Set("Content-Type", ProblemJSONMediaType)
	w.WriteHeader(problem.Status)
	if err := json.NewEncoder(w).Encode(problem); err != nil {
		return fmt.Errorf("errors: encode problem details: %w", err)
	}
	return nil
}

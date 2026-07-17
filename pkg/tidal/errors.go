package tidal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// errorBodyLimit caps how many bytes of a non-success response body are read
// when extracting an error code, so a hostile or oversized body cannot exhaust
// memory.
const errorBodyLimit int64 = 16384

// APIError is returned by [Client.Do] when the TIDAL API answers with a status
// outside the 2xx range. It carries the HTTP status and, when the response body
// is JSON, a machine-readable error code.
type APIError struct {
	// Status is the HTTP status code of the failing response.
	Status int
	// Code is the machine-readable error code parsed from the response body.
	// It is empty when the body carries no recognizable code.
	Code string
}

// Error implements the error interface.
func (e *APIError) Error() string {
	return fmt.Sprintf("tidal: api error: status=%d code=%q", e.Status, e.Code)
}

// newAPIError builds an [APIError] from a non-success response. It reads at most
// [errorBodyLimit] bytes of the body to recover a "code" or "error" field; the
// caller retains ownership of resp.Body and is responsible for closing it.
func newAPIError(resp *http.Response) *APIError {
	var body struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, errorBodyLimit)).Decode(&body)

	code := body.Code
	if code == "" {
		code = body.Error
	}
	return &APIError{Status: resp.StatusCode, Code: code}
}

// errorHandler is a [github.com/hashicorp/go-retryablehttp.ErrorHandler]. It is
// invoked once the retry loop is exhausted, carrying the final response and
// error. On a non-nil response it returns the same [*APIError] the normal
// non-2xx path builds via [newAPIError] and closes the body, so a persistent
// retryable status (429/503) surfaces as a typed error identical to a
// persistent 403. On a transport failure (nil response), or a context
// cancellation that coincides with a final response, it passes the error through
// unchanged so cancellation stays classifiable via errors.Is and [Client.do] wraps it.
func errorHandler(resp *http.Response, err error, _ int) (*http.Response, error) {
	if resp == nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return nil, err
	}
	return nil, newAPIError(resp)
}

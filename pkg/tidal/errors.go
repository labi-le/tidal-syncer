package tidal

import (
	"encoding/json"
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

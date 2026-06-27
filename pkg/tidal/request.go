package tidal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// getJSON performs an authenticated GET against path with the given query and
// decodes the JSON response body into dst. The response body is always closed.
// A non-2xx response is returned as the [*APIError] produced by [Client.Do],
// unwrapped so callers can classify it with errors.As.
func (c *Client) getJSON(ctx context.Context, path string, query url.Values, dst any) error {
	resp, err := c.Do(ctx, http.MethodGet, path, query)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if err = json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("tidal: decode %s: %w", path, err)
	}
	return nil
}

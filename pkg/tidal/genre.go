package tidal

import (
	"context"
	"net/http"
	"net/url"
)

// jsonAPIMediaType is the JSON:API content type the openapi v2 API requires in
// the Accept header; the v1 API uses plain application/json instead.
const jsonAPIMediaType = "application/vnd.api+json"

const (
	// includeParam sideloads related resources into a JSON:API response.
	includeParam = "include"
	// includeGenres is the relationship name requested to sideload genres.
	includeGenres = "genres"
	// genresType is the JSON:API resource type that carries a genre name.
	genresType = "genres"
)

// genreDocument is the slice of a v2 JSON:API track document this client reads:
// the sideloaded resources in the top-level "included" array. Genre names live
// on the "genres"-type resources there; the primary "data" resource only
// references them.
type genreDocument struct {
	Included []includedResource `json:"included"`
}

// includedResource is one sideloaded JSON:API resource. Only "genres"-type
// resources carry a genre name; every other included type is ignored.
type includedResource struct {
	Type       string `json:"type"`
	Attributes struct {
		GenreName string `json:"genreName"`
	} `json:"attributes"`
}

// genreNames returns the genre names in document order, skipping non-genre
// resources and empty names.
func (d genreDocument) genreNames() []string {
	var names []string
	for _, res := range d.Included {
		if res.Type != genresType || res.Attributes.GenreName == "" {
			continue
		}
		names = append(names, res.Attributes.GenreName)
	}
	return names
}

// TrackGenres returns the genres TIDAL associates with a track, read from the
// openapi v2 catalog because the v1 API does not expose genre. The result is
// empty when TIDAL has no genre for the track; multiple genres are returned in
// API order. A taken-down or unavailable track is reported as
// [ErrTrackUnavailable], letting callers treat it as "no genre" rather than abort.
func (c *Client) TrackGenres(ctx context.Context, id string) ([]string, error) {
	query := url.Values{includeParam: {includeGenres}}
	var doc genreDocument
	if err := c.getJSONV2(ctx, tracksPath+id, query, &doc); err != nil {
		return nil, asUnavailable(err)
	}
	return doc.genreNames(), nil
}

// getJSONV2 performs an authenticated GET against the openapi v2 API and decodes
// the JSON:API response body into dst. The response body is always closed.
func (c *Client) getJSONV2(ctx context.Context, path string, query url.Values, dst any) error {
	resp, err := c.do(ctx, apiRequest{
		method:  http.MethodGet,
		baseURL: c.v2BaseURL,
		accept:  jsonAPIMediaType,
		path:    path,
		query:   query,
	})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	return decodeJSON(resp, path, dst)
}

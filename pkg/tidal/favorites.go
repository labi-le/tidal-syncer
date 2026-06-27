package tidal

import (
	"context"
	"iter"
	"net/url"
	"strconv"
)

// favoritesPageSize is the number of favorite items requested per page. TIDAL
// caps the favorites page size at 100.
const favoritesPageSize = 100

// Favorite collection segments under /users/{userID}/favorites/.
const (
	favoriteKindTracks    = "tracks"
	favoriteKindAlbums    = "albums"
	favoriteKindArtists   = "artists"
	favoriteKindPlaylists = "playlists"
)

// favoriteEntry wraps one favorited item. TIDAL nests the entity under "item".
type favoriteEntry[T any] struct {
	// Item is the favorited entity.
	Item T `json:"item"`
}

// favoritesPage is one page of a favorites listing.
type favoritesPage[T any] struct {
	// Limit is the page size echoed by the server.
	Limit int `json:"limit"`
	// Offset is the zero-based index of the first item on the page.
	Offset int `json:"offset"`
	// TotalNumberOfItems is the size of the whole collection, used to detect the
	// final page.
	TotalNumberOfItems int `json:"totalNumberOfItems"`
	// Items are the entries on this page.
	Items []favoriteEntry[T] `json:"items"`
}

// FavoriteTracks streams the authenticated user's favorite tracks, fetching one
// page at a time so the whole library is never held in memory. Iteration stops
// at the first error, which is yielded once with a zero-value item.
func (c *Client) FavoriteTracks(ctx context.Context) iter.Seq2[Track, error] {
	return streamFavorites[Track](ctx, c, favoriteKindTracks)
}

// FavoriteAlbums streams the authenticated user's favorite albums one page at a
// time. See [Client.FavoriteTracks] for the streaming and error contract.
func (c *Client) FavoriteAlbums(ctx context.Context) iter.Seq2[Album, error] {
	return streamFavorites[Album](ctx, c, favoriteKindAlbums)
}

// FavoriteArtists streams the authenticated user's favorite artists one page at
// a time. See [Client.FavoriteTracks] for the streaming and error contract.
func (c *Client) FavoriteArtists(ctx context.Context) iter.Seq2[Artist, error] {
	return streamFavorites[Artist](ctx, c, favoriteKindArtists)
}

// FavoritePlaylists streams the authenticated user's favorite playlists one page
// at a time. See [Client.FavoriteTracks] for the streaming and error contract.
func (c *Client) FavoritePlaylists(ctx context.Context) iter.Seq2[Playlist, error] {
	return streamFavorites[Playlist](ctx, c, favoriteKindPlaylists)
}

// streamFavorites returns an iterator over a favorites collection. The user id
// is resolved lazily on first iteration; any error (token, transport, or
// decode) is yielded once with a zero-value item and ends the stream.
func streamFavorites[T any](ctx context.Context, c *Client, kind string) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		userID, err := c.UserID(ctx)
		if err != nil {
			var zero T
			yield(zero, err)
			return
		}
		emitFavorites[T](ctx, c, "/users/"+userID+"/favorites/"+kind, yield)
	}
}

// emitFavorites pages through the favorites collection at path, yielding each
// item in order. It stops on the first error, on an empty page, or once the
// reported total has been reached, holding at most one page in memory.
func emitFavorites[T any](ctx context.Context, c *Client, path string, yield func(T, error) bool) {
	for offset := 0; ; {
		page, err := fetchFavoritesPage[T](ctx, c, path, offset)
		if err != nil {
			var zero T
			yield(zero, err)
			return
		}
		for i := range page.Items {
			if !yield(page.Items[i].Item, nil) {
				return
			}
		}
		offset += len(page.Items)
		if len(page.Items) == 0 || offset >= page.TotalNumberOfItems {
			return
		}
	}
}

// fetchFavoritesPage fetches the favorites page at offset for the collection at
// path, requesting favoritesPageSize items.
func fetchFavoritesPage[T any](
	ctx context.Context, c *Client, path string, offset int,
) (favoritesPage[T], error) {
	query := url.Values{
		"limit":  {strconv.Itoa(favoritesPageSize)},
		"offset": {strconv.Itoa(offset)},
	}
	var page favoritesPage[T]
	if err := c.getJSON(ctx, path, query, &page); err != nil {
		return favoritesPage[T]{}, err
	}
	return page, nil
}

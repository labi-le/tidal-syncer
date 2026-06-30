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

// favoriteEntry wraps one favorited item. TIDAL nests the entity under "item"
// and stamps each entry with the instant it was favorited under "created".
type favoriteEntry[T any] struct {
	// Item is the favorited entity.
	Item T `json:"item"`
	// Created is the TIDAL timestamp at which the item was added to favorites.
	Created string `json:"created"`
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

// FavoriteTrack is a favorited track paired with the instant it was added to the
// user's favorites. AddedAt is a TIDAL timestamp such as
// 2024-04-12T21:51:19.759+0000 and is empty only when TIDAL omits it.
type FavoriteTrack struct {
	Track   Track
	AddedAt string
}

// FavoriteTracks streams the authenticated user's favorite tracks, each paired
// with its favorite-add date, fetching one page at a time so the whole library
// is never held in memory. Iteration stops at the first error, which is yielded
// once with a zero-value item.
func (c *Client) FavoriteTracks(ctx context.Context) iter.Seq2[FavoriteTrack, error] {
	return func(yield func(FavoriteTrack, error) bool) {
		for entry, err := range streamFavorites[Track](ctx, c, favoriteKindTracks) {
			if err != nil {
				yield(FavoriteTrack{}, err)
				return
			}
			if !yield(FavoriteTrack{Track: entry.Item, AddedAt: entry.Created}, nil) {
				return
			}
		}
	}
}

// FavoriteAlbums streams the authenticated user's favorite albums one page at a
// time. See [Client.FavoriteTracks] for the streaming and error contract.
func (c *Client) FavoriteAlbums(ctx context.Context) iter.Seq2[Album, error] {
	return favoriteItems[Album](ctx, c, favoriteKindAlbums)
}

// FavoriteArtists streams the authenticated user's favorite artists one page at
// a time. See [Client.FavoriteTracks] for the streaming and error contract.
func (c *Client) FavoriteArtists(ctx context.Context) iter.Seq2[Artist, error] {
	return favoriteItems[Artist](ctx, c, favoriteKindArtists)
}

// FavoritePlaylists streams the authenticated user's favorite playlists one page
// at a time. See [Client.FavoriteTracks] for the streaming and error contract.
func (c *Client) FavoritePlaylists(ctx context.Context) iter.Seq2[Playlist, error] {
	return favoriteItems[Playlist](ctx, c, favoriteKindPlaylists)
}

// favoriteItems streams just the favorited entities for kind, discarding the
// per-entry favorite-add date. It backs the album, artist and playlist streams,
// which do not need the date.
func favoriteItems[T any](ctx context.Context, c *Client, kind string) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		for entry, err := range streamFavorites[T](ctx, c, kind) {
			if err != nil {
				var zero T
				yield(zero, err)
				return
			}
			if !yield(entry.Item, nil) {
				return
			}
		}
	}
}

// streamFavorites returns an iterator over a favorites collection, yielding each
// entry with its favorite-add date. The user id is resolved lazily on first
// iteration; any error (token, transport, or decode) is yielded once with a
// zero-value entry and ends the stream.
func streamFavorites[T any](ctx context.Context, c *Client, kind string) iter.Seq2[favoriteEntry[T], error] {
	return func(yield func(favoriteEntry[T], error) bool) {
		userID, err := c.UserID(ctx)
		if err != nil {
			yield(favoriteEntry[T]{}, err)
			return
		}
		emitFavorites[T](ctx, c, "/users/"+userID+"/favorites/"+kind, yield)
	}
}

// emitFavorites pages through the favorites collection at path, yielding each
// entry in order. It stops on the first error, on an empty page, or once the
// reported total has been reached, holding at most one page in memory.
func emitFavorites[T any](
	ctx context.Context, c *Client, path string, yield func(favoriteEntry[T], error) bool,
) {
	for offset := 0; ; {
		page, err := fetchFavoritesPage[T](ctx, c, path, offset)
		if err != nil {
			yield(favoriteEntry[T]{}, err)
			return
		}
		for i := range page.Items {
			if !yield(page.Items[i], nil) {
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

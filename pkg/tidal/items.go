package tidal

import (
	"context"
	"iter"
)

// itemsSuffix is appended to an album or playlist resource path to list the
// tracks it contains, for example /albums/{id}/items.
const itemsSuffix = "/items"

// playlistsPath is the path prefix for playlist-scoped endpoints; the playlist
// UUID is interpolated after it, for example playlistsPath + uuid + itemsSuffix.
const playlistsPath = "/playlists/"

// AlbumTracks streams the tracks on the album identified by albumID, fetching
// one page at a time so a large album is never held in memory. The album-items
// endpoint returns the same paged {item:{...}} envelope as the favorites stream,
// so iteration reuses that pager: it stops at the first error, which is yielded
// once with a zero-value track. See [Client.FavoriteTracks] for the contract.
func (c *Client) AlbumTracks(ctx context.Context, albumID string) iter.Seq2[Track, error] {
	return func(yield func(Track, error) bool) {
		emitFavorites[Track](ctx, c, albumsPath+albumID+itemsSuffix, yield)
	}
}

// PlaylistTracks streams the tracks in the playlist identified by playlistUUID,
// fetching one page at a time. See [Client.AlbumTracks] for the streaming and
// error contract.
func (c *Client) PlaylistTracks(ctx context.Context, playlistUUID string) iter.Seq2[Track, error] {
	return func(yield func(Track, error) bool) {
		emitFavorites[Track](ctx, c, playlistsPath+playlistUUID+itemsSuffix, yield)
	}
}

package sync_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"iter"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	synceng "github.com/labi-le/tidal-syncer/internal/sync"
	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

var (
	_ synceng.TidalClient  = (*fakeClient)(nil)
	_ synceng.Downloader   = (*fakeDownloader)(nil)
	_ synceng.CoverFetcher = (*fakeCovers)(nil)
)

// seqOf adapts a slice into the error-carrying iterator the ports expect,
// yielding each element with a nil error.
func seqOf[T any](items []T) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		for _, item := range items {
			if !yield(item, nil) {
				return
			}
		}
	}
}

// fakeClient is an in-memory TidalClient driven entirely by its tables.
type fakeClient struct {
	userID         string
	favTracks      []tidal.Track
	favAlbums      []tidal.Album
	favPlaylists   []tidal.Playlist
	albumTracks    map[string][]tidal.Track
	playlistTracks map[string][]tidal.Track
	albums         map[string]tidal.Album
	lyrics         map[string]tidal.Lyrics
}

func (f *fakeClient) UserID(_ context.Context) (string, error) {
	return f.userID, nil
}

func (f *fakeClient) FavoriteTracks(_ context.Context) iter.Seq2[tidal.Track, error] {
	return seqOf(f.favTracks)
}

func (f *fakeClient) FavoriteAlbums(_ context.Context) iter.Seq2[tidal.Album, error] {
	return seqOf(f.favAlbums)
}

func (f *fakeClient) FavoritePlaylists(_ context.Context) iter.Seq2[tidal.Playlist, error] {
	return seqOf(f.favPlaylists)
}

func (f *fakeClient) AlbumTracks(_ context.Context, albumID string) iter.Seq2[tidal.Track, error] {
	return seqOf(f.albumTracks[albumID])
}

func (f *fakeClient) PlaylistTracks(_ context.Context, playlistUUID string) iter.Seq2[tidal.Track, error] {
	return seqOf(f.playlistTracks[playlistUUID])
}

func (f *fakeClient) Album(_ context.Context, id string) (tidal.Album, error) {
	album, ok := f.albums[id]
	if !ok {
		return tidal.Album{}, fmt.Errorf("fake: album %q not found", id)
	}

	return album, nil
}

func (f *fakeClient) Lyrics(_ context.Context, id string) (tidal.Lyrics, error) {
	return f.lyrics[id], nil
}

// fakeDownloader copies a real FLAC fixture to the destination, counting calls
// per track id and failing the ids listed in failIDs.
type fakeDownloader struct {
	src     string
	quality string
	failIDs map[string]bool
	calls   sync.Map
}

func (d *fakeDownloader) Download(_ context.Context, trackID, destPath string) (string, error) {
	recordCall(&d.calls, trackID)
	if d.failIDs[trackID] {
		return "", fmt.Errorf("fake: simulated download failure for track %s", trackID)
	}

	data, err := os.ReadFile(d.src)
	if err != nil {
		return "", err
	}
	if err = os.WriteFile(destPath, data, 0o600); err != nil {
		return "", err
	}

	return d.quality, nil
}

func (d *fakeDownloader) countFor(trackID string) int {
	return callCount(&d.calls, trackID)
}

// fakeCovers returns a fixed JPEG, counting fetches per cover uuid.
type fakeCovers struct {
	jpeg  []byte
	calls sync.Map
}

func (c *fakeCovers) Cover(_ context.Context, uuid string) ([]byte, error) {
	recordCall(&c.calls, uuid)

	return c.jpeg, nil
}

func (c *fakeCovers) countFor(uuid string) int {
	return callCount(&c.calls, uuid)
}

// recordCall atomically increments the per-key counter stored in m.
func recordCall(m *sync.Map, key string) {
	actual, _ := m.LoadOrStore(key, &atomic.Int64{})
	counter, _ := actual.(*atomic.Int64)
	counter.Add(1)
}

// callCount reads the per-key counter stored in m, returning zero when absent.
func callCount(m *sync.Map, key string) int {
	value, ok := m.Load(key)
	if !ok {
		return 0
	}
	counter, ok := value.(*atomic.Int64)
	if !ok {
		return 0
	}

	return int(counter.Load())
}

// minimalJPEG encodes a 1x1 image as a valid JPEG for cover-embedding tests.
func minimalJPEG(t *testing.T) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1)), nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}

	return buf.Bytes()
}

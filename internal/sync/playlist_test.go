package sync_test

import (
	"context"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/labi-le/tidal-syncer/internal/config"
	synceng "github.com/labi-le/tidal-syncer/internal/sync"
	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

var _ synceng.TidalClient = (*fakePlaylistClient)(nil)

// fakePlaylistClient is a TidalClient stub for playlist-export tests, driven by
// its favorite-playlist, per-playlist track and album tables. It counts Album
// fetches per id so a test can assert the per-run album cache.
type fakePlaylistClient struct {
	playlists      []tidal.Playlist
	playlistTracks map[string][]tidal.Track
	albums         map[string]tidal.Album
	albumCalls     map[string]int
}

func (f *fakePlaylistClient) UserID(_ context.Context) (string, error) {
	return "", nil
}

func (f *fakePlaylistClient) FavoriteTracks(_ context.Context) iter.Seq2[tidal.FavoriteTrack, error] {
	return seqOf[tidal.FavoriteTrack](nil)
}

func (f *fakePlaylistClient) FavoriteAlbums(_ context.Context) iter.Seq2[tidal.Album, error] {
	return seqOf[tidal.Album](nil)
}

func (f *fakePlaylistClient) FavoritePlaylists(_ context.Context) iter.Seq2[tidal.Playlist, error] {
	return seqOf(f.playlists)
}

func (f *fakePlaylistClient) AlbumTracks(_ context.Context, _ string) iter.Seq2[tidal.Track, error] {
	return seqOf[tidal.Track](nil)
}

func (f *fakePlaylistClient) PlaylistTracks(
	_ context.Context, playlistUUID string,
) iter.Seq2[tidal.Track, error] {
	return seqOf(f.playlistTracks[playlistUUID])
}

func (f *fakePlaylistClient) Album(_ context.Context, id string) (tidal.Album, error) {
	f.albumCalls[id]++
	album, ok := f.albums[id]
	if !ok {
		return tidal.Album{}, fmt.Errorf("fake: album %q not found", id)
	}

	return album, nil
}

func (f *fakePlaylistClient) Lyrics(_ context.Context, _ string) (tidal.Lyrics, error) {
	return tidal.Lyrics{}, nil
}

func (f *fakePlaylistClient) TrackGenres(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

// TestPlaylistWriterWritesM3U8WithRelativePaths exports a three-track playlist
// (two tracks sharing one album) and asserts the file lands at
// Playlists/<name>.m3u8 with an EXTM3U header, the exact relative audio paths,
// no entry escaping the music root, and a single fetch per album id.
func TestPlaylistWriterWritesM3U8WithRelativePaths(t *testing.T) {
	t.Parallel()

	client, cfg := newPlaylistFixture(t)
	writer := synceng.NewPlaylistWriter(client, cfg, zerolog.Nop())
	if err := writer.WritePlaylists(context.Background()); err != nil {
		t.Fatalf("WritePlaylists() error = %v, want nil", err)
	}

	playlistsDir := filepath.Join(cfg.Paths.Music, "Playlists")
	got := readPlaylistLines(t, filepath.Join(playlistsDir, "My Mix.m3u8"))

	if got[0] != "#EXTM3U" {
		t.Fatalf("first line = %q, want %q", got[0], "#EXTM3U")
	}

	wantLines := []string{
		"#EXTM3U",
		"#EXTINF:200,Artist One - Song One",
		"../Artist One/Album One/01 - Song One.flac",
		"#EXTINF:215,Artist One - Song Two",
		"../Artist One/Album One/02 - Song Two.flac",
		"#EXTINF:230,Artist Two - Song Three",
		"../Artist Two/Album Two/03 - Song Three.flac",
	}
	if !slices.Equal(got, wantLines) {
		t.Fatalf("playlist lines =\n%s\nwant:\n%s",
			strings.Join(got, "\n"), strings.Join(wantLines, "\n"))
	}

	assertNoEscape(t, cfg.Paths.Music, playlistsDir, pathLines(got))

	if calls := client.albumCalls["11"]; calls != 1 {
		t.Errorf("album 11 fetched %d times, want 1 (per-run cache)", calls)
	}
}

// newPlaylistFixture builds a one-playlist client and a config rooted at a fresh
// temp music directory. Tracks 101 and 102 share album 11 to exercise the cache.
func newPlaylistFixture(t *testing.T) (*fakePlaylistClient, config.Config) {
	t.Helper()

	cfg := config.Defaults()
	cfg.Paths.Music = t.TempDir()

	albumOne := tidal.Album{
		ID: 11, Title: "Album One", NumberOfVolumes: 1, ReleaseDate: "2020-01-01",
		Artists: []tidal.Artist{{ID: 1, Name: "Artist One", Type: "MAIN"}},
	}
	albumTwo := tidal.Album{
		ID: 22, Title: "Album Two", NumberOfVolumes: 1, ReleaseDate: "2021-01-01",
		Artists: []tidal.Artist{{ID: 2, Name: "Artist Two", Type: "MAIN"}},
	}
	tracks := []tidal.Track{
		{
			ID: 101, Title: "Song One", Duration: 200, TrackNumber: 1, VolumeNumber: 1,
			Artists: albumOne.Artists,
			Album:   tidal.AlbumRef{ID: 11, Title: "Album One"},
		},
		{
			ID: 102, Title: "Song Two", Duration: 215, TrackNumber: 2, VolumeNumber: 1,
			Artists: albumOne.Artists,
			Album:   tidal.AlbumRef{ID: 11, Title: "Album One"},
		},
		{
			ID: 103, Title: "Song Three", Duration: 230, TrackNumber: 3, VolumeNumber: 1,
			Artists: albumTwo.Artists,
			Album:   tidal.AlbumRef{ID: 22, Title: "Album Two"},
		},
	}

	return &fakePlaylistClient{
		playlists:      []tidal.Playlist{{UUID: "uuid-1", Title: "My Mix", NumberOfTracks: len(tracks)}},
		playlistTracks: map[string][]tidal.Track{"uuid-1": tracks},
		albums:         map[string]tidal.Album{"11": albumOne, "22": albumTwo},
		albumCalls:     map[string]int{},
	}, cfg
}

// readPlaylistLines reads path and splits it into trailing-newline-trimmed lines.
func readPlaylistLines(t *testing.T, path string) []string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read playlist %q: %v", path, err)
	}

	return strings.Split(strings.TrimRight(string(content), "\n"), "\n")
}

// pathLines returns the entry path lines of a playlist body: every line that is
// neither the #EXTM3U header nor an #EXTINF directive.
func pathLines(lines []string) []string {
	paths := make([]string, 0, len(lines))
	for _, line := range lines {
		if !strings.HasPrefix(line, "#") {
			paths = append(paths, line)
		}
	}

	return paths
}

// assertNoEscape fails when any entry is absolute or, once joined onto the
// Playlists directory, resolves outside the music root.
func assertNoEscape(t *testing.T, music, playlistsDir string, entries []string) {
	t.Helper()

	for _, entry := range entries {
		if filepath.IsAbs(entry) {
			t.Errorf("entry %q is absolute, want relative", entry)
		}
		resolved := filepath.Clean(filepath.Join(playlistsDir, filepath.FromSlash(entry)))
		if !strings.HasPrefix(resolved, music+string(os.PathSeparator)) {
			t.Errorf("entry %q escapes music root %q (resolved %q)", entry, music, resolved)
		}
	}
}

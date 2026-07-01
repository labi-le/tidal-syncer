package sync_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/labi-le/tidal-syncer/internal/config"
	"github.com/labi-le/tidal-syncer/internal/store"
	synceng "github.com/labi-le/tidal-syncer/internal/sync"
)

func TestFavoritesWriterWritesM3U8InAddOrder(t *testing.T) {
	t.Parallel()

	// Given three downloaded favorites added on different dates
	ctx := context.Background()
	st := newTestStore(t)
	cfg := baseConfig(t, config.Scope{Favorites: config.Favorites{Tracks: true}})
	favorites := []struct {
		id, title, addedAt, rel string
	}{
		{"1", "Oldest", "2026-06-01T00:00:00.000+0000", "Artist One/Album One/01 - Oldest.flac"},
		{"2", "Newest", "2026-06-30T00:00:00.000+0000", "Artist One/Album One/02 - Newest.flac"},
		{"3", "Middle", "2026-06-15T00:00:00.000+0000", "Artist Two/Album Two/03 - Middle.flac"},
	}
	items := make([]store.SnapshotItem, 0, len(favorites))
	for _, fav := range favorites {
		items = append(items, store.SnapshotItem{TidalID: fav.id, Name: fav.title, AddedAt: fav.addedAt})
	}
	if err := st.ReplaceSnapshot(ctx, synceng.SnapshotKindTracks, items); err != nil {
		t.Fatalf("replace snapshot: %v", err)
	}
	for _, fav := range favorites {
		if err := st.MarkTrack(ctx, store.Track{
			TidalID:          fav.id,
			Path:             filepath.Join(cfg.Paths.Music, filepath.FromSlash(fav.rel)),
			ObtainedQuality:  "LOSSLESS",
			RequestedQuality: "LOSSLESS",
			Status:           store.StatusDone,
		}); err != nil {
			t.Fatalf("mark track %s: %v", fav.id, err)
		}
	}

	// When exporting the favorites playlist
	if err := synceng.NewFavoritesWriter(st, cfg, zerolog.Nop()).WriteFavorites(ctx); err != nil {
		t.Fatalf("WriteFavorites() error = %v, want nil", err)
	}

	// Then Favorite Tracks.m3u8 lists the favorites newest-first as relative paths
	playlistsDir := filepath.Join(cfg.Paths.Music, "Playlists")
	got := readPlaylistLines(t, filepath.Join(playlistsDir, "Favorite Tracks.m3u8"))
	wantLines := []string{
		"#EXTM3U",
		"#EXTINF:-1,Newest",
		"../Artist One/Album One/02 - Newest.flac",
		"#EXTINF:-1,Middle",
		"../Artist Two/Album Two/03 - Middle.flac",
		"#EXTINF:-1,Oldest",
		"../Artist One/Album One/01 - Oldest.flac",
	}
	if !slices.Equal(got, wantLines) {
		t.Fatalf("playlist lines =\n%s\nwant:\n%s",
			strings.Join(got, "\n"), strings.Join(wantLines, "\n"))
	}

	assertNoEscape(t, cfg.Paths.Music, playlistsDir, pathLines(got))
}

func TestFavoritesWriterSkipsWhenNoDownloadedFavorites(t *testing.T) {
	t.Parallel()

	// Given a favorite that carries a date but was never downloaded (no file)
	ctx := context.Background()
	st := newTestStore(t)
	cfg := baseConfig(t, config.Scope{Favorites: config.Favorites{Tracks: true}})
	if err := st.ReplaceSnapshot(ctx, synceng.SnapshotKindTracks, []store.SnapshotItem{
		{TidalID: "1", Name: "Undownloaded", AddedAt: "2026-06-01T00:00:00.000+0000"},
	}); err != nil {
		t.Fatalf("replace snapshot: %v", err)
	}

	// When exporting the favorites playlist
	if err := synceng.NewFavoritesWriter(st, cfg, zerolog.Nop()).WriteFavorites(ctx); err != nil {
		t.Fatalf("WriteFavorites() error = %v, want nil", err)
	}

	// Then no playlist file is written
	dest := filepath.Join(cfg.Paths.Music, "Playlists", "Favorite Tracks.m3u8")
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("os.Stat(%q) err = %v, want not-exist", dest, err)
	}
}

package sync

import (
	gosync "sync"

	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

// albumCache memoizes album metadata and cover artwork by album id so a run
// fetches each album exactly once even when many tracks share it. It is safe
// for concurrent use; entries are stored by pointer so their sync.Once is never
// copied.
type albumCache struct {
	mu gosync.Mutex
	m  map[string]*albumEntry
}

// albumEntry is one memoized album resolution. The once guard ensures the
// loader function runs a single time regardless of how many goroutines race on
// the same id.
type albumEntry struct {
	once  gosync.Once
	album tidal.Album
	cover []byte
	err   error
}

// newAlbumCache returns an empty album cache ready for concurrent use.
func newAlbumCache() *albumCache {
	return &albumCache{mu: gosync.Mutex{}, m: make(map[string]*albumEntry)}
}

// load returns the album and cover for id, invoking fn exactly once per id and
// returning the memoized result (including any error) on every later call. The
// cache mutex is held only while looking up or inserting the entry, never while
// fn performs its I/O, so independent albums resolve in parallel.
func (c *albumCache) load(
	id string, fn func() (tidal.Album, []byte, error),
) (tidal.Album, []byte, error) {
	c.mu.Lock()
	entry, ok := c.m[id]
	if !ok {
		entry = &albumEntry{}
		c.m[id] = entry
	}
	c.mu.Unlock()

	entry.once.Do(func() {
		entry.album, entry.cover, entry.err = fn()
	})

	return entry.album, entry.cover, entry.err
}

package tidal

import "errors"

// ErrTrackUnavailable signals that a track exists in the user's library but
// cannot be downloaded because it was taken down or is unavailable in the
// account's region. Callers should skip the track and continue rather than
// abort the sync.
var ErrTrackUnavailable = errors.New("tidal: track unavailable")

// Endpoint path fragments shared across the endpoint methods. A request path is
// built by interpolating the resource id between a prefix and an optional
// suffix, for example tracksPath + id + playbackInfoSuffix.
const (
	tracksPath = "/tracks/"
	albumsPath = "/albums/"
)

// Artist is a recording artist credited on a track or album.
type Artist struct {
	// ID is the TIDAL artist id.
	ID int `json:"id"`
	// Name is the artist's display name.
	Name string `json:"name"`
	// Type distinguishes a primary ("MAIN") from a featured ("FEATURED") artist.
	Type string `json:"type"`
}

// AlbumRef is the lightweight album reference embedded in a [Track]. The full
// album record is fetched separately with [Client.Album].
type AlbumRef struct {
	// ID is the TIDAL album id.
	ID int `json:"id"`
	// Title is the album title.
	Title string `json:"title"`
	// Cover is the album cover image UUID; pass it to [CoverURL] to build a
	// download URL.
	Cover string `json:"cover"`
}

// Track is a single audio track and its metadata as returned by [Client.Track]
// and the favorite-tracks stream.
type Track struct {
	// ID is the TIDAL track id.
	ID int `json:"id"`
	// Title is the track title.
	Title string `json:"title"`
	// Version is an optional edition qualifier, for example "Remastered".
	Version string `json:"version"`
	// Duration is the track length in seconds.
	Duration int `json:"duration"`
	// TrackNumber is the track's position within its volume.
	TrackNumber int `json:"trackNumber"`
	// VolumeNumber is the disc (volume) the track belongs to.
	VolumeNumber int `json:"volumeNumber"`
	// ISRC is the International Standard Recording Code.
	ISRC string `json:"isrc"`
	// Copyright is the copyright notice.
	Copyright string `json:"copyright"`
	// Explicit reports whether the track carries an explicit-content flag.
	Explicit bool `json:"explicit"`
	// AudioQuality is the highest audio tier TIDAL advertises for the track.
	AudioQuality Quality `json:"audioQuality"`
	// Artists are the credited artists, primary first.
	Artists []Artist `json:"artists"`
	// Album references the album the track appears on.
	Album AlbumRef `json:"album"`
}

// Album is a full album record returned by [Client.Album].
type Album struct {
	// ID is the TIDAL album id.
	ID int `json:"id"`
	// Title is the album title.
	Title string `json:"title"`
	// Duration is the total album length in seconds.
	Duration int `json:"duration"`
	// NumberOfTracks is the count of tracks across every volume.
	NumberOfTracks int `json:"numberOfTracks"`
	// NumberOfVolumes is the number of discs (volumes) in the album.
	NumberOfVolumes int `json:"numberOfVolumes"`
	// ReleaseDate is the ISO-8601 release date, for example "2020-01-01".
	ReleaseDate string `json:"releaseDate"`
	// Copyright is the copyright notice.
	Copyright string `json:"copyright"`
	// UPC is the Universal Product Code barcode.
	UPC string `json:"upc"`
	// Cover is the cover image UUID; pass it to [CoverURL].
	Cover string `json:"cover"`
	// Artists are the credited album artists, primary first.
	Artists []Artist `json:"artists"`
}

// Playlist is a user playlist as returned by the favorite-playlists stream.
type Playlist struct {
	// UUID is the playlist's unique identifier.
	UUID string `json:"uuid"`
	// Title is the playlist title.
	Title string `json:"title"`
	// Description is the optional playlist description.
	Description string `json:"description"`
	// NumberOfTracks is the count of tracks in the playlist.
	NumberOfTracks int `json:"numberOfTracks"`
	// Duration is the total playlist length in seconds.
	Duration int `json:"duration"`
}

// Contributor is a single credited person within an album [Credit] role.
type Contributor struct {
	// Name is the contributor's name.
	Name string `json:"name"`
	// ID is the TIDAL contributor id, when known.
	ID int `json:"id"`
}

// Credit groups the contributors that share one production role, for example
// every "Producer" credit on an album.
type Credit struct {
	// Type is the role name, for example "Producer" or "Mixer".
	Type string `json:"type"`
	// Contributors are the people credited under Type.
	Contributors []Contributor `json:"contributors"`
}

// Lyrics holds a track's words in both plain and time-synced forms.
type Lyrics struct {
	// Plain is the unsynced lyric text.
	Plain string
	// LRC is the time-synced lyric text in LRC format, with the redundant space
	// after each timestamp removed.
	LRC string
}

// PlaybackInfo describes how to fetch a track's audio stream. The manifest is
// parsed by the manifest package; this package neither decodes nor downloads it.
type PlaybackInfo struct {
	// TrackID echoes the requested track id.
	TrackID int `json:"trackId"`
	// AudioQuality is the audio tier the manifest actually provides.
	AudioQuality Quality `json:"audioQuality"`
	// ManifestMimeType selects how Manifest must be parsed, for example
	// "application/dash+xml" or "application/vnd.tidal.bts".
	ManifestMimeType string `json:"manifestMimeType"`
	// Manifest is the base64-encoded playback manifest.
	Manifest string `json:"manifest"`
}

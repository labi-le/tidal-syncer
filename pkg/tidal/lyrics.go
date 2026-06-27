package tidal

import (
	"context"
	"regexp"
)

// lyricsSuffix is appended to a track path to request its lyrics.
const lyricsSuffix = "/lyrics"

// lrcTimestampSpaceRe matches an LRC timestamp tag immediately followed by a
// single space, for example "[01:02.789] ". TIDAL emits that redundant space;
// the capture group keeps the tag so it can be re-inserted without the space.
var lrcTimestampSpaceRe = regexp.MustCompile(`(\[\d{2}:\d{2}\.\d{2,3}]) `)

// lyricsResponse is the raw TIDAL lyrics payload. The synced text arrives under
// "subtitles" and the unsynced text under "lyrics".
type lyricsResponse struct {
	// Lyrics is the plain, unsynced lyric text.
	Lyrics string `json:"lyrics"`
	// Subtitles is the time-synced lyric text in LRC format.
	Subtitles string `json:"subtitles"`
}

// Lyrics fetches a track's lyrics by id, returning both the plain and the
// time-synced (LRC) forms. The redundant space TIDAL inserts after each LRC
// timestamp is stripped so the result is well-formed LRC.
func (c *Client) Lyrics(ctx context.Context, id string) (Lyrics, error) {
	var raw lyricsResponse
	if err := c.getJSON(ctx, tracksPath+id+lyricsSuffix, nil, &raw); err != nil {
		return Lyrics{}, err
	}
	return Lyrics{
		Plain: raw.Lyrics,
		LRC:   lrcTimestampSpaceRe.ReplaceAllString(raw.Subtitles, "$1"),
	}, nil
}

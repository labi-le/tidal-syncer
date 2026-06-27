package tidal

import "context"

// creditsSuffix is appended to an album path to request its credits list.
const creditsSuffix = "/credits"

// Album fetches a full album record by id.
func (c *Client) Album(ctx context.Context, id string) (Album, error) {
	var album Album
	if err := c.getJSON(ctx, albumsPath+id, nil, &album); err != nil {
		return Album{}, err
	}
	return album, nil
}

// AlbumCredits fetches the per-role contributor credits for an album by id.
func (c *Client) AlbumCredits(ctx context.Context, id string) ([]Credit, error) {
	var credits []Credit
	if err := c.getJSON(ctx, albumsPath+id+creditsSuffix, nil, &credits); err != nil {
		return nil, err
	}
	return credits, nil
}

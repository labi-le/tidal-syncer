package tag

import (
	"fmt"

	"go.senan.xyz/taglib"
)

// FrontCoverType is the TagLib picture-type label that maps onto the FLAC/ID3
// PICTURE type-3 ("front cover") byte.
const FrontCoverType = "Front Cover"

// WriteTags writes the Vorbis comment map to the FLAC at path, clearing any
// pre-existing comments so the file reflects exactly tags.
func WriteTags(path string, tags map[string][]string) error {
	if err := taglib.WriteTags(path, tags, taglib.Clear); err != nil {
		return fmt.Errorf("taglib write tags %q: %w", path, err)
	}

	return nil
}

// ReadTags reads every Vorbis comment from the FLAC at path.
func ReadTags(path string) (map[string][]string, error) {
	tags, err := taglib.ReadTags(path)
	if err != nil {
		return nil, fmt.Errorf("taglib read tags %q: %w", path, err)
	}

	return tags, nil
}

// WriteImage embeds data as a PICTURE type-3 front cover at index 0 with the
// given MIME type.
func WriteImage(path string, data []byte, mimeType string) error {
	if err := taglib.WriteImageOptions(path, data, 0, FrontCoverType, "", mimeType); err != nil {
		return fmt.Errorf("taglib write image %q: %w", path, err)
	}

	return nil
}

// ReadImage reads the first embedded image (index 0) from the FLAC at path.
func ReadImage(path string) ([]byte, error) {
	data, err := taglib.ReadImage(path)
	if err != nil {
		return nil, fmt.Errorf("taglib read image %q: %w", path, err)
	}

	return data, nil
}

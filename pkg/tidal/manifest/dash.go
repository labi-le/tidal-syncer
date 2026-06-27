package manifest

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// DASH segment-template constants.
const (
	// defaultStartNumber is the MPEG-DASH default applied when a
	// SegmentTemplate omits the startNumber attribute.
	defaultStartNumber = 1
	// initSegmentCount is the single initialization segment that precedes the
	// media segments.
	initSegmentCount = 1
	// baseSegmentsPerEntry is the segment described by an <S> element before
	// its repeat count is applied.
	baseSegmentsPerEntry = 1
	// numberPlaceholder is the SegmentTemplate token substituted with each
	// media segment number.
	numberPlaceholder = "$Number$"
)

// DASH is a parsed MPEG-DASH manifest. It exposes the codec MIME type and an
// ordered segment-URL builder via [DASH.SegmentURLs].
type DASH struct {
	mimeType       string
	initialization string
	media          string
	startNumber    int
	segmentCount   int
}

// MimeType reports the codec MIME type declared by the manifest's
// AdaptationSet (for example "audio/mp4").
func (d DASH) MimeType() string { return d.mimeType }

// SegmentURLs returns the ordered list of segment URLs to download: the
// initialization segment first, followed by each media segment in timeline
// order with the $Number$ template token substituted.
func (d DASH) SegmentURLs() []string {
	urls := make([]string, 0, initSegmentCount+d.segmentCount)
	urls = append(urls, d.initialization)

	for i := range d.segmentCount {
		number := d.startNumber + i
		urls = append(urls, strings.ReplaceAll(d.media, numberPlaceholder, strconv.Itoa(number)))
	}

	return urls
}

// parseDASH decodes and validates a base64-encoded MPEG-DASH manifest.
func parseDASH(base64Manifest string) (Manifest, error) {
	raw, err := decodeBase64(base64Manifest)
	if err != nil {
		return Manifest{}, err
	}

	var doc mpdDocument
	if err = xml.Unmarshal(raw, &doc); err != nil {
		return Manifest{}, fmt.Errorf("manifest: decode dash xml: %w", err)
	}

	dash, err := doc.toDASH()
	if err != nil {
		return Manifest{}, err
	}

	return Manifest{kind: KindDASH, dash: dash}, nil
}

// mpdDocument mirrors the subset of the MPEG-DASH MPD schema tidal-syncer
// consumes.
type mpdDocument struct {
	Periods []mpdPeriod `xml:"Period"`
}

// toDASH projects the parsed MPD onto a [DASH] result, selecting the first
// representation and validating its segment template.
func (d mpdDocument) toDASH() (DASH, error) {
	set, rep, ok := d.firstRepresentation()
	if !ok {
		return DASH{}, fmt.Errorf("manifest: dash has no representation: %w", ErrInvalidManifest)
	}

	tmpl := rep.SegmentTemplate
	if tmpl.Initialization == "" || tmpl.Media == "" {
		return DASH{}, fmt.Errorf("manifest: dash segment template incomplete: %w", ErrInvalidManifest)
	}

	startNumber := defaultStartNumber
	if tmpl.StartNumber != nil {
		startNumber = *tmpl.StartNumber
	}

	return DASH{
		mimeType:       set.MimeType,
		initialization: tmpl.Initialization,
		media:          tmpl.Media,
		startNumber:    startNumber,
		segmentCount:   tmpl.Timeline.totalSegments(),
	}, nil
}

// firstRepresentation returns the first AdaptationSet/Representation pair in
// document order, reporting false when the document contains none.
func (d mpdDocument) firstRepresentation() (mpdAdaptationSet, mpdRepresentation, bool) {
	for _, period := range d.Periods {
		for _, set := range period.AdaptationSets {
			for _, rep := range set.Representations {
				return set, rep, true
			}
		}
	}

	return mpdAdaptationSet{}, mpdRepresentation{}, false
}

type mpdPeriod struct {
	AdaptationSets []mpdAdaptationSet `xml:"AdaptationSet"`
}

type mpdAdaptationSet struct {
	MimeType        string              `xml:"mimeType,attr"`
	Representations []mpdRepresentation `xml:"Representation"`
}

type mpdRepresentation struct {
	SegmentTemplate mpdSegmentTemplate `xml:"SegmentTemplate"`
}

type mpdSegmentTemplate struct {
	Initialization string      `xml:"initialization,attr"`
	Media          string      `xml:"media,attr"`
	StartNumber    *int        `xml:"startNumber,attr"`
	Timeline       mpdTimeline `xml:"SegmentTimeline"`
}

// mpdTimeline is a SegmentTimeline: an ordered run of <S> entries whose repeat
// counts determine the total media-segment count.
type mpdTimeline struct {
	Segments []mpdSegment `xml:"S"`
}

// totalSegments sums the media-segment count described by the timeline, where
// each <S> element contributes one segment plus its repeat count.
func (t mpdTimeline) totalSegments() int {
	total := 0
	for _, segment := range t.Segments {
		total += baseSegmentsPerEntry + segment.Repeat
	}

	return total
}

type mpdSegment struct {
	Repeat int `xml:"r,attr"`
}

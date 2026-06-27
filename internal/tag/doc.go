// Package tag writes audio metadata and embedded cover art onto downloaded
// tracks.
//
// The download pipeline calls [TagFile] to stamp Vorbis comments or ID3
// frames and a front-cover image onto a finished media file. The lower-level
// helpers [WriteTags], [ReadTags], [WriteImage] and [ReadImage] wrap the
// underlying TagLib bindings and back both [TagFile] and the package tests.
package tag

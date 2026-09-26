// Package pmtiles decodes and validates PMTiles v3 archives within explicit
// limits. It has no HTTP dependency: callers supply an io.ReaderAt.
package pmtiles

import (
	"errors"
	"fmt"
)

// Code is a stable, machine-readable diagnostic identifier.
type Code string

// Diagnostic codes. These strings are part of the public contract (manifest,
// inspect JSON, probe reports) and must not change meaning.
const (
	CodeTruncatedHeader        Code = "truncated_header"
	CodeBadMagic               Code = "bad_magic"
	CodeUnsupportedVersion     Code = "unsupported_version"
	CodeSectionOutOfBounds     Code = "section_out_of_bounds"
	CodeRootTooFar             Code = "root_directory_too_far"
	CodeInvalidZoomRange       Code = "invalid_zoom_range"
	CodeUnsupportedCompression Code = "unsupported_compression"
	CodeUnknownCompression     Code = "unknown_compression"
	CodeDecompressionFailed    Code = "decompression_failed"
	CodeDecompressedSizeLimit  Code = "decompressed_size_limit"
	CodeDirectoryTooLarge      Code = "directory_too_large"
	CodeVarintOverflow         Code = "varint_overflow"
	CodeTruncatedDirectory     Code = "truncated_directory"
	CodeEmptyDirectory         Code = "empty_directory"
	CodeTooManyEntries         Code = "too_many_entries"
	CodeZeroLengthEntry        Code = "zero_length_entry"
	CodeInvalidOffset          Code = "invalid_offset"
	CodeTileIDOverflow         Code = "tile_id_overflow"
	CodeEntryOutOfBounds       Code = "entry_out_of_bounds"
	CodeDepthExceeded          Code = "directory_depth_exceeded"
	CodeEntryLimit             Code = "entry_limit_exceeded"
	CodeInvalidMetadata        Code = "invalid_metadata"
	CodeInvalidTileCoord       Code = "invalid_tile_coordinate"
	CodeFileTooLarge           Code = "file_too_large"
)

// Error is a structural diagnostic about archive bytes.
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Msg }

func errf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// CodeOf returns the diagnostic code of err, or "" if err is not a *Error
// (for example an I/O error from the underlying reader).
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package filestore

import (
	"bytes"
	"context"
	"fmt"
)

// The line index is the per-part newline count stored alongside each part row
// (written at flush time). Cumulative sums over partidx locate the part that
// contains a given logical line, so line->byte addressing decompresses at most
// the two boundary parts instead of scanning the whole file.
//
// Line numbers are retained-relative: line 0 starts at DataStartIdx (for
// unlimited segmented files that start never moves, making them absolute).
// Counts come from flushed parts only, so totals lag the live tail by at most
// one flush interval; byte resolution reads parts through the cache, which is
// safe because segmented files are append-only (a flushed newline never moves).

// EnsureLineIndex backfills newline counts for part rows written before the
// line index existed (linecount < 0).
func (s *FileStore) EnsureLineIndex(ctx context.Context, zoneId string, name string) error {
	file, err := s.Stat(ctx, zoneId, name)
	if err != nil {
		return err
	}
	if file.Opts.Circular {
		return fmt.Errorf("line index is not supported for circular files")
	}
	return dbBackfillLineCounts(ctx, zoneId, name, file.Opts.Compressed)
}

// GetTotalLineCount returns the number of newlines in the retained, flushed
// portion of the file.
func (s *FileStore) GetTotalLineCount(ctx context.Context, zoneId string, name string) (int64, error) {
	_, counts, err := s.loadLineIndex(ctx, zoneId, name)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, pc := range counts {
		total += int64(pc.LineCount)
	}
	return total, nil
}

// LineRangeToByteRange resolves retained-relative logical lines
// [startLine, startLine+numLines) to the raw byte range [startOff, endOff).
// endOff is clamped to the end of the file when the range extends past the
// indexed lines (the caller gets the live tail bytes too).
func (s *FileStore) LineRangeToByteRange(ctx context.Context, zoneId string, name string, startLine int64, numLines int64) (int64, int64, error) {
	if startLine < 0 || numLines < 0 {
		return 0, 0, fmt.Errorf("startLine and numLines must be non-negative")
	}
	file, counts, err := s.loadLineIndex(ctx, zoneId, name)
	if err != nil {
		return 0, 0, err
	}
	startOff, err := s.lineStartOffset(ctx, file, counts, startLine)
	if err != nil {
		return 0, 0, err
	}
	endOff, err := s.lineStartOffset(ctx, file, counts, startLine+numLines)
	if err != nil {
		return 0, 0, err
	}
	return startOff, endOff, nil
}

// OffsetToLine returns the retained-relative logical line number containing
// the given raw byte offset.
func (s *FileStore) OffsetToLine(ctx context.Context, zoneId string, name string, offset int64) (int64, error) {
	file, counts, err := s.loadLineIndex(ctx, zoneId, name)
	if err != nil {
		return 0, err
	}
	dataStart := file.DataStartIdx()
	if offset <= dataStart {
		return 0, nil
	}
	if offset > file.Size {
		offset = file.Size
	}
	targetPart := int(offset / partDataSize)
	var line int64
	for _, pc := range counts {
		if pc.PartIdx >= targetPart {
			break
		}
		line += int64(pc.LineCount)
	}
	partStart := int64(targetPart) * partDataSize
	if offset > partStart {
		_, data, err := s.ReadAt(ctx, zoneId, name, partStart, offset-partStart)
		if err != nil {
			return 0, err
		}
		line += int64(bytes.Count(data, []byte{'\n'}))
	}
	return line, nil
}

// loadLineIndex stats the file, backfills legacy rows, and returns the
// per-part newline counts restricted to retained parts.
func (s *FileStore) loadLineIndex(ctx context.Context, zoneId string, name string) (*WaveFile, []partLineCount, error) {
	file, err := s.Stat(ctx, zoneId, name)
	if err != nil {
		return nil, nil, err
	}
	if file.Opts.Circular {
		return nil, nil, fmt.Errorf("line index is not supported for circular files")
	}
	err = dbBackfillLineCounts(ctx, zoneId, name, file.Opts.Compressed)
	if err != nil {
		return nil, nil, err
	}
	counts, err := dbGetPartLineCounts(ctx, zoneId, name)
	if err != nil {
		return nil, nil, err
	}
	// retention deletes part rows at flush time, so rows older than
	// DataStartIdx can linger between flushes -- drop them here
	firstSeg := int(file.DataStartIdx() / partDataSize)
	retained := counts[:0:0]
	for _, pc := range counts {
		if pc.PartIdx >= firstSeg {
			retained = append(retained, pc)
		}
	}
	return file, retained, nil
}

// lineStartOffset returns the raw byte offset where the given retained-relative
// line starts: line 0 starts at DataStartIdx, line L starts after the L-th
// newline. Offsets past the indexed lines clamp to file.Size.
func (s *FileStore) lineStartOffset(ctx context.Context, file *WaveFile, counts []partLineCount, line int64) (int64, error) {
	if line <= 0 {
		return file.DataStartIdx(), nil
	}
	var cum int64
	for _, pc := range counts {
		partLines := int64(pc.LineCount)
		if cum+partLines < line {
			cum += partLines
			continue
		}
		// the line-th newline lives in this part: scan for newline number
		// (line - cum) within it
		partStart := int64(pc.PartIdx) * partDataSize
		_, data, err := s.ReadAt(ctx, file.ZoneId, file.Name, partStart, partDataSize)
		if err != nil {
			return 0, err
		}
		need := line - cum
		for i, b := range data {
			if b != '\n' {
				continue
			}
			need--
			if need == 0 {
				return partStart + int64(i) + 1, nil
			}
		}
		// flushed count promised more newlines than the part holds -- should
		// not happen for append-only segmented files
		return 0, fmt.Errorf("line index inconsistent for %s:%s part %d", file.ZoneId, file.Name, pc.PartIdx)
	}
	return file.Size, nil
}

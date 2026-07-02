// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package filestore

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/wavetermdev/waveterm/pkg/wshrpc"
)

// each line is exactly lineLen bytes including the trailing newline
func makeLines(count int, lineLen int) []byte {
	var buf bytes.Buffer
	for i := 0; i < count; i++ {
		line := fmt.Sprintf("l%04d", i)
		for len(line) < lineLen-1 {
			line += "x"
		}
		buf.WriteString(line[:lineLen-1])
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

func TestLineIndex(t *testing.T) {
	initDb(t)
	defer cleanupDb(t)
	ctx := context.Background()
	zoneId := uuid.NewString()
	err := WFS.MakeFile(ctx, zoneId, "term", nil, wshrpc.FileOpts{Segmented: true, Compressed: true})
	if err != nil {
		t.Fatalf("error making file: %v", err)
	}
	// 20 lines x 12 bytes = 240 bytes across 50-byte parts
	data := makeLines(20, 12)
	err = WFS.AppendData(ctx, zoneId, "term", data)
	if err != nil {
		t.Fatalf("error appending: %v", err)
	}
	_, err = WFS.FlushCache(ctx)
	if err != nil {
		t.Fatalf("error flushing: %v", err)
	}
	total, err := WFS.GetTotalLineCount(ctx, zoneId, "term")
	if err != nil {
		t.Fatalf("error getting line count: %v", err)
	}
	if total != 20 {
		t.Errorf("expected 20 lines, got %d", total)
	}
	// line k starts at byte k*12
	startOff, endOff, err := WFS.LineRangeToByteRange(ctx, zoneId, "term", 5, 3)
	if err != nil {
		t.Fatalf("error resolving line range: %v", err)
	}
	if startOff != 60 || endOff != 96 {
		t.Errorf("expected [60,96), got [%d,%d)", startOff, endOff)
	}
	// verify the resolved range round-trips to the right content
	_, rangeData, err := WFS.ReadAt(ctx, zoneId, "term", startOff, endOff-startOff)
	if err != nil {
		t.Fatalf("error reading range: %v", err)
	}
	if !bytes.Equal(rangeData, data[60:96]) {
		t.Errorf("range content mismatch: %q", rangeData)
	}
	// range past the end clamps to file size
	_, endOff, err = WFS.LineRangeToByteRange(ctx, zoneId, "term", 18, 100)
	if err != nil {
		t.Fatalf("error resolving clamped range: %v", err)
	}
	if endOff != int64(len(data)) {
		t.Errorf("expected clamped end %d, got %d", len(data), endOff)
	}
	line, err := WFS.OffsetToLine(ctx, zoneId, "term", 61)
	if err != nil {
		t.Fatalf("error mapping offset to line: %v", err)
	}
	if line != 5 {
		t.Errorf("expected line 5 at offset 61, got %d", line)
	}
	line, err = WFS.OffsetToLine(ctx, zoneId, "term", 0)
	if err != nil || line != 0 {
		t.Errorf("expected line 0 at offset 0, got %d (err %v)", line, err)
	}
}

func TestLineIndexBackfill(t *testing.T) {
	initDb(t)
	defer cleanupDb(t)
	ctx := context.Background()
	zoneId := uuid.NewString()
	err := WFS.MakeFile(ctx, zoneId, "term", nil, wshrpc.FileOpts{Segmented: true, Compressed: true})
	if err != nil {
		t.Fatalf("error making file: %v", err)
	}
	err = WFS.AppendData(ctx, zoneId, "term", makeLines(10, 12))
	if err != nil {
		t.Fatalf("error appending: %v", err)
	}
	_, err = WFS.FlushCache(ctx)
	if err != nil {
		t.Fatalf("error flushing: %v", err)
	}
	// simulate legacy rows written before the line index existed
	err = WithTx(ctx, func(tx *TxWrap) error {
		tx.Exec("UPDATE db_file_data SET linecount = -1 WHERE zoneid = ?", zoneId)
		return nil
	})
	if err != nil {
		t.Fatalf("error clearing linecounts: %v", err)
	}
	total, err := WFS.GetTotalLineCount(ctx, zoneId, "term")
	if err != nil {
		t.Fatalf("error getting line count after backfill: %v", err)
	}
	if total != 10 {
		t.Errorf("expected 10 lines after backfill, got %d", total)
	}
}

func TestLineIndexWithRetention(t *testing.T) {
	initDb(t)
	defer cleanupDb(t)
	ctx := context.Background()
	zoneId := uuid.NewString()
	// maxsize 100 = 2 parts at the 50-byte test part size
	err := WFS.MakeFile(ctx, zoneId, "term", nil, wshrpc.FileOpts{Segmented: true, Compressed: true, MaxSize: 100})
	if err != nil {
		t.Fatalf("error making file: %v", err)
	}
	data := makeLines(30, 10) // 300 bytes = 6 parts, retention keeps the last 2
	err = WFS.AppendData(ctx, zoneId, "term", data)
	if err != nil {
		t.Fatalf("error appending: %v", err)
	}
	_, err = WFS.FlushCache(ctx)
	if err != nil {
		t.Fatalf("error flushing: %v", err)
	}
	file, err := WFS.Stat(ctx, zoneId, "term")
	if err != nil {
		t.Fatalf("error stating file: %v", err)
	}
	dataStart := file.DataStartIdx()
	if dataStart != 200 {
		t.Fatalf("expected data start 200, got %d", dataStart)
	}
	// retained bytes are [200,300) = 10 lines; line 0 starts at DataStartIdx
	total, err := WFS.GetTotalLineCount(ctx, zoneId, "term")
	if err != nil {
		t.Fatalf("error getting line count: %v", err)
	}
	if total != 10 {
		t.Errorf("expected 10 retained lines, got %d", total)
	}
	startOff, endOff, err := WFS.LineRangeToByteRange(ctx, zoneId, "term", 2, 2)
	if err != nil {
		t.Fatalf("error resolving line range: %v", err)
	}
	if startOff != 220 || endOff != 240 {
		t.Errorf("expected [220,240), got [%d,%d)", startOff, endOff)
	}
}

func TestUpdateFileMaxSize(t *testing.T) {
	initDb(t)
	defer cleanupDb(t)
	ctx := context.Background()
	zoneId := uuid.NewString()
	err := WFS.MakeFile(ctx, zoneId, "term", nil, wshrpc.FileOpts{Segmented: true, Compressed: true, MaxSize: 100})
	if err != nil {
		t.Fatalf("error making file: %v", err)
	}
	err = WFS.UpdateFileMaxSize(ctx, zoneId, "term", 0)
	if err != nil {
		t.Fatalf("error updating max size: %v", err)
	}
	WFS.clearCache()
	file, err := WFS.Stat(ctx, zoneId, "term")
	if err != nil {
		t.Fatalf("error stating file: %v", err)
	}
	if file.Opts.MaxSize != 0 {
		t.Errorf("expected maxsize 0 after update, got %d", file.Opts.MaxSize)
	}
	// non-multiple sizes round up to a part boundary
	err = WFS.UpdateFileMaxSize(ctx, zoneId, "term", 60)
	if err != nil {
		t.Fatalf("error updating max size: %v", err)
	}
	WFS.clearCache()
	file, err = WFS.Stat(ctx, zoneId, "term")
	if err != nil {
		t.Fatalf("error stating file: %v", err)
	}
	if file.Opts.MaxSize != 100 {
		t.Errorf("expected maxsize rounded to 100, got %d", file.Opts.MaxSize)
	}
}

func TestGetFileDiskUsage(t *testing.T) {
	initDb(t)
	defer cleanupDb(t)
	ctx := context.Background()
	zoneId := uuid.NewString()
	err := WFS.MakeFile(ctx, zoneId, "term", nil, wshrpc.FileOpts{Segmented: true, Compressed: true})
	if err != nil {
		t.Fatalf("error making file: %v", err)
	}
	err = WFS.AppendData(ctx, zoneId, "term", makeLines(20, 12))
	if err != nil {
		t.Fatalf("error appending: %v", err)
	}
	_, err = WFS.FlushCache(ctx)
	if err != nil {
		t.Fatalf("error flushing: %v", err)
	}
	usage, err := WFS.GetFileDiskUsage(ctx, zoneId, "term")
	if err != nil {
		t.Fatalf("error getting disk usage: %v", err)
	}
	if usage <= 0 {
		t.Errorf("expected positive disk usage, got %d", usage)
	}
}

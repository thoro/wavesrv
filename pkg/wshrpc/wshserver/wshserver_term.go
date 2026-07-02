// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package wshserver

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"github.com/google/uuid"
	"github.com/wavetermdev/waveterm/pkg/blockcontroller"
	"github.com/wavetermdev/waveterm/pkg/filestore"
	"github.com/wavetermdev/waveterm/pkg/util/ansistrip"
	"github.com/wavetermdev/waveterm/pkg/wavebase"
	"github.com/wavetermdev/waveterm/pkg/waveobj"
	"github.com/wavetermdev/waveterm/pkg/wcore"
	"github.com/wavetermdev/waveterm/pkg/wshrpc"
	"github.com/wavetermdev/waveterm/pkg/wstore"
)

const TermSearchMaxResults = 1000
const TermSearchMaxLineText = 200

// TermSearchScrollbackCommand scans a block's full on-disk term file
// (ANSI-stripped, streaming 64KB parts -- the file is never materialized) for
// a plain substring and returns byte-offset + line-number anchors.
func (ws *WshServer) TermSearchScrollbackCommand(ctx context.Context, data wshrpc.CommandTermSearchScrollbackData) (*wshrpc.CommandTermSearchScrollbackRtnData, error) {
	if _, err := uuid.Parse(data.BlockId); err != nil {
		return nil, fmt.Errorf("invalid blockid: %w", err)
	}
	file, err := filestore.WFS.Stat(ctx, data.BlockId, wavebase.BlockFile_Term)
	if err == fs.ErrNotExist {
		return &wshrpc.CommandTermSearchScrollbackRtnData{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("error stating term file: %w", err)
	}
	maxResults := data.MaxResults
	if maxResults <= 0 || maxResults > TermSearchMaxResults {
		maxResults = TermSearchMaxResults
	}
	needle := data.Query
	if !data.CaseSensitive {
		needle = strings.ToLower(needle)
	}
	rtn := &wshrpc.CommandTermSearchScrollbackRtnData{
		FileSize:     file.Size,
		DataStartIdx: file.DataStartIdx(),
	}
	scanner := ansistrip.NewLineScanner(rtn.DataStartIdx)
	matchLine := func(line ansistrip.Line) {
		if data.Query == "" || len(rtn.Matches) >= maxResults {
			return
		}
		hay := line.Text
		if !data.CaseSensitive {
			hay = strings.ToLower(hay)
		}
		if !strings.Contains(hay, needle) {
			return
		}
		text := line.Text
		if len(text) > TermSearchMaxLineText {
			text = text[:TermSearchMaxLineText]
		}
		rtn.Matches = append(rtn.Matches, wshrpc.TermSearchMatch{Offset: line.Offset, Line: line.Line, LineText: text})
	}
	for off := rtn.DataStartIdx; off < file.Size; off += filestore.DefaultPartDataSize {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		_, part, err := filestore.WFS.ReadAt(ctx, data.BlockId, wavebase.BlockFile_Term, off, filestore.DefaultPartDataSize)
		if err != nil {
			return nil, fmt.Errorf("error reading term file at %d: %w", off, err)
		}
		for _, line := range scanner.Feed(part) {
			matchLine(line)
		}
		if len(part) == 0 {
			break
		}
	}
	if last := scanner.Flush(); last != nil {
		matchLine(*last)
	}
	rtn.Truncated = len(rtn.Matches) >= maxResults
	rtn.TotalLines = scanner.NewlineCount()
	return rtn, nil
}

// TermClearBufferCommand truncates a block's persisted term file (and its
// serialized cache). works with or without a running controller; open
// terminals clear live via the published blockfile truncate event.
func (ws *WshServer) TermClearBufferCommand(ctx context.Context, blockId string) error {
	if _, err := uuid.Parse(blockId); err != nil {
		return fmt.Errorf("invalid blockid: %w", err)
	}
	return blockcontroller.HandleTruncateBlockFile(blockId)
}

// TermScrollbackListCommand enumerates every stored term scrollback buffer
// across all workspaces (plus orphaned filestore zones no tab references),
// with logical and compressed on-disk sizes.
func (ws *WshServer) TermScrollbackListCommand(ctx context.Context) ([]wshrpc.TermScrollbackEntry, error) {
	var results []wshrpc.TermScrollbackEntry
	seenBlockIds := make(map[string]bool)
	workspaceList, err := wcore.ListWorkspaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("error listing workspaces: %w", err)
	}
	for _, wsEntry := range workspaceList {
		wsData, err := wcore.GetWorkspace(ctx, wsEntry.WorkspaceId)
		if err != nil {
			return nil, fmt.Errorf("error getting workspace %s: %w", wsEntry.WorkspaceId, err)
		}
		for _, tabId := range wsData.TabIds {
			tab, err := wstore.DBMustGet[*waveobj.Tab](ctx, tabId)
			if err != nil {
				return nil, err
			}
			for _, blockId := range tab.BlockIds {
				seenBlockIds[blockId] = true
				entry, err := makeTermScrollbackEntry(ctx, blockId)
				if err != nil || entry == nil {
					continue
				}
				blk, err := wstore.DBMustGet[*waveobj.Block](ctx, blockId)
				if err == nil {
					entry.View = blk.Meta.GetString(waveobj.MetaKey_View, "")
					entry.Controller = blk.Meta.GetString(waveobj.MetaKey_Controller, "")
					entry.Connection = blk.Meta.GetString(waveobj.MetaKey_Connection, "")
				}
				entry.WorkspaceId = wsEntry.WorkspaceId
				entry.WorkspaceName = wsData.Name
				entry.TabId = tabId
				entry.TabName = tab.Name
				results = append(results, *entry)
			}
		}
	}
	zoneIds, err := filestore.WFS.GetAllZoneIds(ctx)
	if err != nil {
		return results, nil
	}
	for _, zoneId := range zoneIds {
		if seenBlockIds[zoneId] {
			continue
		}
		entry, err := makeTermScrollbackEntry(ctx, zoneId)
		if err != nil || entry == nil {
			continue
		}
		entry.Orphaned = true
		results = append(results, *entry)
	}
	return results, nil
}

// returns (nil, err) when the block has no term file
func makeTermScrollbackEntry(ctx context.Context, blockId string) (*wshrpc.TermScrollbackEntry, error) {
	wf, err := filestore.WFS.Stat(ctx, blockId, wavebase.BlockFile_Term)
	if err != nil {
		return nil, err
	}
	diskSize, err := filestore.WFS.GetFileDiskUsage(ctx, blockId, wavebase.BlockFile_Term)
	if err != nil {
		diskSize = 0
	}
	return &wshrpc.TermScrollbackEntry{
		BlockId:   blockId,
		Size:      wf.Size,
		DiskSize:  diskSize,
		Unlimited: wf.Opts.MaxSize == 0,
	}, nil
}

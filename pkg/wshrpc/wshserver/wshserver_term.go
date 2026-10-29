// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package wshserver

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"strings"

	"github.com/google/uuid"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/wavetermdev/waveterm/pkg/blockcontroller"
	"github.com/wavetermdev/waveterm/pkg/filestore"
	"github.com/wavetermdev/waveterm/pkg/util/ansistrip"
	"github.com/wavetermdev/waveterm/pkg/wavebase"
	"github.com/wavetermdev/waveterm/pkg/waveobj"
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
	// not wcore.ListWorkspaces: it skips unnamed workspaces, whose live blocks
	// would then be reported as orphaned (and deleted by "delete orphaned").
	workspaces, err := wstore.DBGetAllObjsByType[*waveobj.Workspace](ctx, waveobj.OType_Workspace)
	if err != nil {
		return nil, fmt.Errorf("error listing workspaces: %w", err)
	}
	for _, wsData := range workspaces {
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
				entry.WorkspaceId = wsData.OID
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

// TermDeleteOrphanedBuffersCommand deletes the whole filestore zone of every
// orphaned term buffer (as reported by TermScrollbackListCommand) whose block
// or job no longer exists. Unlike
// TermClearBufferCommand this removes the files instead of truncating them:
// nothing references these zones, so there is no live writer whose file opts
// must survive. Orphan status is recomputed here rather than trusting a
// client-supplied list, so a buffer that became referenced since the client
// last listed is never deleted. Returns the number of zones deleted.
func (ws *WshServer) TermDeleteOrphanedBuffersCommand(ctx context.Context) (int, error) {
	entries, err := ws.TermScrollbackListCommand(ctx)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, entry := range entries {
		if !entry.Orphaned {
			continue
		}
		// a still-existing block may be mid-creation or awaiting GC; its zone is
		// not ours to drop.
		if termZoneOwnerExists(ctx, entry.BlockId) {
			continue
		}
		if err := filestore.WFS.DeleteZone(ctx, entry.BlockId); err != nil {
			log.Printf("TermDeleteOrphanedBuffersCommand: error deleting zone %s: %v\n", entry.BlockId, err)
			continue
		}
		deleted++
	}
	log.Printf("TermDeleteOrphanedBuffersCommand: deleted %d orphaned buffer zones\n", deleted)
	return deleted, nil
}

// termZoneOwnerExists reports whether zoneId is still a block or a job (or the
// lookup failed). A durable job's output is a "term" file in a zone keyed by
// the job id, which no tab references, so a live job's zone is listed as
// orphaned; deleting it would also reset the job's reconnect stream seq.
func termZoneOwnerExists(ctx context.Context, zoneId string) bool {
	for _, otype := range []string{waveobj.OType_Block, waveobj.OType_Job} {
		exists, err := wstore.DBExistsORef(ctx, waveobj.MakeORef(otype, zoneId))
		if err != nil || exists {
			return true
		}
	}
	return false
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

// TermGetRunningCommandsCommand reports the child processes still running
// under the LOCAL shells of the given scope (one block, a tab's blocks, or
// every tab of a workspace) -- the "is an application still running here?"
// check behind close confirmations. Remote shells are skipped (their process
// tree is not inspectable from here), as are transient wsh helpers.
func (ws *WshServer) TermGetRunningCommandsCommand(ctx context.Context, data wshrpc.CommandTermGetRunningCommandsData) ([]wshrpc.TermRunningCommand, error) {
	var blockIds []string
	switch {
	case data.WorkspaceId != "":
		workspace, err := wstore.DBMustGet[*waveobj.Workspace](ctx, data.WorkspaceId)
		if err != nil {
			return nil, fmt.Errorf("error getting workspace: %w", err)
		}
		for _, tabId := range workspace.TabIds {
			tab, err := wstore.DBGet[*waveobj.Tab](ctx, tabId)
			if err != nil || tab == nil {
				continue
			}
			blockIds = append(blockIds, tab.BlockIds...)
		}
	case data.TabId != "":
		tab, err := wstore.DBMustGet[*waveobj.Tab](ctx, data.TabId)
		if err != nil {
			return nil, fmt.Errorf("error getting tab: %w", err)
		}
		blockIds = tab.BlockIds
	case data.BlockId != "":
		blockIds = []string{data.BlockId}
	default:
		return nil, fmt.Errorf("workspaceid, tabid, or blockid is required")
	}
	var rtn []wshrpc.TermRunningCommand
	for _, blockId := range blockIds {
		pid := blockcontroller.GetLocalShellPid(ctx, blockId)
		if pid <= 0 {
			continue
		}
		proc, err := process.NewProcess(int32(pid))
		if err != nil {
			continue
		}
		children, err := proc.Children()
		if err != nil {
			continue
		}
		for _, child := range children {
			name, err := child.Name()
			if err != nil || name == "" || name == "wsh" {
				continue
			}
			rtn = append(rtn, wshrpc.TermRunningCommand{BlockId: blockId, Pid: int(child.Pid), Command: name})
		}
	}
	return rtn, nil
}

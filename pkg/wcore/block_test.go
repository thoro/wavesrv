// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package wcore

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/wavetermdev/waveterm/pkg/waveobj"
	"github.com/wavetermdev/waveterm/pkg/wstore"
)

// Creates a Tab (with its LayoutState) plus one Block per requested block id, all
// persisted and cross-linked the way CreateBlock would leave them.
func makeTestTab(t *testing.T, ctx context.Context, tabId string, blockIds []string) *waveobj.Tab {
	t.Helper()
	layoutId := uuid.NewString()
	if err := wstore.DBInsert(ctx, &waveobj.LayoutState{OID: layoutId}); err != nil {
		t.Fatalf("failed to insert layout state: %v", err)
	}
	tab := &waveobj.Tab{OID: tabId, LayoutState: layoutId, BlockIds: blockIds}
	if err := wstore.DBInsert(ctx, tab); err != nil {
		t.Fatalf("failed to insert tab: %v", err)
	}
	for _, blockId := range blockIds {
		block := &waveobj.Block{
			OID:        blockId,
			ParentORef: waveobj.MakeORef(waveobj.OType_Tab, tabId).String(),
		}
		if err := wstore.DBInsert(ctx, block); err != nil {
			t.Fatalf("failed to insert block: %v", err)
		}
	}
	return tab
}

func pendingActions(t *testing.T, ctx context.Context, layoutId string) []waveobj.LayoutActionData {
	t.Helper()
	ls, err := wstore.DBGet[*waveobj.LayoutState](ctx, layoutId)
	if err != nil || ls == nil {
		t.Fatalf("failed to get layout state %s: %v", layoutId, err)
	}
	if ls.PendingBackendActions == nil {
		return nil
	}
	return *ls.PendingBackendActions
}

func TestMoveBlockToTab_MovesBlockAndQueuesActions(t *testing.T) {
	ctx := context.Background()
	tabA, tabB := uuid.NewString(), uuid.NewString()
	block1, block2 := uuid.NewString(), uuid.NewString()
	srcTab := makeTestTab(t, ctx, tabA, []string{block1, block2})
	dstTab := makeTestTab(t, ctx, tabB, []string{})
	ws := makeTestWorkspace(t, ctx, []string{tabA, tabB}, tabA)

	rtn, err := MoveBlockToTab(ctx, block1, tabB)
	if err != nil {
		t.Fatalf("MoveBlockToTab returned error: %v", err)
	}
	if rtn.SourceTabId != tabA || rtn.SourceWorkspaceId != ws.OID || rtn.DestWorkspaceId != ws.OID {
		t.Fatalf("unexpected result: %+v", rtn)
	}
	if rtn.NewSourceActiveTabId != "" {
		t.Fatalf("expected no source tab deletion, got new active tab %q", rtn.NewSourceActiveTabId)
	}

	srcReloaded, _ := wstore.DBGet[*waveobj.Tab](ctx, tabA)
	if len(srcReloaded.BlockIds) != 1 || srcReloaded.BlockIds[0] != block2 {
		t.Fatalf("expected source blockids [%s], got %v", block2, srcReloaded.BlockIds)
	}
	dstReloaded, _ := wstore.DBGet[*waveobj.Tab](ctx, tabB)
	if len(dstReloaded.BlockIds) != 1 || dstReloaded.BlockIds[0] != block1 {
		t.Fatalf("expected target blockids [%s], got %v", block1, dstReloaded.BlockIds)
	}
	blockReloaded, _ := wstore.DBGet[*waveobj.Block](ctx, block1)
	wantParent := waveobj.MakeORef(waveobj.OType_Tab, tabB).String()
	if blockReloaded.ParentORef != wantParent {
		t.Fatalf("expected block parent %q, got %q", wantParent, blockReloaded.ParentORef)
	}

	srcActions := pendingActions(t, ctx, srcTab.LayoutState)
	if len(srcActions) != 1 || srcActions[0].ActionType != LayoutActionDataType_RemoveNode ||
		srcActions[0].BlockId != block1 {
		t.Fatalf("expected one removenode action on source, got %+v", srcActions)
	}
	dstActions := pendingActions(t, ctx, dstTab.LayoutState)
	if len(dstActions) != 1 || dstActions[0].ActionType != LayoutActionDataType_Insert ||
		dstActions[0].BlockId != block1 || !dstActions[0].Focused {
		t.Fatalf("expected one focused insert action on target, got %+v", dstActions)
	}

	wsReloaded, _ := wstore.DBGet[*waveobj.Workspace](ctx, ws.OID)
	if wsReloaded.ActiveTabId != tabB {
		t.Fatalf("expected target tab %q active, got %q", tabB, wsReloaded.ActiveTabId)
	}
}

func TestMoveBlockToTab_DeletesEmptiedSourceTab(t *testing.T) {
	ctx := context.Background()
	tabA, tabB := uuid.NewString(), uuid.NewString()
	block1 := uuid.NewString()
	makeTestTab(t, ctx, tabA, []string{block1})
	makeTestTab(t, ctx, tabB, []string{})
	makeTestWorkspace(t, ctx, []string{tabA, tabB}, tabA)

	rtn, err := MoveBlockToTab(ctx, block1, tabB)
	if err != nil {
		t.Fatalf("MoveBlockToTab returned error: %v", err)
	}
	if rtn.NewSourceActiveTabId != tabB {
		t.Fatalf("expected new source active tab %q, got %q", tabB, rtn.NewSourceActiveTabId)
	}
	tabReloaded, _ := wstore.DBGet[*waveobj.Tab](ctx, tabA)
	if tabReloaded != nil {
		t.Fatalf("expected emptied source tab to be deleted, still present: %+v", tabReloaded)
	}
	blockReloaded, _ := wstore.DBGet[*waveobj.Block](ctx, block1)
	if blockReloaded == nil {
		t.Fatalf("expected moved block to survive the source tab deletion")
	}
}

func TestMoveBlockToTab_RejectsLastPaneOfOnlyTab(t *testing.T) {
	ctx := context.Background()
	tabA, tabB := uuid.NewString(), uuid.NewString()
	block1 := uuid.NewString()
	makeTestTab(t, ctx, tabA, []string{block1})
	makeTestTab(t, ctx, tabB, []string{})
	makeTestWorkspace(t, ctx, []string{tabA}, tabA)
	makeTestWorkspace(t, ctx, []string{tabB}, tabB)

	if _, err := MoveBlockToTab(ctx, block1, tabB); err == nil {
		t.Fatalf("expected error when moving the last pane out of a workspace's only tab")
	}
}

func TestMoveBlockToTab_RejectsSameTab(t *testing.T) {
	ctx := context.Background()
	tabA := uuid.NewString()
	block1 := uuid.NewString()
	makeTestTab(t, ctx, tabA, []string{block1})
	makeTestWorkspace(t, ctx, []string{tabA}, tabA)

	if _, err := MoveBlockToTab(ctx, block1, tabA); err == nil {
		t.Fatalf("expected error when source and target tabs are the same")
	}
}

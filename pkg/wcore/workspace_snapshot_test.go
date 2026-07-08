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

// makeSnapshotTab inserts a Tab whose LayoutState is a two-pane horizontal split, plus the two
// leaf Blocks, and links it into the given workspace. Returns the tab id and its block ids.
func makeSnapshotTab(t *testing.T, ctx context.Context, ws *waveobj.Workspace, name string, metas ...waveobj.MetaMapType) (string, []string) {
	t.Helper()
	tabId := uuid.NewString()
	layoutId := uuid.NewString()
	blockIds := make([]string, 0, len(metas))
	children := make([]any, 0, len(metas))
	for _, meta := range metas {
		blockId := uuid.NewString()
		blk := &waveobj.Block{OID: blockId, ParentORef: "tab:" + tabId, Meta: meta}
		if err := wstore.DBInsert(ctx, blk); err != nil {
			t.Fatalf("insert block: %v", err)
		}
		blockIds = append(blockIds, blockId)
		children = append(children, map[string]any{
			"id":   uuid.NewString(),
			"size": float64(10),
			"data": map[string]any{"blockId": blockId},
		})
	}
	root := map[string]any{
		"id":            uuid.NewString(),
		"flexDirection": "row",
		"size":          float64(10),
		"children":      children,
	}
	focused, _ := children[0].(map[string]any)["id"].(string)
	ls := &waveobj.LayoutState{OID: layoutId, RootNode: root, FocusedNodeId: focused}
	if err := wstore.DBInsert(ctx, ls); err != nil {
		t.Fatalf("insert layoutstate: %v", err)
	}
	tab := &waveobj.Tab{OID: tabId, Name: name, LayoutState: layoutId, BlockIds: blockIds}
	if err := wstore.DBInsert(ctx, tab); err != nil {
		t.Fatalf("insert tab: %v", err)
	}
	ws.TabIds = append(ws.TabIds, tabId)
	if ws.ActiveTabId == "" {
		ws.ActiveTabId = tabId
	}
	if err := wstore.DBUpdate(ctx, ws); err != nil {
		t.Fatalf("update workspace: %v", err)
	}
	return tabId, blockIds
}

// leafBlockIds walks a decoded RootNode tree and returns its leaf block ids in order.
func leafBlockIds(node any) []string {
	m, ok := node.(map[string]any)
	if !ok {
		return nil
	}
	if children, ok := m["children"].([]any); ok {
		var out []string
		for _, c := range children {
			out = append(out, leafBlockIds(c)...)
		}
		return out
	}
	if data, ok := m["data"].(map[string]any); ok {
		if bid, _ := data["blockId"].(string); bid != "" {
			return []string{bid}
		}
	}
	return nil
}

func TestSaveRestoreWorkspaceLayout_RoundTrip(t *testing.T) {
	ctx := context.Background()
	ws := makeTestWorkspace(t, ctx, nil, "")
	termMeta := waveobj.MetaMapType{waveobj.MetaKey_View: "term", waveobj.MetaKey_Controller: "shell"}
	webMeta := waveobj.MetaMapType{waveobj.MetaKey_View: "web", waveobj.MetaKey_Url: "https://example.com"}
	_, origBlocks := makeSnapshotTab(t, ctx, ws, "T1", termMeta, webMeta)

	if err := SaveWorkspaceLayout(ctx, ws.OID); err != nil {
		t.Fatalf("SaveWorkspaceLayout: %v", err)
	}

	// Drift: add a second tab. Reload first so the drift write preserves the saved-layout
	// meta (the local ws still has the pre-save Meta and would clobber it on DBUpdate).
	ws, _ = GetWorkspace(ctx, ws.OID)
	makeSnapshotTab(t, ctx, ws, "T2", termMeta)
	reloaded, _ := GetWorkspace(ctx, ws.OID)
	if len(reloaded.TabIds) != 2 {
		t.Fatalf("expected 2 tabs after drift, got %d", len(reloaded.TabIds))
	}

	if err := RestoreWorkspaceLayout(ctx, ws.OID); err != nil {
		t.Fatalf("RestoreWorkspaceLayout: %v", err)
	}

	restored, _ := GetWorkspace(ctx, ws.OID)
	if len(restored.TabIds) != 1 {
		t.Fatalf("expected 1 tab after restore (drift removed), got %d", len(restored.TabIds))
	}
	tab, _ := wstore.DBGet[*waveobj.Tab](ctx, restored.TabIds[0])
	if tab.Name != "T1" {
		t.Fatalf("restored tab name = %q, want T1", tab.Name)
	}
	if len(tab.BlockIds) != 2 {
		t.Fatalf("restored tab has %d blocks, want 2", len(tab.BlockIds))
	}
	// New block ids must differ from the originals.
	for _, nb := range tab.BlockIds {
		for _, ob := range origBlocks {
			if nb == ob {
				t.Fatalf("restored block %s reused an original id", nb)
			}
		}
	}
	// Block metas (view/url) must survive.
	views := map[string]string{}
	for _, bid := range tab.BlockIds {
		blk, _ := wstore.DBGet[*waveobj.Block](ctx, bid)
		v := blk.Meta.GetString(waveobj.MetaKey_View, "")
		views[v] = blk.Meta.GetString(waveobj.MetaKey_Url, "")
	}
	if _, ok := views["term"]; !ok {
		t.Fatalf("restored tab missing term pane; views=%v", views)
	}
	if views["web"] != "https://example.com" {
		t.Fatalf("restored web pane url = %q, want https://example.com", views["web"])
	}
	// Layout tree must have two leaves pointing at the new blocks, with a valid LeafOrder.
	ls, _ := wstore.DBGet[*waveobj.LayoutState](ctx, tab.LayoutState)
	leaves := leafBlockIds(ls.RootNode)
	if len(leaves) != 2 {
		t.Fatalf("restored layout has %d leaves, want 2", len(leaves))
	}
	if ls.LeafOrder == nil || len(*ls.LeafOrder) != 2 {
		t.Fatalf("restored layout LeafOrder invalid: %+v", ls.LeafOrder)
	}
	if ls.FocusedNodeId == "" {
		t.Fatalf("restored layout lost FocusedNodeId")
	}
}

func TestRestoreWorkspaceLayout_NoSnapshot(t *testing.T) {
	ctx := context.Background()
	ws := makeTestWorkspace(t, ctx, nil, "")
	makeSnapshotTab(t, ctx, ws, "T1", waveobj.MetaMapType{waveobj.MetaKey_View: "term"})
	if err := RestoreWorkspaceLayout(ctx, ws.OID); err == nil {
		t.Fatalf("expected error restoring a workspace with no saved layout")
	}
}

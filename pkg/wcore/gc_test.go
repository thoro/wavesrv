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

func makeTestBlock(t *testing.T, ctx context.Context, parentORef string) string {
	t.Helper()
	block := &waveobj.Block{
		OID:        uuid.NewString(),
		ParentORef: parentORef,
		Meta:       waveobj.MetaMapType{waveobj.MetaKey_View: "term"},
	}
	if err := wstore.DBInsert(ctx, block); err != nil {
		t.Fatalf("failed to insert block: %v", err)
	}
	return block.OID
}

func leafNode(blockId string) map[string]any {
	return map[string]any{
		"id":            uuid.NewString(),
		"size":          float64(10),
		"flexDirection": "column",
		"data":          map[string]any{"blockId": blockId},
	}
}

func TestGCOrphanedBlocks_RemovesOrphanAndPrunesLayout(t *testing.T) {
	ctx := context.Background()

	// A valid tab with two blocks tiled side by side: one valid, one orphan (empty
	// parentoref). Both are referenced from BlockIds and the layout tree.
	tabId := uuid.NewString()
	validBlock := makeTestBlock(t, ctx, waveobj.MakeORef(waveobj.OType_Tab, tabId).String())
	orphanBlock := makeTestBlock(t, ctx, "")

	// Patch the helper-created tab to carry our fixed tabId.
	layoutId := uuid.NewString()
	validLeaf := leafNode(validBlock)
	orphanLeaf := leafNode(orphanBlock)
	leafOrder := []waveobj.LeafOrderEntry{
		{NodeId: validLeaf["id"].(string), BlockId: validBlock},
		{NodeId: orphanLeaf["id"].(string), BlockId: orphanBlock},
	}
	layout := &waveobj.LayoutState{
		OID: layoutId,
		RootNode: map[string]any{
			"id":            "root",
			"size":          float64(10),
			"flexDirection": "row",
			"children":      []any{validLeaf, orphanLeaf},
		},
		LeafOrder: &leafOrder,
	}
	tab := &waveobj.Tab{
		OID:         tabId,
		Name:        "T1",
		LayoutState: layoutId,
		BlockIds:    []string{validBlock, orphanBlock},
	}
	if err := wstore.DBInsert(ctx, layout); err != nil {
		t.Fatalf("insert layout: %v", err)
	}
	if err := wstore.DBInsert(ctx, tab); err != nil {
		t.Fatalf("insert tab: %v", err)
	}

	if err := GCOrphanedBlocks(ctx); err != nil {
		t.Fatalf("GCOrphanedBlocks: %v", err)
	}

	// Orphan block is deleted, valid block survives.
	if gotOrphan, _ := wstore.DBGet[*waveobj.Block](ctx, orphanBlock); gotOrphan != nil {
		t.Fatalf("expected orphan block %s to be deleted, still present", orphanBlock)
	}
	gotValid, err := wstore.DBGet[*waveobj.Block](ctx, validBlock)
	if err != nil || gotValid == nil {
		t.Fatalf("expected valid block %s to survive, got %v (err %v)", validBlock, gotValid, err)
	}

	// Tab BlockIds pruned to just the valid block.
	gotTab, _ := wstore.DBGet[*waveobj.Tab](ctx, tabId)
	if len(gotTab.BlockIds) != 1 || gotTab.BlockIds[0] != validBlock {
		t.Fatalf("expected tab blockids [%s], got %v", validBlock, gotTab.BlockIds)
	}

	// Layout leaf order pruned, and the single-surviving-child root collapsed to the
	// valid leaf.
	gotLayout, _ := wstore.DBGet[*waveobj.LayoutState](ctx, layoutId)
	if gotLayout.LeafOrder == nil || len(*gotLayout.LeafOrder) != 1 || (*gotLayout.LeafOrder)[0].BlockId != validBlock {
		t.Fatalf("expected leaf order with only %s, got %v", validBlock, gotLayout.LeafOrder)
	}
	rootMap, ok := gotLayout.RootNode.(map[string]any)
	if !ok {
		t.Fatalf("expected root node to remain a node, got %T", gotLayout.RootNode)
	}
	if bid := nodeLeafBlockId(rootMap); bid != validBlock {
		t.Fatalf("expected collapsed root leaf to reference %s, got %q", validBlock, bid)
	}
}

func TestGCOrphanedBlocks_KeepsValidBlocksAndLayout(t *testing.T) {
	ctx := context.Background()
	tabId := uuid.NewString()
	b1 := makeTestBlock(t, ctx, waveobj.MakeORef(waveobj.OType_Tab, tabId).String())
	b2 := makeTestBlock(t, ctx, waveobj.MakeORef(waveobj.OType_Tab, tabId).String())

	layoutId := uuid.NewString()
	leaf1 := leafNode(b1)
	leaf2 := leafNode(b2)
	leafOrder := []waveobj.LeafOrderEntry{
		{NodeId: leaf1["id"].(string), BlockId: b1},
		{NodeId: leaf2["id"].(string), BlockId: b2},
	}
	layout := &waveobj.LayoutState{
		OID: layoutId,
		RootNode: map[string]any{
			"id": "root", "size": float64(10), "flexDirection": "row",
			"children": []any{leaf1, leaf2},
		},
		LeafOrder: &leafOrder,
	}
	tab := &waveobj.Tab{OID: tabId, Name: "T1", LayoutState: layoutId, BlockIds: []string{b1, b2}}
	if err := wstore.DBInsert(ctx, layout); err != nil {
		t.Fatalf("insert layout: %v", err)
	}
	if err := wstore.DBInsert(ctx, tab); err != nil {
		t.Fatalf("insert tab: %v", err)
	}
	layoutVerBefore := layout.Version
	tabVerBefore := tab.Version

	if err := GCOrphanedBlocks(ctx); err != nil {
		t.Fatalf("GCOrphanedBlocks: %v", err)
	}

	if got, _ := wstore.DBGet[*waveobj.Block](ctx, b1); got == nil {
		t.Fatalf("valid block %s was deleted", b1)
	}
	if got, _ := wstore.DBGet[*waveobj.Block](ctx, b2); got == nil {
		t.Fatalf("valid block %s was deleted", b2)
	}
	gotTab, _ := wstore.DBGet[*waveobj.Tab](ctx, tabId)
	if len(gotTab.BlockIds) != 2 {
		t.Fatalf("expected tab blockids untouched (2), got %v", gotTab.BlockIds)
	}
	// Nothing was orphaned, so the GC must not have rewritten (bumped) the objects.
	if gotTab.Version != tabVerBefore {
		t.Fatalf("expected tab version unchanged (%d), got %d", tabVerBefore, gotTab.Version)
	}
	gotLayout, _ := wstore.DBGet[*waveobj.LayoutState](ctx, layoutId)
	if gotLayout.Version != layoutVerBefore {
		t.Fatalf("expected layout version unchanged (%d), got %d", layoutVerBefore, gotLayout.Version)
	}
	if gotLayout.LeafOrder == nil || len(*gotLayout.LeafOrder) != 2 {
		t.Fatalf("expected 2 leaves untouched, got %v", gotLayout.LeafOrder)
	}
}

func TestGCOrphanedBlocks_DeletesBlockWithMissingParentTab(t *testing.T) {
	ctx := context.Background()
	// parentoref points at a tab that does not exist.
	orphan := makeTestBlock(t, ctx, waveobj.MakeORef(waveobj.OType_Tab, uuid.NewString()).String())
	if err := GCOrphanedBlocks(ctx); err != nil {
		t.Fatalf("GCOrphanedBlocks: %v", err)
	}
	if got, _ := wstore.DBGet[*waveobj.Block](ctx, orphan); got != nil {
		t.Fatalf("expected block with missing parent tab to be deleted")
	}
}

func TestGCOrphanedBlocks_KeepsSubBlockChainToTab(t *testing.T) {
	ctx := context.Background()
	tabId := uuid.NewString()
	tab := &waveobj.Tab{OID: tabId, Name: "T1", LayoutState: uuid.NewString(), BlockIds: []string{}}
	if err := wstore.DBInsert(ctx, tab); err != nil {
		t.Fatalf("insert tab: %v", err)
	}
	parent := makeTestBlock(t, ctx, waveobj.MakeORef(waveobj.OType_Tab, tabId).String())
	sub := makeTestBlock(t, ctx, waveobj.MakeORef(waveobj.OType_Block, parent).String())

	if err := GCOrphanedBlocks(ctx); err != nil {
		t.Fatalf("GCOrphanedBlocks: %v", err)
	}
	if got, _ := wstore.DBGet[*waveobj.Block](ctx, parent); got == nil {
		t.Fatalf("expected parent block %s to survive", parent)
	}
	if got, _ := wstore.DBGet[*waveobj.Block](ctx, sub); got == nil {
		t.Fatalf("expected sub-block %s (chain to tab) to survive", sub)
	}
}

func TestBlockResolvesToTab(t *testing.T) {
	tabId := uuid.NewString()
	tabIds := map[string]bool{tabId: true}
	blockA := &waveobj.Block{OID: uuid.NewString(), ParentORef: waveobj.MakeORef(waveobj.OType_Tab, tabId).String()}
	blockBadParse := &waveobj.Block{OID: uuid.NewString(), ParentORef: "not-an-oref"}
	blockEmpty := &waveobj.Block{OID: uuid.NewString(), ParentORef: ""}
	blockMissingTab := &waveobj.Block{OID: uuid.NewString(), ParentORef: waveobj.MakeORef(waveobj.OType_Tab, uuid.NewString()).String()}
	blockSub := &waveobj.Block{OID: uuid.NewString(), ParentORef: waveobj.MakeORef(waveobj.OType_Block, blockA.OID).String()}
	blocks := map[string]*waveobj.Block{
		blockA.OID:          blockA,
		blockBadParse.OID:   blockBadParse,
		blockEmpty.OID:      blockEmpty,
		blockMissingTab.OID: blockMissingTab,
		blockSub.OID:        blockSub,
	}
	cases := []struct {
		name    string
		blockId string
		want    bool
	}{
		{"valid-direct", blockA.OID, true},
		{"valid-subchain", blockSub.OID, true},
		{"bad-parse", blockBadParse.OID, false},
		{"empty-parent", blockEmpty.OID, false},
		{"missing-tab", blockMissingTab.OID, false},
		{"unknown-block", uuid.NewString(), false},
	}
	for _, tc := range cases {
		if got := blockResolvesToTab(tc.blockId, blocks, tabIds); got != tc.want {
			t.Fatalf("%s: blockResolvesToTab = %v, want %v", tc.name, got, tc.want)
		}
	}
}

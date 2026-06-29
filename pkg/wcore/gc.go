// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package wcore

import (
	"context"
	"fmt"
	"log"

	"github.com/wavetermdev/waveterm/pkg/waveobj"
	"github.com/wavetermdev/waveterm/pkg/wstore"
)

// maxBlockParentDepth caps how far a block's parentoref chain is walked when
// deciding whether it terminates at a live tab. It guards against a cyclic or
// pathologically deep chain (a real chain is block -> ... -> tab, only a couple
// of hops). It mirrors the iteration cap in wstore.DBFindTabForBlockId.
const maxBlockParentDepth = 10

// blockResolvesToTab walks a block's parentoref chain and reports whether it
// terminates at a tab that currently exists. A block is treated as orphaned (the
// function returns false) when its parentoref is empty/unparseable, points at a
// block that is missing from the store, points at a tab that no longer exists, or
// the chain fails to reach a real tab within maxBlockParentDepth hops.
func blockResolvesToTab(blockId string, blocks map[string]*waveobj.Block, tabIds map[string]bool) bool {
	curId := blockId
	for depth := 0; depth < maxBlockParentDepth; depth++ {
		block := blocks[curId]
		if block == nil {
			return false
		}
		oref, err := waveobj.ParseORef(block.ParentORef)
		if err != nil {
			return false
		}
		switch oref.OType {
		case waveobj.OType_Tab:
			return tabIds[oref.OID]
		case waveobj.OType_Block:
			curId = oref.OID
		default:
			return false
		}
	}
	return false
}

// GCOrphanedBlocks deletes blocks that cannot be traced to an existing tab and
// prunes every dangling/orphaned block id out of each tab's BlockIds list and its
// LayoutState node tree. It is safe to run at startup / workspace load: it removes
// ONLY genuinely orphaned blocks (empty or unparseable parentoref, or a parent
// chain that never reaches a live tab), never a block that resolves to a real tab.
//
// This is the self-heal for the "dead black pane" landmine: an orphaned block
// otherwise makes wstore.DBFindTabForBlockId fail with "bad block parent oref",
// which the bootstrap resolvers surface as a fatal startup error.
func GCOrphanedBlocks(ctx context.Context) error {
	blockList, err := wstore.DBGetAllObjsByType[*waveobj.Block](ctx, waveobj.OType_Block)
	if err != nil {
		return fmt.Errorf("error listing blocks: %w", err)
	}
	tabIdList, err := wstore.DBGetAllOIDsByType(ctx, waveobj.OType_Tab)
	if err != nil {
		return fmt.Errorf("error listing tabs: %w", err)
	}

	blocks := make(map[string]*waveobj.Block, len(blockList))
	for _, b := range blockList {
		blocks[b.OID] = b
	}
	tabIds := make(map[string]bool, len(tabIdList))
	for _, id := range tabIdList {
		tabIds[id] = true
	}

	validBlocks := make(map[string]bool, len(blockList))
	var orphanIds []string
	for _, b := range blockList {
		if blockResolvesToTab(b.OID, blocks, tabIds) {
			validBlocks[b.OID] = true
		} else {
			orphanIds = append(orphanIds, b.OID)
		}
	}

	// Prune dangling/orphaned block references out of every tab (BlockIds + layout
	// tree) BEFORE deleting, so nothing keeps pointing at a block that is gone (or
	// at a block id that no longer exists in the store at all).
	for tabId := range tabIds {
		if err := pruneTabBlockRefs(ctx, tabId, validBlocks); err != nil {
			log.Printf("GCOrphanedBlocks: error pruning tab %s: %v", tabId, err)
		}
	}

	if len(orphanIds) == 0 {
		return nil
	}

	// Delete the orphan blocks. Only delete chain-roots: an orphan whose parent is
	// another block still present in the snapshot is a sub-block and gets removed by
	// the parent's recursive DeleteBlock, so deleting it directly would double-delete.
	for _, blockId := range orphanIds {
		block := blocks[blockId]
		parentORef := waveobj.ParseORefNoErr(block.ParentORef)
		if parentORef != nil && parentORef.OType == waveobj.OType_Block {
			if _, parentPresent := blocks[parentORef.OID]; parentPresent {
				continue
			}
		}
		log.Printf("GCOrphanedBlocks: deleting orphaned block %s (parentoref=%q)", blockId, block.ParentORef)
		if err := DeleteBlock(ctx, blockId, false); err != nil {
			log.Printf("GCOrphanedBlocks: error deleting orphaned block %s: %v", blockId, err)
		}
	}
	return nil
}

// pruneTabBlockRefs removes any block id not in validBlocks from the tab's
// BlockIds list and from its LayoutState node tree.
func pruneTabBlockRefs(ctx context.Context, tabId string, validBlocks map[string]bool) error {
	tab, err := wstore.DBGet[*waveobj.Tab](ctx, tabId)
	if err != nil {
		return err
	}
	if tab == nil {
		return nil
	}
	newBlockIds := make([]string, 0, len(tab.BlockIds))
	blockIdsChanged := false
	for _, id := range tab.BlockIds {
		if validBlocks[id] {
			newBlockIds = append(newBlockIds, id)
			continue
		}
		blockIdsChanged = true
	}
	if blockIdsChanged {
		tab.BlockIds = newBlockIds
		if err := wstore.DBUpdate(ctx, tab); err != nil {
			return fmt.Errorf("error updating tab blockids: %w", err)
		}
	}
	if tab.LayoutState == "" {
		return nil
	}
	return pruneLayoutState(ctx, tab.LayoutState, validBlocks)
}

// pruneLayoutState rewrites a tab's LayoutState so its node tree and leaf order no
// longer reference any block id outside validBlocks.
func pruneLayoutState(ctx context.Context, layoutStateId string, validBlocks map[string]bool) error {
	layout, err := wstore.DBGet[*waveobj.LayoutState](ctx, layoutStateId)
	if err != nil {
		return err
	}
	if layout == nil {
		return nil
	}
	changed := false
	if layout.RootNode != nil {
		newRoot, keep, removed := pruneLayoutNode(layout.RootNode, validBlocks)
		if removed {
			changed = true
			if keep {
				layout.RootNode = newRoot
			} else {
				layout.RootNode = nil
			}
		}
	}
	if layout.LeafOrder != nil {
		newLeaves := make([]waveobj.LeafOrderEntry, 0, len(*layout.LeafOrder))
		for _, e := range *layout.LeafOrder {
			if validBlocks[e.BlockId] {
				newLeaves = append(newLeaves, e)
				continue
			}
			changed = true
		}
		layout.LeafOrder = &newLeaves
	}
	if !changed {
		return nil
	}
	return wstore.DBUpdate(ctx, layout)
}

// pruneLayoutNode recursively prunes a JSON-decoded layout node tree. It returns
// the rewritten node, whether that node should be kept, and whether anything was
// removed in this subtree. A leaf survives only when its block id is in
// validBlocks. An internal node that loses all children is dropped; one that is
// left with a single child collapses into that child (the same invariant the
// native balanceNode enforces — internal nodes never hold a single child).
func pruneLayoutNode(node any, validBlocks map[string]bool) (newNode any, keep bool, removed bool) {
	m, ok := node.(map[string]any)
	if !ok {
		// Unrecognized/garbage node — drop it.
		return nil, false, true
	}
	children, hasChildren := nodeChildren(m)
	if hasChildren {
		newChildren := make([]any, 0, len(children))
		for _, c := range children {
			pc, childKeep, childRemoved := pruneLayoutNode(c, validBlocks)
			if childRemoved {
				removed = true
			}
			if childKeep {
				newChildren = append(newChildren, pc)
			}
		}
		if len(newChildren) != len(children) {
			removed = true
		}
		if len(newChildren) == 0 {
			return nil, false, true
		}
		if len(newChildren) == 1 {
			// Collapse the lone surviving child into this node's slot.
			return newChildren[0], true, true
		}
		m["children"] = newChildren
		return m, true, removed
	}
	blockId := nodeLeafBlockId(m)
	if blockId != "" && validBlocks[blockId] {
		return m, true, false
	}
	return nil, false, true
}

// nodeChildren returns the non-empty children slice of a JSON-decoded layout node.
func nodeChildren(m map[string]any) ([]any, bool) {
	c, ok := m["children"].([]any)
	if !ok || len(c) == 0 {
		return nil, false
	}
	return c, true
}

// nodeLeafBlockId returns the block id of a leaf layout node. The wire key is the
// camelCase `blockId` nested under `data` (frontend TabLayoutData).
func nodeLeafBlockId(m map[string]any) string {
	data, ok := m["data"].(map[string]any)
	if !ok {
		return ""
	}
	blockId, _ := data["blockId"].(string)
	return blockId
}

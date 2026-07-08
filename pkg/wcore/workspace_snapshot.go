// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package wcore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/wavetermdev/waveterm/pkg/waveobj"
	"github.com/wavetermdev/waveterm/pkg/wstore"
)

// SavedWorkspaceTab is one tab captured in a workspace layout snapshot: its name plus the
// pane arrangement. RootNode is a verbatim (deep) copy of the tab's LayoutState tree with the
// ORIGINAL block ids at its leaves; Blocks maps each of those old block ids to the BlockDef
// (captured meta) needed to recreate the pane on restore.
type SavedWorkspaceTab struct {
	Name            string                       `json:"name"`
	RootNode        any                          `json:"rootnode,omitempty"`
	FocusedNodeId   string                       `json:"focusednodeid,omitempty"`
	MagnifiedNodeId string                       `json:"magnifiednodeid,omitempty"`
	Blocks          map[string]*waveobj.BlockDef `json:"blocks"`
}

// SavedWorkspaceLayout is a self-snapshot of a workspace's tabs + panes, stored JSON-encoded
// in Workspace.Meta[MetaKey_WorkspaceSavedLayout]. It captures structure + block definitions,
// NOT live process state (restored terminals start fresh shells).
type SavedWorkspaceLayout struct {
	Tabs []SavedWorkspaceTab `json:"tabs"`
}

// jsonDeepCopy round-trips a JSON-decoded value so the returned tree is fully detached from
// the source (mutating it cannot affect the live LayoutState, and vice versa).
func jsonDeepCopy(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// decodeSavedLayout reconstructs a SavedWorkspaceLayout from a Workspace.Meta value (which,
// after a DB round-trip, is a generic map[string]any) via a JSON round-trip.
func decodeSavedLayout(v any) (*SavedWorkspaceLayout, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var snap SavedWorkspaceLayout
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

// SaveWorkspaceLayout captures the workspace's current tabs + pane layout into
// Workspace.Meta[MetaKey_WorkspaceSavedLayout], overwriting any previous snapshot.
func SaveWorkspaceLayout(ctx context.Context, workspaceId string) error {
	ws, err := GetWorkspace(ctx, workspaceId)
	if err != nil {
		return fmt.Errorf("workspace %s not found: %w", workspaceId, err)
	}
	snap := SavedWorkspaceLayout{}
	for _, tabId := range ws.TabIds {
		tab, err := wstore.DBGet[*waveobj.Tab](ctx, tabId)
		if err != nil || tab == nil {
			continue
		}
		saved := SavedWorkspaceTab{Name: tab.Name, Blocks: map[string]*waveobj.BlockDef{}}
		if tab.LayoutState != "" {
			ls, _ := wstore.DBGet[*waveobj.LayoutState](ctx, tab.LayoutState)
			if ls != nil {
				root, err := jsonDeepCopy(ls.RootNode)
				if err != nil {
					return fmt.Errorf("error copying layout for tab %s: %w", tabId, err)
				}
				saved.RootNode = root
				saved.FocusedNodeId = ls.FocusedNodeId
				saved.MagnifiedNodeId = ls.MagnifiedNodeId
			}
		}
		for _, blockId := range tab.BlockIds {
			blk, err := wstore.DBGet[*waveobj.Block](ctx, blockId)
			if err != nil || blk == nil {
				continue
			}
			saved.Blocks[blockId] = &waveobj.BlockDef{Meta: blk.Meta}
		}
		snap.Tabs = append(snap.Tabs, saved)
	}
	if ws.Meta == nil {
		ws.Meta = waveobj.MetaMapType{}
	}
	ws.Meta[waveobj.MetaKey_WorkspaceSavedLayout] = snap
	return wstore.DBUpdate(ctx, ws)
}

// RestoreWorkspaceLayout rebuilds the workspace to its saved snapshot (wipe & recreate): it
// creates fresh tabs/panes matching the snapshot and deletes every current tab (force-closing
// panes/tabs that have since drifted). Returns an error if no snapshot exists. New tabs are
// created BEFORE the old ones are deleted so the workspace is never momentarily empty.
func RestoreWorkspaceLayout(ctx context.Context, workspaceId string) error {
	ws, err := GetWorkspace(ctx, workspaceId)
	if err != nil {
		return fmt.Errorf("workspace %s not found: %w", workspaceId, err)
	}
	raw, ok := ws.Meta[waveobj.MetaKey_WorkspaceSavedLayout]
	if !ok || raw == nil {
		return fmt.Errorf("workspace %s has no saved layout", workspaceId)
	}
	snap, err := decodeSavedLayout(raw)
	if err != nil {
		return fmt.Errorf("error decoding saved layout: %w", err)
	}
	if len(snap.Tabs) == 0 {
		return fmt.Errorf("saved layout for workspace %s has no tabs", workspaceId)
	}

	oldTabIds := append([]string{}, ws.TabIds...)
	newTabIds := make([]string, 0, len(snap.Tabs))

	for _, saved := range snap.Tabs {
		// createTabObj appends the new tab to ws.TabIds and creates an empty LayoutState.
		tab, err := createTabObj(ctx, workspaceId, saved.Name, nil)
		if err != nil {
			return fmt.Errorf("error creating tab for restore: %w", err)
		}
		idMap := map[string]string{}
		leafOrder := []waveobj.LeafOrderEntry{}
		var newRoot any
		if saved.RootNode != nil {
			copied, err := jsonDeepCopy(saved.RootNode)
			if err != nil {
				return fmt.Errorf("error copying saved layout: %w", err)
			}
			newRoot, err = rebuildLayoutNode(ctx, copied, tab.OID, saved.Blocks, idMap, &leafOrder)
			if err != nil {
				return fmt.Errorf("error rebuilding layout for tab %s: %w", tab.OID, err)
			}
		} else {
			// No captured tree (e.g. a pre-layout tab): fall back to a single default terminal.
			if err := ApplyPortableLayout(ctx, tab.OID, GetNewTabLayout(), false); err != nil {
				return fmt.Errorf("error applying fallback layout: %w", err)
			}
		}
		if newRoot != nil {
			ls, err := wstore.DBGet[*waveobj.LayoutState](ctx, tab.LayoutState)
			if err != nil || ls == nil {
				return fmt.Errorf("error loading layout state for restored tab %s: %w", tab.OID, err)
			}
			ls.RootNode = newRoot
			order := leafOrder
			ls.LeafOrder = &order
			ls.FocusedNodeId = idMap[saved.FocusedNodeId]
			ls.MagnifiedNodeId = idMap[saved.MagnifiedNodeId]
			if err := wstore.DBUpdate(ctx, ls); err != nil {
				return fmt.Errorf("error updating layout state for restored tab %s: %w", tab.OID, err)
			}
		}
		newTabIds = append(newTabIds, tab.OID)
	}

	// Point the workspace at exactly the new tabs (createTabObj left ws.TabIds = old + new).
	ws, err = GetWorkspace(ctx, workspaceId)
	if err != nil {
		return fmt.Errorf("error reloading workspace: %w", err)
	}
	ws.TabIds = newTabIds
	ws.ActiveTabId = newTabIds[0]
	if err := wstore.DBUpdate(ctx, ws); err != nil {
		return fmt.Errorf("error updating workspace tabids: %w", err)
	}

	// Now tear down the old tabs (blocks stop their controllers -> the "force close").
	for _, oldTabId := range oldTabIds {
		tab, _ := wstore.DBGet[*waveobj.Tab](ctx, oldTabId)
		if tab == nil {
			continue
		}
		for _, blockId := range tab.BlockIds {
			if err := DeleteBlock(ctx, blockId, false); err != nil {
				return fmt.Errorf("error deleting old block %s: %w", blockId, err)
			}
		}
		if err := wstore.DBDelete(ctx, waveobj.OType_Tab, oldTabId); err != nil {
			return fmt.Errorf("error deleting old tab %s: %w", oldTabId, err)
		}
		if tab.LayoutState != "" {
			wstore.DBDelete(ctx, waveobj.OType_LayoutState, tab.LayoutState)
		}
	}
	return nil
}

// rebuildLayoutNode walks a copied snapshot layout tree, creating a fresh block for each leaf
// (from its captured BlockDef) and regenerating every node id. It rewrites each leaf's
// data.blockId to the new block, records the (newNodeId -> newBlockId) leaf order, and fills
// idMap with old->new node ids so the caller can remap focused/magnified node ids. Node sizes
// and flexDirection are preserved verbatim from the copy. Reuses gc.go's nodeChildren /
// nodeLeafBlockId (same package).
func rebuildLayoutNode(ctx context.Context, node any, tabId string, blocks map[string]*waveobj.BlockDef, idMap map[string]string, leafOrder *[]waveobj.LeafOrderEntry) (any, error) {
	m, ok := node.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid layout node %T", node)
	}
	newId := uuid.NewString()
	if oldId, _ := m["id"].(string); oldId != "" {
		idMap[oldId] = newId
	}
	m["id"] = newId

	if children, hasChildren := nodeChildren(m); hasChildren {
		newChildren := make([]any, 0, len(children))
		for _, c := range children {
			nc, err := rebuildLayoutNode(ctx, c, tabId, blocks, idMap, leafOrder)
			if err != nil {
				return nil, err
			}
			newChildren = append(newChildren, nc)
		}
		m["children"] = newChildren
		return m, nil
	}

	oldBlockId := nodeLeafBlockId(m)
	if oldBlockId == "" {
		return nil, fmt.Errorf("layout leaf has no block id")
	}
	def := blocks[oldBlockId]
	if def == nil {
		return nil, fmt.Errorf("saved layout references unknown block %s", oldBlockId)
	}
	blk, err := CreateBlock(ctx, tabId, def, &waveobj.RuntimeOpts{})
	if err != nil {
		return nil, fmt.Errorf("error creating block for restore: %w", err)
	}
	data, _ := m["data"].(map[string]any)
	if data == nil {
		data = map[string]any{}
		m["data"] = data
	}
	data["blockId"] = blk.OID
	*leafOrder = append(*leafOrder, waveobj.LeafOrderEntry{NodeId: newId, BlockId: blk.OID})
	return m, nil
}

// Copyright 2025, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package wcore

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/wavetermdev/waveterm/pkg/filestore"
	"github.com/wavetermdev/waveterm/pkg/panichandler"
	"github.com/wavetermdev/waveterm/pkg/telemetry"
	"github.com/wavetermdev/waveterm/pkg/telemetry/telemetrydata"
	"github.com/wavetermdev/waveterm/pkg/util/utilfn"
	"github.com/wavetermdev/waveterm/pkg/waveobj"
	"github.com/wavetermdev/waveterm/pkg/wps"
	"github.com/wavetermdev/waveterm/pkg/wshrpc"
	"github.com/wavetermdev/waveterm/pkg/wstore"
)

func CreateSubBlock(ctx context.Context, blockId string, blockDef *waveobj.BlockDef) (*waveobj.Block, error) {
	if blockDef == nil {
		return nil, fmt.Errorf("blockDef is nil")
	}
	if blockDef.Meta == nil || blockDef.Meta.GetString(waveobj.MetaKey_View, "") == "" {
		return nil, fmt.Errorf("no view provided for new block")
	}
	blockData, err := createSubBlockObj(ctx, blockId, blockDef)
	if err != nil {
		return nil, fmt.Errorf("error creating sub block: %w", err)
	}
	blockView := blockDef.Meta.GetString(waveobj.MetaKey_View, "")
	blockController := blockDef.Meta.GetString(waveobj.MetaKey_Controller, "")
	go recordBlockCreationTelemetry(blockView, blockController, true)
	return blockData, nil
}

func createSubBlockObj(ctx context.Context, parentBlockId string, blockDef *waveobj.BlockDef) (*waveobj.Block, error) {
	return wstore.WithTxRtn(ctx, func(tx *wstore.TxWrap) (*waveobj.Block, error) {
		parentBlock, _ := wstore.DBGet[*waveobj.Block](tx.Context(), parentBlockId)
		if parentBlock == nil {
			return nil, fmt.Errorf("parent block not found: %q", parentBlockId)
		}
		blockId := uuid.NewString()
		blockData := &waveobj.Block{
			OID:         blockId,
			ParentORef:  waveobj.MakeORef(waveobj.OType_Block, parentBlockId).String(),
			RuntimeOpts: nil,
			Meta:        blockDef.Meta,
		}
		wstore.DBInsert(tx.Context(), blockData)
		parentBlock.SubBlockIds = append(parentBlock.SubBlockIds, blockId)
		wstore.DBUpdate(tx.Context(), parentBlock)
		return blockData, nil
	})
}

func CreateBlock(ctx context.Context, tabId string, blockDef *waveobj.BlockDef, rtOpts *waveobj.RuntimeOpts) (rtnBlock *waveobj.Block, rtnErr error) {
	return CreateBlockWithTelemetry(ctx, tabId, blockDef, rtOpts, true)
}

func CreateBlockWithTelemetry(ctx context.Context, tabId string, blockDef *waveobj.BlockDef, rtOpts *waveobj.RuntimeOpts, recordTelemetry bool) (rtnBlock *waveobj.Block, rtnErr error) {
	var blockCreated bool
	var newBlockOID string
	defer func() {
		if rtnErr == nil {
			return
		}
		// if there was an error, and we created the block, clean it up since the function failed
		if blockCreated && newBlockOID != "" {
			deleteBlockObj(ctx, newBlockOID)
			filestore.WFS.DeleteZone(ctx, newBlockOID)
		}
	}()
	if blockDef == nil {
		return nil, fmt.Errorf("blockDef is nil")
	}
	if blockDef.Meta == nil || blockDef.Meta.GetString(waveobj.MetaKey_View, "") == "" {
		return nil, fmt.Errorf("no view provided for new block")
	}
	blockData, err := createBlockObj(ctx, tabId, blockDef, rtOpts)
	if err != nil {
		return nil, fmt.Errorf("error creating block: %w", err)
	}
	blockCreated = true
	newBlockOID = blockData.OID
	// upload the files if present
	if len(blockDef.Files) > 0 {
		for fileName, fileDef := range blockDef.Files {
			err := filestore.WFS.MakeFile(ctx, newBlockOID, fileName, fileDef.Meta, wshrpc.FileOpts{})
			if err != nil {
				return nil, fmt.Errorf("error making blockfile %q: %w", fileName, err)
			}
			err = filestore.WFS.WriteFile(ctx, newBlockOID, fileName, []byte(fileDef.Content))
			if err != nil {
				return nil, fmt.Errorf("error writing blockfile %q: %w", fileName, err)
			}
		}
	}
	if recordTelemetry {
		blockView := blockDef.Meta.GetString(waveobj.MetaKey_View, "")
		blockController := blockDef.Meta.GetString(waveobj.MetaKey_Controller, "")
		go recordBlockCreationTelemetry(blockView, blockController, false)
	}
	return blockData, nil
}

func recordBlockCreationTelemetry(blockView string, blockController string, subBlock bool) {
	defer func() {
		panichandler.PanicHandler("CreateBlock:telemetry", recover())
	}()
	if blockView == "" {
		return
	}
	tctx, cancelFn := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelFn()
	telemetry.UpdateActivity(tctx, wshrpc.ActivityUpdate{
		Renderers: map[string]int{blockView: 1},
	})
	telemetry.RecordTEvent(tctx, &telemetrydata.TEvent{
		Event: "action:createblock",
		Props: telemetrydata.TEventProps{
			BlockView:       blockView,
			BlockController: blockController,
			BlockSubBlock:   subBlock,
		},
	})
}

func createBlockObj(ctx context.Context, tabId string, blockDef *waveobj.BlockDef, rtOpts *waveobj.RuntimeOpts) (*waveobj.Block, error) {
	return wstore.WithTxRtn(ctx, func(tx *wstore.TxWrap) (*waveobj.Block, error) {
		tab, _ := wstore.DBGet[*waveobj.Tab](tx.Context(), tabId)
		if tab == nil {
			return nil, fmt.Errorf("tab not found: %q", tabId)
		}
		blockId := uuid.NewString()
		blockData := &waveobj.Block{
			OID:         blockId,
			ParentORef:  waveobj.MakeORef(waveobj.OType_Tab, tabId).String(),
			RuntimeOpts: rtOpts,
			Meta:        blockDef.Meta,
		}
		wstore.DBInsert(tx.Context(), blockData)
		tab.BlockIds = append(tab.BlockIds, blockId)
		wstore.DBUpdate(tx.Context(), tab)
		return blockData, nil
	})
}

// Must delete all blocks individually first.
// Also deletes LayoutState.
// recursive: if true, will recursively close parent tab, window, workspace, if they are empty.
// Returns new active tab id, error.
func DeleteBlock(ctx context.Context, blockId string, recursive bool) error {
	block, err := wstore.DBGet[*waveobj.Block](ctx, blockId)
	if err != nil {
		return fmt.Errorf("error getting block: %w", err)
	}
	if block == nil {
		return nil
	}
	if len(block.SubBlockIds) > 0 {
		for _, subBlockId := range block.SubBlockIds {
			err := DeleteBlock(ctx, subBlockId, recursive)
			if err != nil {
				return fmt.Errorf("error deleting subblock %s: %w", subBlockId, err)
			}
		}
	}
	parentBlockCount, err := deleteBlockObj(ctx, blockId)
	if err != nil {
		return fmt.Errorf("error deleting block: %w", err)
	}
	log.Printf("DeleteBlock: parentBlockCount: %d", parentBlockCount)
	parentORef := waveobj.ParseORefNoErr(block.ParentORef)

	if recursive && parentORef.OType == waveobj.OType_Tab && parentBlockCount == 0 {
		// if parent tab has no blocks, delete the tab
		log.Printf("DeleteBlock: parent tab has no blocks, deleting tab %s", parentORef.OID)
		parentWorkspaceId, err := wstore.DBFindWorkspaceForTabId(ctx, parentORef.OID)
		if err != nil {
			return fmt.Errorf("error finding workspace for tab to delete %s: %w", parentORef.OID, err)
		}
		newActiveTabId, err := DeleteTab(ctx, parentWorkspaceId, parentORef.OID, true)
		if err != nil {
			return fmt.Errorf("error deleting tab %s: %w", parentORef.OID, err)
		}
		SendActiveTabUpdate(ctx, parentWorkspaceId, newActiveTabId)
	}
	sendBlockCloseEvent(blockId)
	return nil
}

type MoveBlockToTabResult struct {
	SourceTabId       string
	SourceWorkspaceId string
	DestWorkspaceId   string
	// Non-empty when the emptied source tab was deleted and a sibling became active
	// in its place (also carries the unchanged active tab id when a non-active tab
	// was deleted — safe to re-announce either way).
	NewSourceActiveTabId string
}

// MoveBlockToTab moves a top-level block from its current tab to targetTabId. The
// block (and its running controller) is preserved — only tab membership, the block's
// parent ref, and the two tabs' layout trees change. The source tab's layout node is
// removed via "removenode" (NOT "delete", which would make the client delete the
// block). An emptied source tab is deleted (Wave keeps no empty tabs); moving the
// last block out of a workspace's only tab is refused, mirroring MoveTab's last-tab
// guard. The target tab is made active in its workspace.
func MoveBlockToTab(ctx context.Context, blockId string, targetTabId string) (*MoveBlockToTabResult, error) {
	block, err := wstore.DBGet[*waveobj.Block](ctx, blockId)
	if err != nil {
		return nil, fmt.Errorf("error getting block: %w", err)
	}
	if block == nil {
		return nil, fmt.Errorf("block not found: %q", blockId)
	}
	parentORef := waveobj.ParseORefNoErr(block.ParentORef)
	if parentORef == nil || parentORef.OType != waveobj.OType_Tab {
		return nil, fmt.Errorf("block %s is not a top-level tab block (parent %q)", blockId, block.ParentORef)
	}
	sourceTabId := parentORef.OID
	if sourceTabId == targetTabId {
		return nil, fmt.Errorf("source and target tabs are the same")
	}
	sourceWorkspaceId, err := wstore.DBFindWorkspaceForTabId(ctx, sourceTabId)
	if err != nil || sourceWorkspaceId == "" {
		return nil, fmt.Errorf("error finding workspace for source tab %s: %w", sourceTabId, err)
	}
	destWorkspaceId, err := wstore.DBFindWorkspaceForTabId(ctx, targetTabId)
	if err != nil || destWorkspaceId == "" {
		return nil, fmt.Errorf("error finding workspace for target tab %s: %w", targetTabId, err)
	}

	sourceEmpty, err := wstore.WithTxRtn(ctx, func(tx *wstore.TxWrap) (bool, error) {
		sourceTab, _ := wstore.DBGet[*waveobj.Tab](tx.Context(), sourceTabId)
		if sourceTab == nil {
			return false, fmt.Errorf("source tab not found: %q", sourceTabId)
		}
		targetTab, _ := wstore.DBGet[*waveobj.Tab](tx.Context(), targetTabId)
		if targetTab == nil {
			return false, fmt.Errorf("target tab not found: %q", targetTabId)
		}
		txBlock, _ := wstore.DBGet[*waveobj.Block](tx.Context(), blockId)
		if txBlock == nil {
			return false, fmt.Errorf("block not found: %q", blockId)
		}
		sourceWs, _ := wstore.DBGet[*waveobj.Workspace](tx.Context(), sourceWorkspaceId)
		if sourceWs == nil {
			return false, fmt.Errorf("source workspace not found: %q", sourceWorkspaceId)
		}
		if len(sourceTab.BlockIds) <= 1 && len(sourceWs.TabIds) <= 1 {
			return false, fmt.Errorf("cannot move the last pane out of a workspace")
		}
		sourceTab.BlockIds = utilfn.RemoveElemFromSlice(sourceTab.BlockIds, blockId)
		targetTab.BlockIds = append(targetTab.BlockIds, blockId)
		txBlock.ParentORef = waveobj.MakeORef(waveobj.OType_Tab, targetTabId).String()
		wstore.DBUpdate(tx.Context(), sourceTab)
		wstore.DBUpdate(tx.Context(), targetTab)
		wstore.DBUpdate(tx.Context(), txBlock)
		return len(sourceTab.BlockIds) == 0, nil
	})
	if err != nil {
		return nil, err
	}

	rtn := &MoveBlockToTabResult{
		SourceTabId:       sourceTabId,
		SourceWorkspaceId: sourceWorkspaceId,
		DestWorkspaceId:   destWorkspaceId,
	}
	if sourceEmpty {
		// DeleteTab also removes the tab's LayoutState, so no removenode is needed
		// (BlockIds is already empty, so no blocks are deleted with it).
		newActiveTabId, err := DeleteTab(ctx, sourceWorkspaceId, sourceTabId, false)
		if err != nil {
			return nil, fmt.Errorf("error deleting emptied source tab %s: %w", sourceTabId, err)
		}
		rtn.NewSourceActiveTabId = newActiveTabId
	} else {
		err = QueueLayoutActionForTab(ctx, sourceTabId, waveobj.LayoutActionData{
			ActionType: LayoutActionDataType_RemoveNode,
			BlockId:    blockId,
		})
		if err != nil {
			return nil, fmt.Errorf("error queuing removenode for source tab %s: %w", sourceTabId, err)
		}
	}
	err = QueueLayoutActionForTab(ctx, targetTabId, waveobj.LayoutActionData{
		ActionType: LayoutActionDataType_Insert,
		BlockId:    blockId,
		Focused:    true,
	})
	if err != nil {
		return nil, fmt.Errorf("error queuing insert for target tab %s: %w", targetTabId, err)
	}
	err = SetActiveTab(ctx, destWorkspaceId, targetTabId)
	if err != nil {
		return nil, fmt.Errorf("error activating target tab %s: %w", targetTabId, err)
	}
	return rtn, nil
}

// returns the updated block count for the parent object
func deleteBlockObj(ctx context.Context, blockId string) (int, error) {
	return wstore.WithTxRtn(ctx, func(tx *wstore.TxWrap) (int, error) {
		block, err := wstore.DBGet[*waveobj.Block](tx.Context(), blockId)
		if err != nil {
			return -1, fmt.Errorf("error getting block: %w", err)
		}
		if block == nil {
			return -1, fmt.Errorf("block not found: %q", blockId)
		}
		if len(block.SubBlockIds) > 0 {
			return -1, fmt.Errorf("block has subblocks, must delete subblocks first")
		}
		parentORef := waveobj.ParseORefNoErr(block.ParentORef)
		parentBlockCount := -1
		if parentORef != nil {
			if parentORef.OType == waveobj.OType_Tab {
				tab, _ := wstore.DBGet[*waveobj.Tab](tx.Context(), parentORef.OID)
				if tab != nil {
					tab.BlockIds = utilfn.RemoveElemFromSlice(tab.BlockIds, blockId)
					wstore.DBUpdate(tx.Context(), tab)
					parentBlockCount = len(tab.BlockIds)
				}
			} else if parentORef.OType == waveobj.OType_Block {
				parentBlock, _ := wstore.DBGet[*waveobj.Block](tx.Context(), parentORef.OID)
				if parentBlock != nil {
					parentBlock.SubBlockIds = utilfn.RemoveElemFromSlice(parentBlock.SubBlockIds, blockId)
					wstore.DBUpdate(tx.Context(), parentBlock)
					parentBlockCount = len(parentBlock.SubBlockIds)
				}
			}
		}
		wstore.DBDelete(tx.Context(), waveobj.OType_Block, blockId)

		// Clean up block runtime info
		blockORef := waveobj.MakeORef(waveobj.OType_Block, blockId)
		wstore.DeleteRTInfo(blockORef)

		return parentBlockCount, nil
	})
}

func sendBlockCloseEvent(blockId string) {
	waveEvent := wps.WaveEvent{
		Event: wps.Event_BlockClose,
		Scopes: []string{
			waveobj.MakeORef(waveobj.OType_Block, blockId).String(),
		},
		Data: blockId,
	}
	wps.Broker.Publish(waveEvent)
}

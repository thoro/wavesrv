// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package wcore

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/wavetermdev/waveterm/pkg/wavebase"
	"github.com/wavetermdev/waveterm/pkg/waveobj"
	"github.com/wavetermdev/waveterm/pkg/wstore"
)

func TestMain(m *testing.M) {
	tempDir, err := os.MkdirTemp("", "wcore-test-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tempDir)
	wavebase.DataHome_VarCache = tempDir
	wavebase.ConfigHome_VarCache = tempDir
	if err := wavebase.EnsureWaveDBDir(); err != nil {
		panic(err)
	}
	if err := wstore.InitWStore(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func makeTestWorkspace(t *testing.T, ctx context.Context, tabIds []string, activeTabId string) *waveobj.Workspace {
	t.Helper()
	ws := &waveobj.Workspace{
		OID:         uuid.NewString(),
		TabIds:      tabIds,
		ActiveTabId: activeTabId,
	}
	if err := wstore.DBInsert(ctx, ws); err != nil {
		t.Fatalf("failed to insert workspace: %v", err)
	}
	return ws
}

func TestMoveTab_MovesActiveTabAndReassignsSource(t *testing.T) {
	ctx := context.Background()
	tabA, tabB, tabC := uuid.NewString(), uuid.NewString(), uuid.NewString()
	src := makeTestWorkspace(t, ctx, []string{tabA, tabB}, tabA)
	dst := makeTestWorkspace(t, ctx, []string{tabC}, tabC)

	newSrcActive, err := MoveTab(ctx, src.OID, dst.OID, tabA)
	if err != nil {
		t.Fatalf("MoveTab returned error: %v", err)
	}
	if newSrcActive != tabB {
		t.Fatalf("expected new source active tab %q, got %q", tabB, newSrcActive)
	}

	srcReloaded, _ := wstore.DBGet[*waveobj.Workspace](ctx, src.OID)
	if len(srcReloaded.TabIds) != 1 || srcReloaded.TabIds[0] != tabB {
		t.Fatalf("expected source tabids [%s], got %v", tabB, srcReloaded.TabIds)
	}
	if srcReloaded.ActiveTabId != tabB {
		t.Fatalf("expected source active tab %q, got %q", tabB, srcReloaded.ActiveTabId)
	}

	dstReloaded, _ := wstore.DBGet[*waveobj.Workspace](ctx, dst.OID)
	if len(dstReloaded.TabIds) != 2 || dstReloaded.TabIds[1] != tabA {
		t.Fatalf("expected dest tabids [%s %s], got %v", tabC, tabA, dstReloaded.TabIds)
	}
	if dstReloaded.ActiveTabId != tabA {
		t.Fatalf("expected dest active tab %q, got %q", tabA, dstReloaded.ActiveTabId)
	}
}

func TestMoveTab_MovesNonActiveTabKeepsSourceActive(t *testing.T) {
	ctx := context.Background()
	tabA, tabB, tabC := uuid.NewString(), uuid.NewString(), uuid.NewString()
	src := makeTestWorkspace(t, ctx, []string{tabA, tabB}, tabB)
	dst := makeTestWorkspace(t, ctx, []string{tabC}, tabC)

	newSrcActive, err := MoveTab(ctx, src.OID, dst.OID, tabA)
	if err != nil {
		t.Fatalf("MoveTab returned error: %v", err)
	}
	if newSrcActive != "" {
		t.Fatalf("expected no source active-tab change, got %q", newSrcActive)
	}

	srcReloaded, _ := wstore.DBGet[*waveobj.Workspace](ctx, src.OID)
	if srcReloaded.ActiveTabId != tabB {
		t.Fatalf("expected source active tab unchanged %q, got %q", tabB, srcReloaded.ActiveTabId)
	}
}

func TestMoveTab_RejectsLastTab(t *testing.T) {
	ctx := context.Background()
	tabA, tabC := uuid.NewString(), uuid.NewString()
	src := makeTestWorkspace(t, ctx, []string{tabA}, tabA)
	dst := makeTestWorkspace(t, ctx, []string{tabC}, tabC)

	if _, err := MoveTab(ctx, src.OID, dst.OID, tabA); err == nil {
		t.Fatalf("expected error when moving the last tab out of a workspace")
	}
}

func TestMoveTab_RejectsSameWorkspace(t *testing.T) {
	ctx := context.Background()
	tabA, tabB := uuid.NewString(), uuid.NewString()
	src := makeTestWorkspace(t, ctx, []string{tabA, tabB}, tabA)

	if _, err := MoveTab(ctx, src.OID, src.OID, tabA); err == nil {
		t.Fatalf("expected error when source and destination are the same workspace")
	}
}

func TestMoveTab_RejectsTabNotInSource(t *testing.T) {
	ctx := context.Background()
	tabA, tabB, tabC := uuid.NewString(), uuid.NewString(), uuid.NewString()
	src := makeTestWorkspace(t, ctx, []string{tabA, tabB}, tabA)
	dst := makeTestWorkspace(t, ctx, []string{tabC}, tabC)

	if _, err := MoveTab(ctx, src.OID, dst.OID, uuid.NewString()); err == nil {
		t.Fatalf("expected error when the tab is not present in the source workspace")
	}
}

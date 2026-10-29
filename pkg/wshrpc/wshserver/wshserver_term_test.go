// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package wshserver

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/wavetermdev/waveterm/pkg/filestore"
	"github.com/wavetermdev/waveterm/pkg/wavebase"
	"github.com/wavetermdev/waveterm/pkg/waveobj"
	"github.com/wavetermdev/waveterm/pkg/wshrpc"
	"github.com/wavetermdev/waveterm/pkg/wstore"
)

func TestMain(m *testing.M) {
	tempDir, err := os.MkdirTemp("", "wshserver-test-*")
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
	if err := filestore.InitFilestore(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func makeTermZone(t *testing.T, ctx context.Context, zoneId string, extraFiles ...string) {
	t.Helper()
	for _, name := range append([]string{wavebase.BlockFile_Term}, extraFiles...) {
		if err := filestore.WFS.MakeFile(ctx, zoneId, name, nil, wshrpc.FileOpts{}); err != nil {
			t.Fatalf("make %s/%s: %v", zoneId, name, err)
		}
		if err := filestore.WFS.AppendData(ctx, zoneId, name, []byte("output\n")); err != nil {
			t.Fatalf("append %s/%s: %v", zoneId, name, err)
		}
	}
}

func TestTermDeleteOrphanedBuffers_DeletesOnlyUnreferencedZones(t *testing.T) {
	ctx := context.Background()
	liveBlockId := uuid.NewString()
	tab := &waveobj.Tab{OID: uuid.NewString(), BlockIds: []string{liveBlockId}}
	if err := wstore.DBInsert(ctx, tab); err != nil {
		t.Fatal(err)
	}
	// unnamed on purpose: wcore.ListWorkspaces skips these
	ws := &waveobj.Workspace{OID: uuid.NewString(), TabIds: []string{tab.OID}}
	if err := wstore.DBInsert(ctx, ws); err != nil {
		t.Fatal(err)
	}
	// unreferenced by any tab, but the block object still exists (awaiting GC)
	pendingBlock := &waveobj.Block{OID: uuid.NewString()}
	if err := wstore.DBInsert(ctx, pendingBlock); err != nil {
		t.Fatal(err)
	}
	// durable job output lives in a zone keyed by the job id
	job := &waveobj.Job{OID: uuid.NewString()}
	if err := wstore.DBInsert(ctx, job); err != nil {
		t.Fatal(err)
	}
	orphanId := uuid.NewString()
	makeTermZone(t, ctx, liveBlockId)
	makeTermZone(t, ctx, pendingBlock.OID)
	makeTermZone(t, ctx, job.OID)
	makeTermZone(t, ctx, orphanId, wavebase.BlockFile_Cache)

	deleted, err := (&WshServer{}).TermDeleteOrphanedBuffersCommand(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	files, err := filestore.WFS.ListFiles(ctx, orphanId)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("orphan zone still has %d files", len(files))
	}
	for _, id := range []string{liveBlockId, pendingBlock.OID, job.OID} {
		if _, err := filestore.WFS.Stat(ctx, id, wavebase.BlockFile_Term); err != nil {
			t.Fatalf("term file of %s was deleted: %v", id, err)
		}
	}
	entries, err := (&WshServer{}).TermScrollbackListCommand(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.BlockId == orphanId {
			t.Fatalf("deleted orphan still listed")
		}
		if e.BlockId == liveBlockId && (e.Orphaned || e.TabId != tab.OID) {
			t.Fatalf("block in unnamed workspace listed as %+v", e)
		}
	}
}

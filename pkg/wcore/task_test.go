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

func TestTaskCreateValidation(t *testing.T) {
	ctx := context.Background()
	ws := makeTestWorkspace(t, ctx, nil, "")

	if _, err := CreateTask(ctx, ws.OID, "   ", nil); err == nil {
		t.Fatalf("expected error for empty text")
	}
	if _, err := CreateTask(ctx, uuid.NewString(), "orphan", nil); err == nil {
		t.Fatalf("expected error for unknown workspace")
	}

	task, err := CreateTask(ctx, ws.OID, "  ship the tasks feature  ", nil)
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	if task.Text != "ship the tasks feature" {
		t.Fatalf("expected trimmed text, got %q", task.Text)
	}
	if task.CreatedTs == 0 {
		t.Fatalf("expected createdts to be set")
	}
	if task.Done || task.DoneTs != 0 {
		t.Fatalf("new task must not be done: %+v", task)
	}
}

func TestTaskUpdateDoneTransitions(t *testing.T) {
	ctx := context.Background()
	ws := makeTestWorkspace(t, ctx, nil, "")
	task, err := CreateTask(ctx, ws.OID, "check me off", nil)
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}

	done := true
	updated, err := UpdateTask(ctx, task.OID, nil, &done, nil, nil)
	if err != nil {
		t.Fatalf("UpdateTask(done) returned error: %v", err)
	}
	if !updated.Done || updated.DoneTs == 0 {
		t.Fatalf("expected done with donets set, got %+v", updated)
	}

	done = false
	updated, err = UpdateTask(ctx, task.OID, nil, &done, nil, nil)
	if err != nil {
		t.Fatalf("UpdateTask(undone) returned error: %v", err)
	}
	if updated.Done || updated.DoneTs != 0 {
		t.Fatalf("expected donets cleared on uncheck, got %+v", updated)
	}

	newText := "renamed task"
	links := []waveobj.TaskLink{{TabId: uuid.NewString(), BlockId: uuid.NewString(), Label: "term · Tab 1"}}
	updated, err = UpdateTask(ctx, task.OID, &newText, nil, &links, nil)
	if err != nil {
		t.Fatalf("UpdateTask(text+links) returned error: %v", err)
	}
	if updated.Text != newText || len(updated.Links) != 1 || updated.Links[0].Label != "term · Tab 1" {
		t.Fatalf("unexpected update result: %+v", updated)
	}

	empty := "   "
	if _, err := UpdateTask(ctx, task.OID, &empty, nil, nil, nil); err == nil {
		t.Fatalf("expected error updating to empty text")
	}
}

func TestTaskUnassignedAndWorkspaceMove(t *testing.T) {
	ctx := context.Background()
	ws := makeTestWorkspace(t, ctx, nil, "")

	unassigned, err := CreateTask(ctx, "", "no workspace yet", nil)
	if err != nil {
		t.Fatalf("CreateTask(unassigned) returned error: %v", err)
	}
	if unassigned.WorkspaceId != "" {
		t.Fatalf("expected empty workspaceid, got %q", unassigned.WorkspaceId)
	}

	scoped, err := ListTasks(ctx, ws.OID)
	if err != nil {
		t.Fatalf("ListTasks(ws) returned error: %v", err)
	}
	for _, task := range scoped {
		if task.OID == unassigned.OID {
			t.Fatalf("unassigned task must not appear in a workspace-scoped list")
		}
	}

	wsId := ws.OID
	moved, err := UpdateTask(ctx, unassigned.OID, nil, nil, nil, &wsId)
	if err != nil {
		t.Fatalf("UpdateTask(move) returned error: %v", err)
	}
	if moved.WorkspaceId != ws.OID {
		t.Fatalf("expected task moved to %s, got %q", ws.OID, moved.WorkspaceId)
	}

	badWs := uuid.NewString()
	if _, err := UpdateTask(ctx, unassigned.OID, nil, nil, nil, &badWs); err == nil {
		t.Fatalf("expected error moving to unknown workspace")
	}

	none := ""
	moved, err = UpdateTask(ctx, unassigned.OID, nil, nil, nil, &none)
	if err != nil {
		t.Fatalf("UpdateTask(unassign) returned error: %v", err)
	}
	if moved.WorkspaceId != "" {
		t.Fatalf("expected task unassigned, got %q", moved.WorkspaceId)
	}
}

func TestTaskListScopingAndOrder(t *testing.T) {
	ctx := context.Background()
	wsA := makeTestWorkspace(t, ctx, nil, "")
	wsB := makeTestWorkspace(t, ctx, nil, "")

	t1, _ := CreateTask(ctx, wsA.OID, "first", nil)
	t2, _ := CreateTask(ctx, wsA.OID, "second", nil)
	t3, _ := CreateTask(ctx, wsB.OID, "other workspace", nil)

	listA, err := ListTasks(ctx, wsA.OID)
	if err != nil {
		t.Fatalf("ListTasks(wsA) returned error: %v", err)
	}
	if len(listA) != 2 || listA[0].OID != t1.OID || listA[1].OID != t2.OID {
		t.Fatalf("unexpected wsA list: %+v", listA)
	}

	all, err := ListTasks(ctx, "")
	if err != nil {
		t.Fatalf("ListTasks(all) returned error: %v", err)
	}
	found := map[string]bool{}
	for _, task := range all {
		found[task.OID] = true
	}
	if !found[t1.OID] || !found[t2.OID] || !found[t3.OID] {
		t.Fatalf("global list missing tasks: %+v", all)
	}
}

func TestTaskDeleteAndWorkspaceCascade(t *testing.T) {
	ctx := context.Background()
	ws := makeTestWorkspace(t, ctx, nil, "")
	task, _ := CreateTask(ctx, ws.OID, "delete me", nil)

	if err := DeleteTask(ctx, task.OID); err != nil {
		t.Fatalf("DeleteTask returned error: %v", err)
	}
	if got, _ := wstore.DBGet[*waveobj.Task](ctx, task.OID); got != nil {
		t.Fatalf("expected task deleted, still present: %+v", got)
	}
	if err := DeleteTask(ctx, task.OID); err != nil {
		t.Fatalf("deleting an already-deleted task should not error, got: %v", err)
	}

	keep, _ := CreateTask(ctx, ws.OID, "survivor check", nil)
	other := makeTestWorkspace(t, ctx, nil, "")
	otherTask, _ := CreateTask(ctx, other.OID, "other ws task", nil)
	if err := DeleteTasksForWorkspace(ctx, ws.OID); err != nil {
		t.Fatalf("DeleteTasksForWorkspace returned error: %v", err)
	}
	if got, _ := wstore.DBGet[*waveobj.Task](ctx, keep.OID); got != nil {
		t.Fatalf("expected cascade to delete task, still present: %+v", got)
	}
	if got, _ := wstore.DBGet[*waveobj.Task](ctx, otherTask.OID); got == nil {
		t.Fatalf("cascade must not delete other workspace's tasks")
	}
}

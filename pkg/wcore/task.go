// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package wcore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/wavetermdev/waveterm/pkg/waveobj"
	"github.com/wavetermdev/waveterm/pkg/wps"
	"github.com/wavetermdev/waveterm/pkg/wstore"
)

var taskTsLock sync.Mutex
var lastTaskTs int64

// Millisecond clock ticks are coarser than task creation can be; bump equal
// stamps so CreatedTs alone gives a stable creation order.
func nextTaskTs() int64 {
	taskTsLock.Lock()
	defer taskTsLock.Unlock()
	now := time.Now().UnixMilli()
	if now <= lastTaskTs {
		now = lastTaskTs + 1
	}
	lastTaskTs = now
	return now
}

// An unassigned task (empty workspaceId) publishes without scopes: only
// allscopes subscribers (global-mode panes) see it, which matches where
// unassigned tasks are listed.
func publishTaskUpdate(workspaceId string) {
	go func() {
		event := wps.WaveEvent{
			Event: wps.Event_TaskUpdate,
			Data:  wps.TaskUpdateEventData{WorkspaceId: workspaceId},
		}
		if workspaceId != "" {
			event.Scopes = []string{waveobj.MakeORef(waveobj.OType_Workspace, workspaceId).String()}
		}
		wps.Broker.Publish(event)
	}()
}

// CreateTask creates a task. An empty workspaceId is allowed and creates an
// unassigned (global) task, visible only in All Workspaces mode.
func CreateTask(ctx context.Context, workspaceId string, text string, links []waveobj.TaskLink) (*waveobj.Task, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("task text is empty")
	}
	if workspaceId != "" {
		if _, err := wstore.DBMustGet[*waveobj.Workspace](ctx, workspaceId); err != nil {
			return nil, fmt.Errorf("error getting workspace: %w", err)
		}
	}
	task := &waveobj.Task{
		OID:         uuid.NewString(),
		WorkspaceId: workspaceId,
		Text:        text,
		CreatedTs:   nextTaskTs(),
		Links:       links,
		Meta:        waveobj.MetaMapType{},
	}
	if err := wstore.DBInsert(ctx, task); err != nil {
		return nil, fmt.Errorf("error inserting task: %w", err)
	}
	publishTaskUpdate(workspaceId)
	return task, nil
}

// UpdateTask patches a task; nil fields stay unchanged. A non-nil workspaceId
// moves the task ("" unassigns it), publishing updates for both the old and
// new workspace so panes on either side refetch.
func UpdateTask(ctx context.Context, taskId string, text *string, done *bool, links *[]waveobj.TaskLink, workspaceId *string) (*waveobj.Task, error) {
	if taskId == "" {
		return nil, fmt.Errorf("taskid is required")
	}
	// Validate before the update closure: nested DB access inside
	// DBUpdateFnErr's transaction deadlocks.
	if workspaceId != nil && *workspaceId != "" {
		if _, err := wstore.DBMustGet[*waveobj.Workspace](ctx, *workspaceId); err != nil {
			return nil, fmt.Errorf("error getting workspace: %w", err)
		}
	}
	var oldWorkspaceId string
	err := wstore.DBUpdateFnErr(ctx, taskId, func(task *waveobj.Task) error {
		oldWorkspaceId = task.WorkspaceId
		if workspaceId != nil {
			task.WorkspaceId = *workspaceId
		}
		if text != nil {
			trimmed := strings.TrimSpace(*text)
			if trimmed == "" {
				return fmt.Errorf("task text is empty")
			}
			task.Text = trimmed
		}
		if links != nil {
			task.Links = *links
		}
		if done != nil && task.Done != *done {
			task.Done = *done
			if *done {
				task.DoneTs = time.Now().UnixMilli()
			} else {
				task.DoneTs = 0
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error updating task: %w", err)
	}
	task, err := wstore.DBMustGet[*waveobj.Task](ctx, taskId)
	if err != nil {
		return nil, fmt.Errorf("error getting updated task: %w", err)
	}
	publishTaskUpdate(task.WorkspaceId)
	if task.WorkspaceId != oldWorkspaceId {
		publishTaskUpdate(oldWorkspaceId)
	}
	return task, nil
}

func DeleteTask(ctx context.Context, taskId string) error {
	if taskId == "" {
		return fmt.Errorf("taskid is required")
	}
	task, err := wstore.DBGet[*waveobj.Task](ctx, taskId)
	if err != nil {
		return fmt.Errorf("error getting task: %w", err)
	}
	if err := wstore.DBDelete(ctx, waveobj.OType_Task, taskId); err != nil {
		return fmt.Errorf("error deleting task: %w", err)
	}
	if task != nil {
		publishTaskUpdate(task.WorkspaceId)
	}
	return nil
}

// ListTasks returns tasks for one workspace, or all tasks when workspaceId is
// empty (global mode). Sorted by CreatedTs ascending, oid as tiebreaker.
func ListTasks(ctx context.Context, workspaceId string) ([]*waveobj.Task, error) {
	all, err := wstore.DBGetAllObjsByType[*waveobj.Task](ctx, waveobj.OType_Task)
	if err != nil {
		return nil, fmt.Errorf("error listing tasks: %w", err)
	}
	rtn := make([]*waveobj.Task, 0, len(all))
	for _, task := range all {
		if workspaceId != "" && task.WorkspaceId != workspaceId {
			continue
		}
		rtn = append(rtn, task)
	}
	sort.Slice(rtn, func(i, j int) bool {
		if rtn[i].CreatedTs != rtn[j].CreatedTs {
			return rtn[i].CreatedTs < rtn[j].CreatedTs
		}
		return rtn[i].OID < rtn[j].OID
	})
	return rtn, nil
}

// DeleteTasksForWorkspace removes every task of a deleted workspace. Best-effort:
// called from workspace teardown, so it logs rather than fails the cascade.
func DeleteTasksForWorkspace(ctx context.Context, workspaceId string) error {
	tasks, err := ListTasks(ctx, workspaceId)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if err := wstore.DBDelete(ctx, waveobj.OType_Task, task.OID); err != nil {
			return fmt.Errorf("error deleting task %s: %w", task.OID, err)
		}
	}
	return nil
}

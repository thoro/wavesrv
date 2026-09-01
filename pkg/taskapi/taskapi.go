// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

// Package taskapi serves a minimal loopback HTTP API for workspace tasks so
// sandboxed processes (e.g. coding agents inside docker containers) can read
// and update tasks without full wshrpc access. Unix sockets cannot cross the
// Docker Desktop VM boundary, so this listens on loopback TCP (reachable from
// containers via host.docker.internal) and authenticates with a
// workspace-scoped JWT that the rpc socket rejects.
package taskapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wavetermdev/waveterm/pkg/panichandler"
	"github.com/wavetermdev/waveterm/pkg/wavejwt"
	"github.com/wavetermdev/waveterm/pkg/waveobj"
	"github.com/wavetermdev/waveterm/pkg/wcore"
	"github.com/wavetermdev/waveterm/pkg/wstore"
)

const (
	EnvTasksPort  = "WAVETERM_TASKS_PORT"
	EnvTasksUrl   = "WAVETERM_TASKS_URL"
	EnvTasksToken = "WAVETERM_TASKS_TOKEN"

	RequestTimeout = 5 * time.Second
	MaxBodySize    = 64 * 1024
)

var (
	portLock   sync.Mutex
	listenPort int
)

func setPort(port int) {
	portLock.Lock()
	defer portLock.Unlock()
	listenPort = port
}

// GetPort returns the tasks API loopback port, or 0 when the server is not running.
func GetPort() int {
	portLock.Lock()
	defer portLock.Unlock()
	return listenPort
}

// MintToken creates a tasks-scoped JWT restricted to one workspace. Scoped
// tokens are rejected by the full rpc authentication path, so leaking one to a
// container only exposes that workspace's tasks.
func MintToken(workspaceId string) (string, error) {
	if workspaceId == "" {
		return "", fmt.Errorf("workspaceid is required")
	}
	return wavejwt.Sign(&wavejwt.WaveJwtClaims{
		Scope:       wavejwt.ScopeTasks,
		WorkspaceId: workspaceId,
	})
}

// StartServer listens on an ephemeral loopback TCP port and serves the tasks
// API. The chosen port is exposed via GetPort for env injection into shells.
func StartServer() error {
	listener, err := net.Listen("tcp", "127.0.0.1:")
	if err != nil {
		return fmt.Errorf("error creating tasks api listener: %w", err)
	}
	setPort(listener.Addr().(*net.TCPAddr).Port)
	log.Printf("Server [tasks-api] listening on %s\n", listener.Addr())
	go func() {
		defer func() {
			panichandler.PanicHandler("taskapi:StartServer", recover())
		}()
		server := &http.Server{
			Handler:        MakeHandler(),
			ReadTimeout:    RequestTimeout,
			WriteTimeout:   RequestTimeout,
			MaxHeaderBytes: 16 * 1024,
		}
		if serveErr := server.Serve(listener); serveErr != nil {
			log.Printf("tasks api server exited: %v\n", serveErr)
		}
	}()
	return nil
}

func MakeHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/prompt", withAuth(handlePrompt))
	mux.HandleFunc("GET /api/tasks", withAuth(handleListTasks))
	mux.HandleFunc("POST /api/tasks", withAuth(handleCreateTask))
	mux.HandleFunc("PATCH /api/tasks/{taskid}", withAuth(handleUpdateTask))
	mux.HandleFunc("DELETE /api/tasks/{taskid}", withAuth(handleDeleteTask))
	return mux
}

type taskHandlerFn func(w http.ResponseWriter, r *http.Request, workspaceId string)

func withAuth(fn taskHandlerFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		tokenStr, found := strings.CutPrefix(authHeader, "Bearer ")
		if !found || tokenStr == "" {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		claims, err := wavejwt.ValidateAndExtract(tokenStr)
		if err != nil {
			writeError(w, http.StatusUnauthorized, fmt.Sprintf("invalid token: %v", err))
			return
		}
		if claims.Scope != wavejwt.ScopeTasks || claims.WorkspaceId == "" {
			writeError(w, http.StatusForbidden, "token is not tasks-scoped")
			return
		}
		fn(w, r, claims.WorkspaceId)
	}
}

func writeJson(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJson(w, status, map[string]string{"error": msg})
}

func reqCtx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), RequestTimeout)
}

func decodeBody(w http.ResponseWriter, r *http.Request, target any) bool {
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodySize)).Decode(target)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return false
	}
	return true
}

// getScopedTask loads a task and verifies it belongs to the token's workspace.
// Missing and foreign tasks both return 404 so a scoped token cannot probe for
// task ids in other workspaces.
func getScopedTask(ctx context.Context, w http.ResponseWriter, taskId string, workspaceId string) *waveobj.Task {
	task, err := wstore.DBGet[*waveobj.Task](ctx, taskId)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("error getting task: %v", err))
		return nil
	}
	if task == nil || task.WorkspaceId != workspaceId {
		writeError(w, http.StatusNotFound, "task not found")
		return nil
	}
	return task
}

type taskCreateBody struct {
	Text string `json:"text"`
}

type taskUpdateBody struct {
	Text *string `json:"text,omitempty"`
	Done *bool   `json:"done,omitempty"`
}

func handleListTasks(w http.ResponseWriter, r *http.Request, workspaceId string) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	tasks, err := wcore.ListTasks(ctx, workspaceId)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("error listing tasks: %v", err))
		return
	}
	writeJson(w, http.StatusOK, tasks)
}

func handleCreateTask(w http.ResponseWriter, r *http.Request, workspaceId string) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	var body taskCreateBody
	if !decodeBody(w, r, &body) {
		return
	}
	task, err := wcore.CreateTask(ctx, workspaceId, body.Text, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("error creating task: %v", err))
		return
	}
	writeJson(w, http.StatusCreated, task)
}

func handleUpdateTask(w http.ResponseWriter, r *http.Request, workspaceId string) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	var body taskUpdateBody
	if !decodeBody(w, r, &body) {
		return
	}
	if getScopedTask(ctx, w, r.PathValue("taskid"), workspaceId) == nil {
		return
	}
	task, err := wcore.UpdateTask(ctx, r.PathValue("taskid"), body.Text, body.Done, nil, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("error updating task: %v", err))
		return
	}
	writeJson(w, http.StatusOK, task)
}

func handleDeleteTask(w http.ResponseWriter, r *http.Request, workspaceId string) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	if getScopedTask(ctx, w, r.PathValue("taskid"), workspaceId) == nil {
		return
	}
	if err := wcore.DeleteTask(ctx, r.PathValue("taskid")); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("error deleting task: %v", err))
		return
	}
	writeJson(w, http.StatusOK, map[string]bool{"deleted": true})
}

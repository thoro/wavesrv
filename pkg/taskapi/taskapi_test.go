// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package taskapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/wavetermdev/waveterm/pkg/wavebase"
	"github.com/wavetermdev/waveterm/pkg/wavejwt"
	"github.com/wavetermdev/waveterm/pkg/waveobj"
	"github.com/wavetermdev/waveterm/pkg/wcore"
	"github.com/wavetermdev/waveterm/pkg/wshutil"
	"github.com/wavetermdev/waveterm/pkg/wstore"
)

func TestMain(m *testing.M) {
	tempDir, err := os.MkdirTemp("", "taskapi-test-*")
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
	keyPair, err := wavejwt.GenerateKeyPair()
	if err != nil {
		panic(err)
	}
	if err := wavejwt.SetPrivateKey(keyPair.PrivateKey); err != nil {
		panic(err)
	}
	if err := wavejwt.SetPublicKey(keyPair.PublicKey); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func makeTestWorkspace(t *testing.T, ctx context.Context) *waveobj.Workspace {
	t.Helper()
	ws := &waveobj.Workspace{OID: uuid.NewString()}
	if err := wstore.DBInsert(ctx, ws); err != nil {
		t.Fatalf("failed to insert workspace: %v", err)
	}
	return ws
}

func doRequest(t *testing.T, server *httptest.Server, method string, path string, token string, body any) (int, []byte) {
	t.Helper()
	var bodyReader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		bodyReader = bytes.NewReader(data)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, server.URL+path, bodyReader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.Bytes()
}

func decodeTasks(t *testing.T, data []byte) []*waveobj.Task {
	t.Helper()
	var tasks []*waveobj.Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		t.Fatalf("unmarshal tasks: %v (body: %s)", err, data)
	}
	return tasks
}

func TestTaskApiAuth(t *testing.T) {
	ctx := context.Background()
	ws := makeTestWorkspace(t, ctx)
	server := httptest.NewServer(MakeHandler())
	defer server.Close()

	status, _ := doRequest(t, server, "GET", "/api/tasks", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", status)
	}

	status, _ = doRequest(t, server, "GET", "/api/tasks", "garbage-token", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("expected 401 with invalid token, got %d", status)
	}

	fullToken, err := wavejwt.Sign(&wavejwt.WaveJwtClaims{RouteId: "test-route"})
	if err != nil {
		t.Fatalf("sign full token: %v", err)
	}
	status, _ = doRequest(t, server, "GET", "/api/tasks", fullToken, nil)
	if status != http.StatusForbidden {
		t.Fatalf("expected 403 with unscoped token, got %d", status)
	}

	scopedToken, err := MintToken(ws.OID)
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	status, _ = doRequest(t, server, "GET", "/api/tasks", scopedToken, nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200 with scoped token, got %d", status)
	}
}

func TestTaskApiWorkspaceScoping(t *testing.T) {
	ctx := context.Background()
	wsA := makeTestWorkspace(t, ctx)
	wsB := makeTestWorkspace(t, ctx)
	taskB, err := wcore.CreateTask(ctx, wsB.OID, "task in b", nil)
	if err != nil {
		t.Fatalf("create task b: %v", err)
	}
	server := httptest.NewServer(MakeHandler())
	defer server.Close()
	tokenA, err := MintToken(wsA.OID)
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}

	status, body := doRequest(t, server, "POST", "/api/tasks", tokenA, map[string]string{"text": "from container"})
	if status != http.StatusCreated {
		t.Fatalf("expected 201 on create, got %d (body: %s)", status, body)
	}
	var created waveobj.Task
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("unmarshal created task: %v", err)
	}
	if created.WorkspaceId != wsA.OID {
		t.Fatalf("created task must land in token workspace, got %q", created.WorkspaceId)
	}

	status, body = doRequest(t, server, "GET", "/api/tasks", tokenA, nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200 on list, got %d", status)
	}
	tasks := decodeTasks(t, body)
	if len(tasks) != 1 || tasks[0].OID != created.OID {
		t.Fatalf("list must only contain workspace A tasks, got %+v", tasks)
	}

	done := true
	status, _ = doRequest(t, server, "PATCH", "/api/tasks/"+taskB.OID, tokenA, map[string]any{"done": done})
	if status != http.StatusNotFound {
		t.Fatalf("expected 404 patching foreign task, got %d", status)
	}
	status, _ = doRequest(t, server, "DELETE", "/api/tasks/"+taskB.OID, tokenA, nil)
	if status != http.StatusNotFound {
		t.Fatalf("expected 404 deleting foreign task, got %d", status)
	}
	if _, err := wstore.DBMustGet[*waveobj.Task](ctx, taskB.OID); err != nil {
		t.Fatalf("foreign task must survive: %v", err)
	}

	status, body = doRequest(t, server, "PATCH", "/api/tasks/"+created.OID, tokenA, map[string]any{"done": true})
	if status != http.StatusOK {
		t.Fatalf("expected 200 patching own task, got %d (body: %s)", status, body)
	}
	var updated waveobj.Task
	if err := json.Unmarshal(body, &updated); err != nil {
		t.Fatalf("unmarshal updated task: %v", err)
	}
	if !updated.Done || updated.DoneTs == 0 {
		t.Fatalf("expected task marked done, got %+v", updated)
	}

	status, _ = doRequest(t, server, "DELETE", "/api/tasks/"+created.OID, tokenA, nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200 deleting own task, got %d", status)
	}
	gone, err := wstore.DBGet[*waveobj.Task](ctx, created.OID)
	if err != nil {
		t.Fatalf("dbget after delete: %v", err)
	}
	if gone != nil {
		t.Fatalf("task must be deleted, got %+v", gone)
	}
}

func TestTaskApiPrompt(t *testing.T) {
	ctx := context.Background()
	ws := makeTestWorkspace(t, ctx)
	server := httptest.NewServer(MakeHandler())
	defer server.Close()

	status, _ := doRequest(t, server, "GET", "/api/prompt", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", status)
	}

	token, err := MintToken(ws.OID)
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	status, body := doRequest(t, server, "GET", "/api/prompt", token, nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if !bytes.Contains(body, []byte("PATCH /api/tasks/{taskid}")) {
		t.Fatalf("prompt body missing endpoint docs: %s", body)
	}
}

func TestTaskApiCreateValidation(t *testing.T) {
	ctx := context.Background()
	ws := makeTestWorkspace(t, ctx)
	server := httptest.NewServer(MakeHandler())
	defer server.Close()
	token, err := MintToken(ws.OID)
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	status, _ := doRequest(t, server, "POST", "/api/tasks", token, map[string]string{"text": "   "})
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty text, got %d", status)
	}
}

func TestScopedTokenRejectedByRpcAuth(t *testing.T) {
	token, err := MintToken(uuid.NewString())
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	if _, err := wshutil.ValidateAndExtractRpcContextFromToken(token); err == nil {
		t.Fatalf("rpc auth must reject tasks-scoped tokens")
	}
}

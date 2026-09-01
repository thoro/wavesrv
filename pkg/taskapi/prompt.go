// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package taskapi

import "net/http"

// PromptText is served on GET /api/prompt so an agent holding a tasks token
// can bootstrap itself: `curl $WAVETERM_TASKS_URL/api/prompt` returns usage
// docs suitable for dropping into a system prompt or CLAUDE.md.
const PromptText = `# Wave Terminal Tasks API

You have access to the task list of the Wave Terminal workspace you are running in.
Tasks are simple todo items the user sees in Wave's tasks pane; changes you make
appear there live. Use them to communicate progress: create tasks for work the
user should know about, and mark tasks done as you complete them.

## Connection

Environment variables (already set in your environment):
- WAVETERM_TASKS_URL: base URL of this API. Inside a docker container this is
  usually rewritten to http://host.docker.internal:<port>; on the host it is
  http://127.0.0.1:<port> (port also available as WAVETERM_TASKS_PORT).
- WAVETERM_TASKS_TOKEN: bearer token, scoped to this workspace only.

Every request needs the header: Authorization: Bearer $WAVETERM_TASKS_TOKEN

## Endpoints

- GET /api/tasks
  Lists this workspace's tasks, sorted by creation time. Returns a JSON array.
- POST /api/tasks
  Body: {"text": "task description"}. Creates a task, returns it (201).
- PATCH /api/tasks/{taskid}
  Body with any of: {"text": "new text", "done": true|false}. Omitted fields
  stay unchanged. Returns the updated task.
- DELETE /api/tasks/{taskid}
  Deletes the task. Returns {"deleted": true}.

## Task shape

{
  "oid": "uuid",            // task id, use in PATCH/DELETE paths
  "workspaceid": "uuid",
  "text": "the task text",
  "done": true,             // omitted when false
  "createdts": 1755772800000,  // unix millis
  "donets": 1755776400000,     // unix millis, omitted when not done
  "meta": {}
}

## Examples

List open tasks:
  curl -s -H "Authorization: Bearer $WAVETERM_TASKS_TOKEN" "$WAVETERM_TASKS_URL/api/tasks"

Create a task:
  curl -s -X POST -H "Authorization: Bearer $WAVETERM_TASKS_TOKEN" \
    -d '{"text":"fix failing integration tests"}' "$WAVETERM_TASKS_URL/api/tasks"

Mark a task done:
  curl -s -X PATCH -H "Authorization: Bearer $WAVETERM_TASKS_TOKEN" \
    -d '{"done":true}' "$WAVETERM_TASKS_URL/api/tasks/<taskid>"

## Rules and limits

- The token is scoped to one workspace; tasks in other workspaces are invisible
  (requests against their ids return 404).
- Task text must be non-empty; it is trimmed server-side.
- Errors return JSON: {"error": "message"} with a 4xx/5xx status.
- Do not delete tasks you did not create unless the user asks; prefer marking
  them done.
`

func handlePrompt(w http.ResponseWriter, r *http.Request, workspaceId string) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(PromptText))
}

This repo is wavesrv, the Go backend of Wave Terminal (workspaces, terminal blocks, durable shells, SSH/WSL connection management), plus `wsh`, the CLI that runs inside its terminals. The client is a separate native macOS app (thoro/wave-native).

### Project Structure

It is Go only. Clients (the native app, `wsh`) talk to it over `wshrpc` a custom RPC protocol that is implemented over websocket (and domain sockets). See `.kilocode/rules/overview.md` for the package map.

### Coding Guidelines

- **Go Conventions**:
  - Don't use custom enum types in Go. Instead, use string constants (e.g., `const StatusRunning = "running"` rather than creating a custom type like `type Status string`).
  - Use string constants for status values, packet types, and other string-based enumerations.
  - in Go code, prefer using Printf() vs Println()
  - use "Make" as opposed to "New" for struct initialization func names
  - in general const decls go at the top of the file (before types and functions)
  - NEVER run `go build` (especially in weird sub-package directories). we can tell if everything compiles by seeing there are no problems/errors.
- **Synchronization**:
  - Always prefer to use the `lock.Lock(); defer lock.Unlock()` pattern for synchronization if possible
  - Avoid inline lock/unlock pairs - instead create helper functions that use the defer pattern
  - When accessing shared data structures (maps, slices, etc.), ensure proper locking
  - Example: Instead of `gc.lock.Lock(); gc.map[key]++; gc.lock.Unlock()`, create a helper function like `getNextValue(key string) int { gc.lock.Lock(); defer gc.lock.Unlock(); gc.map[key]++; return gc.map[key] }`
- **JSON Field Naming**: All fields must be lowercase, without underscores.
- In general, when writing functions, we prefer _early returns_ rather than putting the majority of a function inside of an if block.

### RPC System

To define a new RPC call, add the new definition to `pkg/wshrpc/wshrpctypes.go` including any input/output data that is required. There is no code generator any more: add the client wrapper to `pkg/wshrpc/wshclient/wshclient.go` by hand (see the add-rpc skill). The same goes for the `metaconsts.go` files in `pkg/wconfig` and `pkg/waveobj`, and `schema/`.

For normal "server" RPCs (where a client is calling the main server) you should implement the RPC call in `pkg/wshrpc/wshserver/wshserver.go`.

### Notes

- **CRITICAL: Completion format MUST be: "Done: [one-line description]"**
- **Keep your Task Completed summaries VERY short**
- **No lengthy pre-completion summaries** - Do not provide detailed explanations of implementation before using attempt_completion
- **No recaps of changes** - Skip explaining what was done before completion
- **Go directly to completion** - After making changes, proceed directly to attempt_completion without summarizing
- The project is currently an un-released POC / MVP. Do not worry about backward compatibility when making changes
- When a user explicitly tells you a function handles null inputs, trust them and stop trying to "protect" it with unnecessary checks or workarounds.
- **Match response length to question complexity** - For simple, direct questions in Ask mode (especially those that can be answered in 1-2 sentences), provide equally brief answers. Save detailed explanations for complex topics or when explicitly requested.
- for simple functions, we prefer `if (!cond) { return }; functionality;` pattern over `if (cond) { functionality }` because it produces less indentation and is easier to follow.
- It is now 2026, so if you write new files, or update files use 2026 for the copyright year

### Strict Comment Rules

- **NEVER add comments that merely describe what code is doing**:
  - ❌ `mutex.Lock() // Lock the mutex`
  - ❌ `counter++ // Increment the counter`
  - ❌ `buffer.Write(data) // Write data to buffer`
  - ❌ `// Header component for app run list` (above AppRunListHeader)
  - ❌ `// Updated function to include onClick parameter`
  - ❌ `// Changed padding calculation`
  - ❌ `// Removed unnecessary div`
  - ❌ `// Using the model's width value here`
- **Only use comments for**:
  - Explaining WHY a particular approach was chosen
  - Documenting non-obvious edge cases or side effects
  - Warning about potential pitfalls in usage
  - Explaining complex algorithms that can't be simplified
- **When in doubt, leave it out**. No comment is better than a redundant comment.
- **Never add comments explaining code changes** - The code should speak for itself, and version control tracks changes. The one exception to this rule is if it is a very unobvious implementation. Something that someone would typically implement in a different (wrong) way. Then the comment helps us remember WHY we changed it to a less obvious implementation.
- **Never remove existing comments** unless specifically directed by the user. Comments that are already defined in existing code have been vetted by the user.

### Tool Use

Do NOT use write_to_file unless it is a new file or very short. Always prefer to use replace_in_file. Often your diffs fail when a file may be out of date in your cache vs the actual on-disk format. You should RE-READ the file and try to create diffs again if your diffs fail rather than fall back to write_to_file. If you feel like your ONLY option is to use write_to_file please ask first.

Also when adding content to the end of files prefer to use the new append_file tool rather than trying to create a diff (as your diffs are often not specific enough and end up inserting code in the middle of existing functions).

### Directory Awareness

- **ALWAYS verify the current working directory before executing commands**
- Either run "pwd" first to verify the directory, or do a "cd" to the correct absolute directory before running commands
- When running tests, do not "cd" to the pkg directory and then run the test. This screws up the cwd and you never recover. run the test from the project root instead.

### Testing / Compiling Go Code

No need to run a `go build` or a `go run` to just check if the Go code compiles. VSCode's errors/problems cover this well.
If there are no Go errors in VSCode you can assume the code compiles fine.

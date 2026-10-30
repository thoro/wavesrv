---
name: add-config
description: Guide for adding new configuration settings to Wave Terminal. Use when adding a new setting to the configuration system, implementing a new config key, or adding user-customizable settings.
---

# Adding a New Configuration Setting to Wave Terminal

This guide explains how to add a new configuration setting to Wave Terminal's hierarchical configuration system.

## Configuration System Overview

Wave Terminal uses a hierarchical configuration system with:

1. **Go Struct Definitions** - Type-safe configuration structure in `pkg/wconfig/settingsconfig.go`
2. **JSON Schema** - Editor validation schema in `schema/settings.json` (served by wavesrv, maintained by hand)
3. **Default Values** - Built-in defaults in `pkg/wconfig/defaultconfig/settings.json`
4. **User Configuration** - User overrides in `$WAVETERM_CONFIG_HOME/settings.json`
5. **Block Metadata** - Block-level overrides in `pkg/waveobj/wtypemeta.go` (key constants in `pkg/waveobj/metaconsts.go`)

Settings cascade from defaults → user settings → connection config → block overrides.

## Step-by-Step Guide

### Step 1: Add to Go Struct Definition

Edit `pkg/wconfig/settingsconfig.go` and add your new field to the `SettingsType` struct:

```go
type SettingsType struct {
    // ... existing fields ...

    // Add your new field with appropriate JSON tag
    MyNewSetting string `json:"mynew:setting,omitempty"`

    // For different types:
    MyBoolSetting   bool    `json:"mynew:boolsetting,omitempty"`
    MyNumberSetting float64 `json:"mynew:numbersetting,omitempty"`
    MyIntSetting    *int64  `json:"mynew:intsetting,omitempty"`    // Use pointer for optional ints
    MyArraySetting  []string `json:"mynew:arraysetting,omitempty"`
}
```

**Naming Conventions:**

- Use namespace prefixes (e.g., `term:`, `window:`, `ai:`, `web:`, `app:`)
- Use lowercase with colons as separators
- Field names should be descriptive and follow Go naming conventions
- Use `omitempty` tag to exclude empty values from JSON

**Type Guidelines:**

- Use `*int64` and `*float64` for optional numeric values
- Use `*bool` for optional boolean values (or `bool` if default is false)
- Use `string` for text values
- Use `[]string` for arrays
- Use `float64` for numbers that can be decimals

**Namespace Organization:**

- `app:*` - Application-level settings
- `term:*` - Terminal-specific settings
- `window:*` - Window and UI settings
- `ai:*` - AI-related settings
- `web:*` - Web browser settings
- `editor:*` - Code editor settings
- `conn:*` - Connection settings

### Step 1.5: Add to Block Metadata (Optional)

If your setting should support block-level overrides, also add it to `pkg/waveobj/wtypemeta.go`:

```go
type MetaTSType struct {
    // ... existing fields ...

    // Add your new field with matching JSON tag and type
    MyNewSetting *string `json:"mynew:setting,omitempty"`  // Use pointer for optional values

    // For different types:
    MyBoolSetting   *bool    `json:"mynew:boolsetting,omitempty"`
    MyNumberSetting *float64 `json:"mynew:numbersetting,omitempty"`
    MyIntSetting    *int     `json:"mynew:intsetting,omitempty"`
    MyArraySetting  []string `json:"mynew:arraysetting,omitempty"`
}
```

**Block Metadata Guidelines:**

- Use pointer types (`*string`, `*bool`, `*int`, `*float64`) for optional overrides
- JSON tags should exactly match the corresponding settings field
- This enables the hierarchical config system: block metadata → connection config → global settings
- Only add settings here that make sense to override per-block or per-connection

### Step 2: Set Default Value (Optional)

If your setting should have a default value, add it to `pkg/wconfig/defaultconfig/settings.json`:

```json
{
  "ai:preset": "ai@global",
  "ai:model": "gpt-5-mini",
  // ... existing defaults ...

  "mynew:setting": "default value",
  "mynew:boolsetting": true,
  "mynew:numbersetting": 42.5,
  "mynew:intsetting": 100
}
```

**Default Value Guidelines:**

- Only add defaults for settings that should have non-zero/non-empty initial values
- Ensure defaults make sense for typical user experience
- Keep defaults conservative and safe
- Boolean settings often don't need defaults if `false` is the correct default

### Step 3: Update the JSON Schema and Meta Constants

The generators that used to produce these were removed with the Electron frontend, so update them by hand:

- `schema/settings.json` - add a property for the key, at the same position as the struct field (2-space indent):

  ```json
  "mynew:setting": {
    "type": "string"
  },
  ```

- `pkg/wconfig/metaconsts.go` - add the matching `ConfigKey_*` constant, at the same position as the struct field
- `pkg/waveobj/metaconsts.go` - if you added a `MetaTSType` field, add the matching `MetaKey_*` constant

Both `metaconsts.go` files still carry their "Generated Code" header; edit them anyway.

### Step 4: Use in Backend Code

Access settings in Go code:

```go
// Get the full config
fullConfig := wconfig.GetWatcher().GetFullConfig()

// Access your setting
myValue := fullConfig.Settings.MyNewSetting

// For optional values (pointers)
if fullConfig.Settings.MyIntSetting != nil {
    intValue := *fullConfig.Settings.MyIntSetting
    // Use intValue
}
```

Clients (the native app) read the merged config with the `getfullconfig` RPC and get changes through the `config` event, keyed by the JSON names; a new key needs handling there too.

## Complete Examples

### Example 1: Simple Boolean Setting (No Block Override)

**Use case:** Add a setting to hide the AI button globally

#### 1. Go Struct (`pkg/wconfig/settingsconfig.go`)

```go
type SettingsType struct {
    // ... existing fields ...
    AppHideAiButton bool `json:"app:hideaibutton,omitempty"`
}
```

#### 2. Default Value (`pkg/wconfig/defaultconfig/settings.json`)

```json
{
  "app:hideaibutton": false
}
```

#### 3. Schema and Config Key

`schema/settings.json`:

```json
"app:hideaibutton": {
  "type": "boolean"
},
```

`pkg/wconfig/metaconsts.go`:

```go
ConfigKey_AppHideAiButton = "app:hideaibutton"
```

#### 4. Usage Examples

```bash
# Set in settings file
wsh setconfig app:hideaibutton=true

# Or edit $WAVETERM_CONFIG_HOME/settings.json
{
  "app:hideaibutton": true
}
```

### Example 2: Terminal Setting with Block Override

**Use case:** Add a terminal bell sound setting that can be overridden per block

#### 1. Go Struct (`pkg/wconfig/settingsconfig.go`)

```go
type SettingsType struct {
    // ... existing fields ...
    TermBellSound string `json:"term:bellsound,omitempty"`
}
```

#### 2. Block Metadata (`pkg/waveobj/wtypemeta.go`)

```go
type MetaTSType struct {
    // ... existing fields ...
    TermBellSound *string `json:"term:bellsound,omitempty"`  // Pointer for optional override
}
```

#### 3. Default Value (`pkg/wconfig/defaultconfig/settings.json`)

```json
{
  "term:bellsound": "default"
}
```

#### 4. Schema and Key Constants

`schema/settings.json`:

```json
"term:bellsound": {
  "type": "string"
},
```

`pkg/wconfig/metaconsts.go` and `pkg/waveobj/metaconsts.go`:

```go
ConfigKey_TermBellSound = "term:bellsound"
MetaKey_TermBellSound   = "term:bellsound"
```

#### 5. Usage Examples

```bash
# Set globally in settings file
wsh setconfig term:bellsound="custom.wav"

# Set for current block only
wsh setmeta term:bellsound="none"

# Set for specific block
wsh setmeta --block BLOCK_ID term:bellsound="beep"

# Or edit $WAVETERM_CONFIG_HOME/settings.json
{
  "term:bellsound": "custom.wav"
}
```

## Configuration Patterns

### Clear/Reset Pattern

Each namespace can have a "clear" field for resetting all settings in that namespace:

```go
AppClear  bool `json:"app:*,omitempty"`
TermClear bool `json:"term:*,omitempty"`
```

### Optional vs Required Settings

- Use pointer types (`*bool`, `*int64`, `*float64`) for truly optional settings
- Use regular types for settings that should always have a value
- Provide sensible defaults for important settings

### Block-Level Overrides via RPC

Settings can be overridden at the block level using metadata:

```go
err := wshclient.SetMetaCommand(RpcClient, wshrpc.CommandSetMetaData{
    ORef: waveobj.MakeORef(waveobj.OType_Block, blockId),
    Meta: waveobj.MetaMapType{"mynew:setting": "block-specific value"},
}, nil)
```

## Common Pitfalls

### 1. Forgetting the Schema or Meta Constant

**Problem:** Editors don't validate or complete the new key; no `ConfigKey_*` / `MetaKey_*` constant to reference

**Solution:** Update `schema/settings.json` and the `metaconsts.go` files by hand after modifying the Go structs

### 2. Type Mismatch Between Settings and Metadata

**Problem:** Settings uses `string`, metadata uses `*int`

**Solution:** Ensure types match (except metadata uses pointers for optionals)

### 3. Not Providing Fallback Values

**Problem:** Code breaks if the setting is unset (`omitempty` keys are absent from the JSON)

**Solution:** Check optional (pointer) settings for `nil` and fall back to a default

## Best Practices

### Naming

- **Use descriptive names**: `term:fontsize` not `term:fs`
- **Follow namespace conventions**: Group related settings with common prefix
- **Use consistent casing**: Always lowercase with colons

### Types

- **Use `bool`** for simple on/off settings (no pointer if false is default)
- **Use `*bool`** only if you need to distinguish unset from false
- **Use `*int64`/`*float64`** for optional numeric values
- **Use `string`** for text, paths, or enum-like values
- **Use `[]string`** for lists

### Defaults

- **Provide sensible defaults** for settings users will commonly change
- **Omit defaults** for advanced/optional settings
- **Keep defaults safe** - don't enable experimental features by default

### Block Overrides

- **Enable for view/display settings**: Font sizes, colors, themes, etc.
- **Don't enable for app-wide settings**: Global hotkeys, window behavior, etc.
- **Consider the use case**: Would a user want different values per block or connection?

## Quick Reference

When adding a new configuration setting:

- [ ] Add field to `SettingsType` in `pkg/wconfig/settingsconfig.go`
- [ ] Add field to `MetaTSType` in `pkg/waveobj/wtypemeta.go` (if block override needed)
- [ ] Add default to `pkg/wconfig/defaultconfig/settings.json` (if needed)
- [ ] Add the key to `schema/settings.json` and a `ConfigKey_*` constant to `pkg/wconfig/metaconsts.go` (plus a `MetaKey_*` constant if block override)
- [ ] Handle the key in the clients that use it

## Related Documentation

- **Type Definitions**: `pkg/wconfig/settingsconfig.go` - Go struct definitions
- **Metadata Types**: `pkg/waveobj/wtypemeta.go` - Block metadata definitions

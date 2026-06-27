# Design Plan: Persistent Chrome Renderer

## Problem

Each Wave tab is its own `WebContentsView` (`WaveTabView`) that loads the **full** React app — the
window chrome (tab bar + workspace switcher) **and** the block content. Switching/creating a tab
swaps the entire renderer, so the chrome itself re-renders and looks unanchored until the new tab
finishes its `wave-init` handshake. Symptoms reported:

- New tab "can't switch to until the renderer is ready" (no free renderer → cold boot).
- The whole window structure loses anchoring on tab switch (the tab bar belongs to the swapped renderer).

Root cause: **the chrome is rendered per-tab.** The fix is to render the chrome **once per window**,
persistently, with only the content area swapping per tab.

## Feasibility decision: overlay vs top-strip

The idea of a **single transparent, click-through chrome overlay covering the whole window** (so HTML
dropdowns superimpose over content) is **not achievable in Electron 41**:

- **There is no per-view click-through.** `setIgnoreMouseEvents` is a **window-level** API
  (`BaseWindow`/`BrowserWindow`), not on `WebContentsView`. A child view can be *visually* transparent
  (`setBackgroundColor("#00000000")`) but **still captures every click within its bounds** — hit-testing
  is by view geometry/stacking, not rendered alpha. The feature request for per-view ignore
  ([electron#23863](https://github.com/electron/electron/issues/23863)) is open/unimplemented.
- A separate transparent always-on-top **overlay window** is technically possible but collides badly
  with Wave's heavy macOS native-fullscreen / quake machinery (Spaces ride-along, bounds sync on
  resize, focus races). High risk in this codebase.

**Recommended approach — fixed top-strip chrome view + content view below (no click-through):**

- `contentView` holds two non-overlapping `WebContentsView`s: a **chrome** view in a top strip
  (tab-bar height) and the **content** (tab) view filling the rest.
- HTML dropdowns from the chrome that must extend over the content are handled by:
  1. **Native menus** (`Menu.popup()`) where possible — they already float above all views/windows
     regardless of the split (Wave's context/app menus are already native and unaffected).
  2. **Dynamic chrome height** for rich HTML overlays that can't be native (the `Cmd+Shift+K` tab
     palette, the workspace-switcher popover): temporarily grow the chrome view to full-window height
     (topmost), render the modal overlay, dismiss on outside-click and shrink back.

This eliminates the flicker/anchoring problem and the "switch waits for renderer" problem (the chrome
never swaps), while keeping menus correct.

## Architecture

Two renderer types per window:

| | Chrome renderer (NEW, 1 per window) | Content renderer (per tab, existing) |
|---|---|---|
| Entry | `chrome.html` → `frontend/wave-chrome.ts` | `index.html` → `frontend/wave.ts` |
| Renders | TabBar / VTabBar, WorkspaceSwitcher, UpdateStatusBanner, tab palette | TabContent (block layout), AIPanel |
| RPC route | `window:${windowId}` (new `makeWindowRouteId`) | `tab:${tabId}` (existing) |
| wave-init data | window + workspace objects; active tab = `workspace.activetabid` | tab + layout + blocks (existing) |
| Owns keys | Cmd+T/W, Cmd+1-9, Cmd+[ /], Cmd+Shift+K/O, workspace switch | block nav, AI focus, view-specific keys |

Shared (both): jotai `globalStore`, `fullConfigAtom`/settings/theme, the `workspace` WaveObj (via WOS
subscription), platform/util/fonts.

## Phased implementation

**Phase 0 — spike (de-risk the seam).** Stand up an empty second renderer: add the `chrome.html`
entry + `wave-chrome.ts` to `electron.vite.config.ts` `rollupOptions.input`; in emain, create a chrome
`WebContentsView` per window, position it as a top strip, and shrink the content view below it. Verify:
both views render, transparency/z-order, dev-server + packaged file loading, and that a window-scoped
wsh route (`window:${windowId}`) connects. No chrome UI yet. **Gate before proceeding.**

**Phase 1 — render the chrome in its own renderer.** Move TabBar/VTabBar/WorkspaceSwitcher/banner into
`wave-chrome.ts`'s render root. Source data window-scoped: load the `window`+`workspace` WaveObjs,
derive active tab from `workspace.activetabid` (not `atoms.staticTabId`). Tab actions call the existing
Electron APIs (`createTab`, `setActiveTab`, `closeTab`) which already operate by workspace/window.

**Phase 2 — content renderer drops the chrome.** `wave.ts`/`workspace.tsx` render only TabContent +
AIPanel; the content view is positioned below the chrome strip. Remove the per-tab tab bar.

**Phase 3 — keyboard + focus routing.** Decide ownership: global hotkeys live in the chrome renderer
(always present); keys that arrive in the focused content renderer and belong to chrome get reinjected
(reuse the existing `onReinjectKey` / `register-global-webview-keys` IPC bridge). Verify Cmd+T/W/1-9,
Cmd+Shift+K/O, tab nav, and that typing focus stays in the content/block.

**Phase 4 — HTML dropdowns over content.** Implement dynamic chrome-height growth for the workspace
switcher popover and the Cmd+Shift+K palette (grow chrome view to full window while open, shrink on
dismiss). Convert any dropdown that can be native to `Menu.popup()`.

**Phase 5 — lifecycle + perf.** Chrome view creation/teardown with the window; ensure the content
hot-spare model still applies to content views; confirm tab switch only repositions content views
(chrome untouched) → instant, anchored switch.

## Risks / open questions

- **Keyboard routing across two renderers** is the trickiest part (Phase 3). The reinject bridge exists
  but needs careful ownership rules to avoid double-handling or dropped keys.
- **Dynamic chrome height** briefly makes the chrome view cover (and capture clicks over) the content
  while a dropdown is open — fine for modal dropdowns (outside-click dismisses), but must be reliable.
- **Two wsh connections per window** (chrome + each content tab); confirm the router and event scopes
  (`waveobj:update` for the workspace) reach the chrome renderer.
- **Drag-and-drop** of tabs now lives entirely in the chrome renderer (simpler — all tab bars in one
  persistent renderer); cross-window tab drag still resolves via the existing screen-point hit-test.
- **macOS traffic lights / window drag regions** are currently in the tab bar renderer; they move to the
  chrome strip — verify `WebkitAppRegion: drag` and the traffic-light offset still work.
- **Effort:** multi-day, phased. Each phase is independently testable; Phase 0 is the go/no-go.

## Files touched (anticipated)

- `electron.vite.config.ts` (2nd entry), `index.html` + new `chrome.html`
- `frontend/wave.ts` (content-only boot) + new `frontend/wave-chrome.ts`
- `frontend/app/app.tsx`, `frontend/app/workspace/workspace.tsx` (split chrome/content roots)
- `frontend/app/store/wshrouter.ts` (`makeWindowRouteId`), `frontend/app/store/wshrpcutil.ts`
- `emain/emain-window.ts` (chrome view lifecycle + top-strip layout, content positioning),
  `emain/emain-tabview.ts` (content view bounds below chrome)
- `frontend/app/store/keymodel.ts` + preload key-reinject bridge (Phase 3)
- chrome components: `tab/tabbar.tsx`, `tab/vtabbar.tsx`, `tab/workspaceswitcher.tsx`,
  `tab/tabswitcher.tsx`, `tab/updatebanner.tsx`

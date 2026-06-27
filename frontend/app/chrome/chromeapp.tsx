// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { globalStore } from "@/app/store/jotaiStore";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import { moveTabToWorkspaceAtPoint, setTabDragImage, WaveTabDragType } from "@/app/tab/crosswindowmove";
import { buildTabContextMenu, getMoveTabTargets } from "@/app/tab/tabcontextmenu";
import { WorkspaceSwitcher, workspaceSwitcherOpenAtom } from "@/app/tab/workspaceswitcher";
import { useWaveEnv, WaveEnvContext } from "@/app/waveenv/waveenv";
import { makeWaveEnvImpl } from "@/app/waveenv/waveenvimpl";
import { atoms, getApi } from "@/store/global";
import { makeORef } from "@/store/wos";
import { isMacOS } from "@/util/platformutil";
import { cn, fireAndForget } from "@/util/util";
import { atom, Provider, useAtomValue } from "jotai";
import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";

import "./chromeapp.scss";

const DragRegion = { WebkitAppRegion: "drag" } as React.CSSProperties;
const NoDragRegion = { WebkitAppRegion: "no-drag" } as React.CSSProperties;
// keep in sync with .chrome-tab width in chromeapp.scss
const ChromeTabWidth = 130;

// reflects the active content tab's AI panel open state (pushed from emain via chrome-waveai-state)
export const chromeWaveAIOpenAtom = atom(false);
// true while the active content tab's renderer is still loading (pushed via chrome-loading)
export const chromeLoadingAtom = atom(false);

// ChromeApp is the root of the persistent chrome renderer. It renders the window-scoped tab bar
// + workspace switcher (the chrome strip pinned above the tab content views). It deliberately does
// NOT mount TabModelContext / the block layout — only chrome UI lives here.
const ChromeApp = ({ onFirstRender }: { onFirstRender: () => void }) => {
    const waveEnvRef = useRef(makeWaveEnvImpl());
    useEffect(() => {
        onFirstRender();
    }, []);
    return (
        <Provider store={globalStore}>
            <WaveEnvContext.Provider value={waveEnvRef.current}>
                <ChromeContent />
            </WaveEnvContext.Provider>
        </Provider>
    );
};

const ChromeContent = () => {
    const env = useWaveEnv();
    const workspace = useAtomValue(atoms.workspace);
    const tabIds = workspace?.tabids ?? [];
    const activeTabId = workspace?.activetabid;
    const hideAiButton = useAtomValue(env.getSettingsKeyAtom("app:hideaibutton"));
    const aiOpen = useAtomValue(chromeWaveAIOpenAtom);
    const loading = useAtomValue(chromeLoadingAtom);
    const [draggingTab, setDraggingTab] = useState<string>(null);
    const [dropIndex, setDropIndex] = useState<number>(null);
    const dragTabIdRef = useRef<string>(null);
    const dropIndexRef = useRef<number>(null);
    // foreground the chrome view while a popover is open (emain foregrounds it for loading on its
    // own); the spinner below is rendered when `loading` and shown because emain has foregrounded it
    const wsSwitcherOpen = useAtomValue(workspaceSwitcherOpenAtom);
    useEffect(() => {
        getApi().setChromeOverlay(wsSwitcherOpen);
    }, [wsSwitcherOpen]);

    const clearDrag = () => {
        dragTabIdRef.current = null;
        dropIndexRef.current = null;
        setDraggingTab(null);
        setDropIndex(null);
    };
    const handleTabDragStart = (event: React.DragEvent<HTMLDivElement>, tabId: string) => {
        dragTabIdRef.current = tabId;
        event.dataTransfer.effectAllowed = "move";
        event.dataTransfer.setData("text/plain", tabId);
        event.dataTransfer.setData(WaveTabDragType, tabId);
        setTabDragImage(event);
        setDraggingTab(tabId);
    };
    const handleTabDragOver = (event: React.DragEvent<HTMLDivElement>, index: number) => {
        if (dragTabIdRef.current == null) {
            return;
        }
        event.preventDefault();
        event.dataTransfer.dropEffect = "move";
        const rect = event.currentTarget.getBoundingClientRect();
        const insertBefore = event.clientX - rect.left < rect.width / 2;
        const next = insertBefore ? index : index + 1;
        dropIndexRef.current = next;
        setDropIndex(next);
    };
    const reorderTabs = (target: number) => {
        const source = dragTabIdRef.current;
        if (source == null || workspace == null) {
            return;
        }
        const order = [...tabIds];
        const sourceIndex = order.indexOf(source);
        if (sourceIndex === -1) {
            return;
        }
        const bounded = Math.max(0, Math.min(target, order.length));
        const adjusted = sourceIndex < bounded ? bounded - 1 : bounded;
        if (sourceIndex === adjusted) {
            return;
        }
        order.splice(sourceIndex, 1);
        order.splice(adjusted, 0, source);
        fireAndForget(() => env.rpc.UpdateWorkspaceTabIdsCommand(TabRpcClient, workspace.oid, order));
    };
    const handleTabDrop = (event: React.DragEvent<HTMLDivElement>) => {
        event.preventDefault();
        if (dropIndexRef.current != null) {
            reorderTabs(dropIndexRef.current);
        }
        clearDrag();
    };
    const handleTabDragEnd = (event: React.DragEvent<HTMLDivElement>) => {
        const source = dragTabIdRef.current;
        const screenX = event.screenX;
        const screenY = event.screenY;
        clearDrag();
        if (source != null && workspace != null) {
            fireAndForget(() => moveTabToWorkspaceAtPoint(workspace.oid, source, screenX, screenY));
        }
    };
    const dropLineVisible = draggingTab != null && dropIndex != null;

    // The strip is NOT a drag region (a macOS drag region eats mousemove/hover). Only the empty
    // spacer between the tabs and the AI button is draggable, so the window still moves.
    return (
        <>
            <div
                className={cn(
                    "chrome-strip flex w-full items-center gap-2 overflow-hidden px-2",
                    isMacOS() ? "pl-[78px]" : ""
                )}
                style={NoDragRegion}
            >
                <div className="shrink-0">
                    <WorkspaceSwitcher />
                </div>
                <div className="chrome-tabs-row flex min-w-0 shrink items-center">
                    {tabIds.map((tabId, index) => (
                        <ChromeTab
                            key={tabId}
                            tabId={tabId}
                            active={tabId === activeTabId}
                            workspaceId={workspace?.oid}
                            isDragging={draggingTab === tabId}
                            onTabDragStart={(e) => handleTabDragStart(e, tabId)}
                            onTabDragOver={(e) => handleTabDragOver(e, index)}
                            onTabDrop={handleTabDrop}
                            onTabDragEnd={handleTabDragEnd}
                        />
                    ))}
                    {dropLineVisible && (
                        <div className="chrome-tab-drop-line" style={{ left: dropIndex * ChromeTabWidth }} />
                    )}
                    <button type="button" className="chrome-newtab" onClick={() => getApi().createTab()} title="New Tab">
                        <i className="fa fa-solid fa-plus" />
                    </button>
                </div>
                <div className="h-full flex-1" style={DragRegion} />
                {!hideAiButton && (
                    <button
                        type="button"
                        className={cn("chrome-aibutton", { active: aiOpen })}
                        onClick={() => getApi().toggleWaveAI()}
                        title="Toggle Wave AI Panel"
                    >
                        <i className="fa fa-sparkles" />
                    </button>
                )}
            </div>
            {loading &&
                createPortal(
                    <div className="chrome-loading-overlay">
                        <div className="chrome-spinner" />
                    </div>,
                    document.body
                )}
        </>
    );
};

type ChromeTabProps = {
    tabId: string;
    active: boolean;
    workspaceId: string;
    isDragging: boolean;
    onTabDragStart: (event: React.DragEvent<HTMLDivElement>) => void;
    onTabDragOver: (event: React.DragEvent<HTMLDivElement>) => void;
    onTabDrop: (event: React.DragEvent<HTMLDivElement>) => void;
    onTabDragEnd: (event: React.DragEvent<HTMLDivElement>) => void;
};

const ChromeTab = ({
    tabId,
    active,
    workspaceId,
    isDragging,
    onTabDragStart,
    onTabDragOver,
    onTabDrop,
    onTabDragEnd,
}: ChromeTabProps) => {
    const env = useWaveEnv();
    const [tabData] = env.wos.useWaveObjectValue<Tab>(makeORef("tab", tabId));
    const name = tabData?.name ?? "";
    const [editing, setEditing] = useState(false);
    const inputRef = useRef<HTMLInputElement>(null);
    // buildTabContextMenu's "Rename Tab" item calls renameRef -> start inline rename
    const renameRef = useRef<() => void>(null);
    renameRef.current = () => setEditing(true);

    useEffect(() => {
        if (editing) {
            inputRef.current?.focus();
            inputRef.current?.select();
        }
    }, [editing]);

    const closeTab = () => fireAndForget(() => getApi().closeTab(workspaceId, tabId, false));
    const commitRename = () => {
        const newName = inputRef.current?.value?.trim();
        setEditing(false);
        if (newName && newName !== name) {
            fireAndForget(() => env.rpc.UpdateTabNameCommand(TabRpcClient, tabId, newName));
        }
    };
    const handleContextMenu = (event: React.MouseEvent<HTMLDivElement>) => {
        event.preventDefault();
        const contextEvent = event;
        fireAndForget(async () => {
            const moveTargets = await getMoveTabTargets(env);
            const menu = buildTabContextMenu(tabId, renameRef, () => closeTab(), env, moveTargets);
            env.showContextMenu(menu, contextEvent);
        });
    };

    return (
        <div
            className={cn("chrome-tab", { active, dragging: isDragging })}
            draggable={!editing}
            onClick={() => {
                if (!editing) {
                    getApi().setActiveTab(tabId);
                }
            }}
            onDragStart={onTabDragStart}
            onDragOver={onTabDragOver}
            onDrop={onTabDrop}
            onDragEnd={onTabDragEnd}
            onContextMenu={handleContextMenu}
        >
            <div className="chrome-tab-inner">
                {editing ? (
                    <input
                        ref={inputRef}
                        className="chrome-tab-rename"
                        defaultValue={name}
                        onClick={(e) => e.stopPropagation()}
                        onKeyDown={(e) => {
                            if (e.key === "Enter") {
                                e.preventDefault();
                                commitRename();
                            } else if (e.key === "Escape") {
                                e.preventDefault();
                                setEditing(false);
                            }
                        }}
                        onBlur={commitRename}
                    />
                ) : (
                    <>
                        <span className="chrome-tab-name">{name}</span>
                        <button
                            type="button"
                            className="chrome-tab-close"
                            onClick={(e) => {
                                e.stopPropagation();
                                closeTab();
                            }}
                            title="Close Tab"
                        >
                            <i className="fa fa-solid fa-xmark" />
                        </button>
                    </>
                )}
            </div>
        </div>
    );
};

export { ChromeApp };

// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { globalStore } from "@/app/store/jotaiStore";
import { WorkspaceSwitcher, workspaceSwitcherOpenAtom } from "@/app/tab/workspaceswitcher";
import { useWaveEnv, WaveEnvContext } from "@/app/waveenv/waveenv";
import { makeWaveEnvImpl } from "@/app/waveenv/waveenvimpl";
import { atoms, getApi } from "@/store/global";
import { makeORef } from "@/store/wos";
import { isMacOS } from "@/util/platformutil";
import { cn, fireAndForget } from "@/util/util";
import { atom, Provider, useAtomValue } from "jotai";
import { useEffect, useRef } from "react";
import { createPortal } from "react-dom";

import "./chromeapp.scss";

const DragRegion = { WebkitAppRegion: "drag" } as React.CSSProperties;
const NoDragRegion = { WebkitAppRegion: "no-drag" } as React.CSSProperties;

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
    // foreground the chrome view while a popover is open (emain foregrounds it for loading on its
    // own); the spinner below is rendered when `loading` and shown because emain has foregrounded it
    const wsSwitcherOpen = useAtomValue(workspaceSwitcherOpenAtom);
    useEffect(() => {
        getApi().setChromeOverlay(wsSwitcherOpen);
    }, [wsSwitcherOpen]);
    // the strip itself is a window-drag region; interactive children opt out with no-drag, and the
    // empty area to the right of the tabs stays draggable for moving the window
    return (
        <>
        <div
            className={cn("chrome-strip flex w-full items-center gap-2 overflow-hidden px-2", isMacOS() ? "pl-[78px]" : "")}
            style={DragRegion}
        >
            <div className="shrink-0" style={NoDragRegion}>
                <WorkspaceSwitcher />
            </div>
            <div className="flex min-w-0 shrink items-center overflow-x-auto" style={NoDragRegion}>
                {tabIds.map((tabId) => (
                    <ChromeTab key={tabId} tabId={tabId} active={tabId === activeTabId} workspaceId={workspace?.oid} />
                ))}
                <button type="button" className="chrome-newtab" onClick={() => getApi().createTab()} title="New Tab">
                    <i className="fa fa-solid fa-plus" />
                </button>
            </div>
            {!hideAiButton && (
                <button
                    type="button"
                    className={cn("chrome-aibutton ml-auto", { active: aiOpen })}
                    style={NoDragRegion}
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

const ChromeTab = ({ tabId, active, workspaceId }: { tabId: string; active: boolean; workspaceId: string }) => {
    const env = useWaveEnv();
    const [tabData] = env.wos.useWaveObjectValue<Tab>(makeORef("tab", tabId));
    const name = tabData?.name ?? "";
    return (
        <div className={cn("chrome-tab", { active })} onClick={() => getApi().setActiveTab(tabId)}>
            <div className="chrome-tab-inner">
                <span className="chrome-tab-name">{name}</span>
                <button
                    type="button"
                    className="chrome-tab-close"
                    onClick={(e) => {
                        e.stopPropagation();
                        fireAndForget(() => getApi().closeTab(workspaceId, tabId, false));
                    }}
                    title="Close Tab"
                >
                    <i className="fa fa-solid fa-xmark" />
                </button>
            </div>
        </div>
    );
};

export { ChromeApp };

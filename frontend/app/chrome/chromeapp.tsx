// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { globalStore } from "@/app/store/jotaiStore";
import { WorkspaceSwitcher } from "@/app/tab/workspaceswitcher";
import { useWaveEnv, WaveEnvContext } from "@/app/waveenv/waveenv";
import { makeWaveEnvImpl } from "@/app/waveenv/waveenvimpl";
import { atoms, getApi } from "@/store/global";
import { makeORef } from "@/store/wos";
import { isMacOS } from "@/util/platformutil";
import { cn, fireAndForget } from "@/util/util";
import { Provider, useAtomValue } from "jotai";
import { useEffect, useRef } from "react";

import "./chromeapp.scss";

const DragRegion = { WebkitAppRegion: "drag" } as React.CSSProperties;
const NoDragRegion = { WebkitAppRegion: "no-drag" } as React.CSSProperties;

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
    const workspace = useAtomValue(atoms.workspace);
    const tabIds = workspace?.tabids ?? [];
    const activeTabId = workspace?.activetabid;
    // the strip itself is a window-drag region; interactive children opt out with no-drag, and the
    // empty area to the right of the tabs stays draggable for moving the window
    return (
        <div
            className={cn("chrome-strip flex h-full w-full items-center gap-2 overflow-hidden px-2", isMacOS() ? "pl-[78px]" : "")}
            style={DragRegion}
        >
            <div className="shrink-0" style={NoDragRegion}>
                <WorkspaceSwitcher />
            </div>
            <div className="flex min-w-0 shrink items-center gap-1 overflow-x-auto" style={NoDragRegion}>
                {tabIds.map((tabId) => (
                    <ChromeTab key={tabId} tabId={tabId} active={tabId === activeTabId} workspaceId={workspace?.oid} />
                ))}
                <button
                    type="button"
                    className="flex h-[26px] shrink-0 cursor-pointer items-center rounded px-2 text-[13px] text-secondary hover:bg-hoverbg hover:text-primary"
                    onClick={() => getApi().createTab()}
                    title="New Tab"
                >
                    <i className="fa fa-solid fa-plus" />
                </button>
            </div>
        </div>
    );
};

const ChromeTab = ({ tabId, active, workspaceId }: { tabId: string; active: boolean; workspaceId: string }) => {
    const env = useWaveEnv();
    const [tabData] = env.wos.useWaveObjectValue<Tab>(makeORef("tab", tabId));
    const name = tabData?.name ?? "";
    return (
        <div
            className={cn(
                "group flex h-[26px] min-w-[90px] max-w-[220px] shrink-0 cursor-pointer items-center justify-between gap-2 rounded px-3 text-[13px]",
                active ? "bg-accent/20 text-primary" : "text-secondary hover:bg-hoverbg hover:text-primary"
            )}
            onClick={() => getApi().setActiveTab(tabId)}
        >
            <span className="overflow-hidden text-ellipsis whitespace-nowrap">{name}</span>
            <button
                type="button"
                className="flex shrink-0 cursor-pointer items-center opacity-0 hover:text-primary group-hover:opacity-100"
                onClick={(e) => {
                    e.stopPropagation();
                    fireAndForget(() => getApi().closeTab(workspaceId, tabId, false));
                }}
                title="Close Tab"
            >
                <i className="fa fa-solid fa-xmark text-[10px]" />
            </button>
        </div>
    );
};

export { ChromeApp };

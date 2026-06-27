// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { globalStore } from "@/app/store/jotaiStore";
import { WorkspaceSwitcher } from "@/app/tab/workspaceswitcher";
import { useWaveEnv, WaveEnvContext } from "@/app/waveenv/waveenv";
import { makeWaveEnvImpl } from "@/app/waveenv/waveenvimpl";
import { atoms, getApi } from "@/store/global";
import { makeORef } from "@/store/wos";
import { cn, fireAndForget } from "@/util/util";
import { isMacOS } from "@/util/platformutil";
import { Provider, useAtomValue } from "jotai";
import { useEffect, useRef } from "react";

import "../tailwindsetup.css";

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
    // the strip is a window-drag region; interactive children opt out with no-drag
    return (
        <div
            className={cn("flex h-full w-full items-center gap-1 overflow-hidden px-2", isMacOS() ? "pl-[80px]" : "")}
            style={{ WebkitAppRegion: "drag" } as React.CSSProperties}
        >
            <div style={{ WebkitAppRegion: "no-drag" } as React.CSSProperties}>
                <WorkspaceSwitcher />
            </div>
            <div
                className="flex h-full min-w-0 flex-1 items-center gap-1 overflow-x-auto"
                style={{ WebkitAppRegion: "no-drag" } as React.CSSProperties}
            >
                {tabIds.map((tabId) => (
                    <ChromeTab key={tabId} tabId={tabId} active={tabId === activeTabId} workspaceId={workspace?.oid} />
                ))}
            </div>
            <button
                type="button"
                className="flex h-[22px] shrink-0 cursor-pointer items-center rounded px-2 text-[12px] text-secondary hover:bg-hoverbg hover:text-primary"
                style={{ WebkitAppRegion: "no-drag" } as React.CSSProperties}
                onClick={() => getApi().createTab()}
                title="New Tab"
            >
                <i className="fa fa-solid fa-plus" />
            </button>
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
                "group flex h-[24px] max-w-[200px] shrink-0 cursor-pointer items-center gap-1 rounded px-2 text-[12px]",
                active ? "bg-accent/20 text-primary" : "text-secondary hover:bg-hoverbg hover:text-primary"
            )}
            onClick={() => getApi().setActiveTab(tabId)}
        >
            <span className="overflow-hidden text-ellipsis whitespace-nowrap">{name}</span>
            <button
                type="button"
                className="flex cursor-pointer items-center opacity-0 hover:text-primary group-hover:opacity-100"
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

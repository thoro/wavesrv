// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { globalStore } from "@/app/store/jotaiStore";
import { useWaveEnv, WaveEnv, WaveEnvSubset } from "@/app/waveenv/waveenv";
import { fuzzyMatch } from "@/util/fuzzymatch";
import { makeIconClass } from "@/util/util";
import clsx from "clsx";
import { atom, PrimitiveAtom, useAtom, useAtomValue } from "jotai";
import { OverlayScrollbarsComponent } from "overlayscrollbars-react";
import { useEffect, useMemo, useRef, useState } from "react";
import ReactDOM from "react-dom";
import { makeORef } from "../store/wos";
import "./tabswitcher.scss";

export type TabSwitcherEnv = WaveEnvSubset<{
    electron: {
        setActiveTab: WaveEnv["electron"]["setActiveTab"];
    };
    atoms: {
        workspace: WaveEnv["atoms"]["workspace"];
    };
    wos: WaveEnv["wos"];
}>;

const tabSwitcherOpenAtom: PrimitiveAtom<boolean> = atom(false);

type TabEntry = {
    id: string;
    name: string;
    flagColor: string;
};

type FilteredTab = {
    tab: TabEntry;
    positions: number[];
};

const TabSwitcher = () => {
    const env = useWaveEnv<TabSwitcherEnv>();
    const workspace = useAtomValue(env.atoms.workspace);
    const [isOpen, setIsOpen] = useAtom(tabSwitcherOpenAtom);
    const [searchText, setSearchText] = useState("");
    const [selectedIndex, setSelectedIndex] = useState(0);
    const searchInputRef = useRef<HTMLInputElement>(null);

    const tabEntries: TabEntry[] = useMemo(() => {
        const tabIds = workspace?.tabids ?? [];
        return tabIds.map((id) => {
            const tab = globalStore.get(env.wos.getWaveObjectAtom<Tab>(makeORef("tab", id)));
            return {
                id,
                name: tab?.name ?? "",
                flagColor: (tab?.meta?.["tab:flagcolor"] as string) ?? null,
            };
        });
        // isOpen re-reads the (possibly renamed) tab objects each time the palette opens
    }, [workspace?.tabids, isOpen]);

    const filteredTabs: FilteredTab[] = useMemo(() => {
        const query = searchText.trim();
        if (!query) {
            return tabEntries.map((tab) => ({ tab, positions: [] }));
        }
        return tabEntries
            .map((tab) => {
                const res = fuzzyMatch(query, tab.name);
                return { tab, positions: res.positions, score: res.score, matched: res.matched };
            })
            .filter((e) => e.matched)
            .sort((a, b) => b.score - a.score)
            .map(({ tab, positions }) => ({ tab, positions }));
    }, [tabEntries, searchText]);

    useEffect(() => {
        if (!isOpen) {
            return;
        }
        setSearchText("");
        setSelectedIndex(0);
        requestAnimationFrame(() => searchInputRef.current?.focus());
    }, [isOpen]);

    useEffect(() => {
        if (!isOpen) {
            return;
        }
        const selectedEl = document.querySelector(".tab-switcher-content .keyboard-selected");
        selectedEl?.scrollIntoView({ block: "nearest" });
    }, [selectedIndex, isOpen]);

    const selectTab = (entry: FilteredTab) => {
        if (!entry) {
            return;
        }
        env.electron.setActiveTab(entry.tab.id);
        setIsOpen(false);
    };

    const handleSearchKeyDown = (e: React.KeyboardEvent) => {
        if (e.key === "ArrowDown") {
            e.preventDefault();
            e.stopPropagation();
            setSelectedIndex((prev) => Math.min(prev + 1, filteredTabs.length - 1));
        } else if (e.key === "ArrowUp") {
            e.preventDefault();
            e.stopPropagation();
            setSelectedIndex((prev) => Math.max(prev - 1, 0));
        } else if (e.key === "Enter") {
            e.preventDefault();
            e.stopPropagation();
            selectTab(filteredTabs[selectedIndex]);
        } else if (e.key === "Escape") {
            e.preventDefault();
            e.stopPropagation();
            setIsOpen(false);
        }
    };

    if (!isOpen) {
        return null;
    }

    const activeTabId = workspace?.activetabid;

    return ReactDOM.createPortal(
        <div className="tab-switcher-backdrop" onMouseDown={() => setIsOpen(false)}>
            <div className="tab-switcher-content" onMouseDown={(e) => e.stopPropagation()}>
                <input
                    ref={searchInputRef}
                    className="tab-switcher-search"
                    type="text"
                    placeholder="Search tabs..."
                    value={searchText}
                    onChange={(e) => {
                        setSearchText(e.target.value);
                        setSelectedIndex(0);
                    }}
                    onKeyDown={handleSearchKeyDown}
                />
                <OverlayScrollbarsComponent className="scrollable" options={{ scrollbars: { autoHide: "leave" } }}>
                    {filteredTabs.length === 0 ? (
                        <div className="tab-switcher-empty">No matching tabs</div>
                    ) : (
                        filteredTabs.map((entry, i) => (
                            <div
                                key={entry.tab.id}
                                className={clsx("tab-switcher-item", {
                                    "keyboard-selected": i === selectedIndex,
                                    "is-active": entry.tab.id === activeTabId,
                                })}
                                onMouseEnter={() => setSelectedIndex(i)}
                                onClick={() => selectTab(entry)}
                            >
                                {entry.tab.flagColor ? (
                                    <i
                                        className={clsx("flag-icon", makeIconClass("flag", false))}
                                        style={{ color: entry.tab.flagColor }}
                                    />
                                ) : (
                                    <span className="flag-icon-placeholder" />
                                )}
                                <span className="tab-name">{highlightMatch(entry.tab.name, entry.positions)}</span>
                                {entry.tab.id === activeTabId ? (
                                    <i className={clsx("active-check", makeIconClass("check", false))} />
                                ) : null}
                            </div>
                        ))
                    )}
                </OverlayScrollbarsComponent>
            </div>
        </div>,
        document.body
    );
};

function highlightMatch(name: string, positions: number[]) {
    if (!positions || positions.length === 0) {
        return name;
    }
    const posSet = new Set(positions);
    return name.split("").map((ch, i) =>
        posSet.has(i) ? (
            <span key={i} className="match">
                {ch}
            </span>
        ) : (
            <span key={i}>{ch}</span>
        )
    );
}

export { TabSwitcher, tabSwitcherOpenAtom };

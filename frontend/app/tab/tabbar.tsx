// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { Tooltip } from "@/app/element/tooltip";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import { useWaveEnv } from "@/app/waveenv/waveenv";
import { WorkspaceLayoutModel } from "@/app/workspace/workspace-layout-model";
import { deleteLayoutModelForTab } from "@/layout/index";
import { isMacOSTahoeOrLater } from "@/util/platformutil";
import { fireAndForget } from "@/util/util";
import { useAtomValue } from "jotai";
import { OverlayScrollbars } from "overlayscrollbars";
import { createRef, memo, useCallback, useEffect, useRef, useState } from "react";
import { debounce } from "throttle-debounce";
import { moveTabToWorkspaceAtPoint, setTabDragImage, WaveTabDragType } from "./crosswindowmove";
import { Tab } from "./tab";
import "./tabbar.scss";
import { TabBarEnv } from "./tabbarenv";
import { UpdateStatusBanner } from "./updatebanner";
import { WorkspaceSwitcher } from "./workspaceswitcher";

const TabDefaultWidth = 130;
const TabMinWidth = 100;
const TabMaxWidth = 260;
const MacOSTrafficLightsWidth = 74;
const MacOSTahoeTrafficLightsWidth = 80;

const OSOptions = {
    overflow: {
        x: "scroll",
        y: "hidden",
    },
    scrollbars: {
        theme: "os-theme-dark",
        visibility: "auto",
        autoHide: "leave",
        autoHideDelay: 1300,
        autoHideSuspend: false,
        dragScroll: true,
        clickScroll: false,
        pointers: ["mouse", "touch", "pen"],
    },
};

interface TabBarProps {
    workspace: Workspace;
    noTabs?: boolean;
}

const WaveAIButton = memo(({ divRef }: { divRef?: React.RefObject<HTMLDivElement> }) => {
    const env = useWaveEnv<TabBarEnv>();
    const aiPanelOpen = useAtomValue(WorkspaceLayoutModel.getInstance().panelVisibleAtom);
    const hideAiButton = useAtomValue(env.getSettingsKeyAtom("app:hideaibutton"));

    const onClick = () => {
        const currentVisible = WorkspaceLayoutModel.getInstance().getAIPanelVisible();
        WorkspaceLayoutModel.getInstance().setAIPanelVisible(!currentVisible);
    };

    if (hideAiButton) {
        return null;
    }

    return (
        <Tooltip
            content="Toggle Wave AI Panel"
            placement="bottom"
            hideOnClick
            divClassName={`flex h-[22px] px-3.5 justify-end mb-1 items-center rounded-md mr-1 box-border cursor-pointer bg-hover hover:bg-hoverbg transition-colors text-[12px] ${aiPanelOpen ? "text-accent" : "text-secondary"}`}
            divStyle={{ WebkitAppRegion: "no-drag" } as React.CSSProperties}
            divOnClick={onClick}
            divRef={divRef}
        >
            <i className="fa fa-sparkles" />
        </Tooltip>
    );
});
WaveAIButton.displayName = "WaveAIButton";

function strArrayIsEqual(a: string[], b: string[]) {
    // null check
    if (a == null && b == null) {
        return true;
    }
    if (a == null || b == null) {
        return false;
    }
    if (a.length !== b.length) {
        return false;
    }
    for (let i = 0; i < a.length; i++) {
        if (a[i] !== b[i]) {
            return false;
        }
    }
    return true;
}

const TabBar = memo(({ workspace, noTabs }: TabBarProps) => {
    const env = useWaveEnv<TabBarEnv>();
    const [tabIds, setTabIds] = useState<string[]>([]);
    const [draggingTab, setDraggingTab] = useState<string>();
    const [dropIndex, setDropIndex] = useState<number | null>(null);
    const [tabsLoaded, setTabsLoaded] = useState({});
    const [newTabId, setNewTabId] = useState<string | null>(null);

    const tabbarWrapperRef = useRef<HTMLDivElement>(null);
    const tabBarRef = useRef<HTMLDivElement>(null);
    const tabsWrapperRef = useRef<HTMLDivElement>(null);
    const tabRefs = useRef<React.RefObject<HTMLDivElement>[]>([]);
    const addBtnRef = useRef<HTMLButtonElement>(null);
    const dragTabIdRef = useRef<string | null>(null);
    const dropIndexRef = useRef<number | null>(null);
    const osInstanceRef = useRef<OverlayScrollbars>(null);
    const draggerLeftRef = useRef<HTMLDivElement>(null);
    const rightContainerRef = useRef<HTMLDivElement>(null);
    const workspaceSwitcherRef = useRef<HTMLDivElement>(null);
    const waveAIButtonRef = useRef<HTMLDivElement>(null);
    const appMenuButtonRef = useRef<HTMLDivElement>(null);
    // raw measured content width per tabId (accumulation buffer, flushed into tabWidths state)
    const measuredWidthsRef = useRef<Record<string, number>>({});
    // applied per-tab widths; kept in state so the wrapper width (and the +/right elements)
    // re-render and the layout effect re-runs with a fresh closure when measurements change
    const [tabWidths, setTabWidths] = useState<Record<string, number>>({});
    const scrollableRef = useRef<boolean>(false);
    const prevAllLoadedRef = useRef<boolean>(false);
    const activeTabId = useAtomValue(env.atoms.staticTabId);
    const isFullScreen = useAtomValue(env.atoms.isFullScreen);
    const zoomFactor = useAtomValue(env.atoms.zoomFactorAtom);
    const showMenuBar = useAtomValue(env.getSettingsKeyAtom("window:showmenubar"));
    const confirmClose = useAtomValue(env.getSettingsKeyAtom("tab:confirmclose")) ?? false;
    const hideAiButton = useAtomValue(env.getSettingsKeyAtom("app:hideaibutton"));
    const appUpdateStatus = useAtomValue(env.atoms.updaterStatusAtom);
    const documentHasFocus = useAtomValue(env.atoms.documentHasFocus);


    // Keep the refs array in sync with tabIds during render (not in an effect) so a tab that
    // was just added/moved in gets a real ref on its first render. Otherwise it renders with
    // ref={undefined}, setSizeAndPosition skips it, and it stays invisible (opacity:0) until
    // some later re-render happens to fix it.
    if (tabRefs.current.length !== tabIds.length) {
        tabRefs.current = tabIds.map((_, index) => tabRefs.current[index] || createRef());
    }

    useEffect(() => {
        if (!workspace) {
            return;
        }
        const newTabIdsArr = workspace.tabids ?? [];

        const areEqual = strArrayIsEqual(tabIds, newTabIdsArr);

        if (!areEqual) {
            setTabIds(newTabIdsArr);
        }
    }, [workspace, tabIds]);

    const getTabWidth = (tabId: string): number => {
        const measured = tabWidths[tabId] ?? TabDefaultWidth;
        return Math.max(TabMinWidth, Math.min(measured, TabMaxWidth));
    };

    // Cumulative left offset of the tab at `index` within the given ordering.
    const getTabOffset = (ids: string[], index: number): number => {
        let offset = 0;
        for (let i = 0; i < index; i++) {
            offset += getTabWidth(ids[i]);
        }
        return offset;
    };

    const getTotalTabsWidth = (ids: string[]): number => ids.reduce((sum, id) => sum + getTabWidth(id), 0);

    const setSizeAndPosition = (animate?: boolean) => {
        const tabBar = tabBarRef.current;
        if (tabBar === null) return;

        const getOuterWidth = (el: HTMLElement): number => {
            const rect = el.getBoundingClientRect();
            const style = getComputedStyle(el);
            return rect.width + parseFloat(style.marginLeft) + parseFloat(style.marginRight);
        };

        const tabbarWrapperWidth = tabbarWrapperRef.current.getBoundingClientRect().width;
        const windowDragLeftWidth = draggerLeftRef.current.getBoundingClientRect().width;
        const rightContainerWidth = rightContainerRef.current?.getBoundingClientRect().width ?? 0;
        const addBtnWidth = getOuterWidth(addBtnRef.current);
        const appMenuButtonWidth = appMenuButtonRef.current?.getBoundingClientRect().width ?? 0;
        const workspaceSwitcherWidth = workspaceSwitcherRef.current?.getBoundingClientRect().width ?? 0;
        const waveAIButtonWidth =
            !hideAiButton && waveAIButtonRef.current != null ? getOuterWidth(waveAIButtonRef.current) : 0;

        const nonTabElementsWidth =
            windowDragLeftWidth +
            rightContainerWidth +
            addBtnWidth +
            appMenuButtonWidth +
            workspaceSwitcherWidth +
            waveAIButtonWidth;
        const spaceForTabs = tabbarWrapperWidth - nonTabElementsWidth;

        // Each tab is sized to its own measured content width (clamped). Tabs keep their
        // content width rather than stretching to fill the bar; leftover space stays empty.
        let cumulativeLeft = 0;
        tabRefs.current.forEach((ref, index) => {
            if (ref.current) {
                if (animate) {
                    ref.current.classList.add("animate");
                } else {
                    ref.current.classList.remove("animate");
                }
                const width = getTabWidth(tabIds[index]);
                ref.current.style.width = `${width}px`;
                ref.current.style.transform = `translate3d(${cumulativeLeft}px,0,0)`;
                ref.current.style.opacity = "1";
                cumulativeLeft += width;
            }
        });

        const totalTabsWidth = cumulativeLeft;

        // Determine if the tab bar needs to be scrollable
        const newScrollable = totalTabsWidth > spaceForTabs;

        // Update the state with the new scrollable state if it has changed
        if (newScrollable !== scrollableRef.current) {
            scrollableRef.current = newScrollable;
        }

        // Initialize/destroy overlay scrollbars
        if (newScrollable) {
            osInstanceRef.current = OverlayScrollbars(tabBarRef.current, { ...(OSOptions as any) });
        } else {
            if (osInstanceRef.current) {
                osInstanceRef.current.destroy();
            }
        }
    };

    const handleResizeTabs = useCallback(() => {
        setSizeAndPosition();
    }, [tabIds, newTabId, isFullScreen]);

    // flush accumulated measurements into state (debounced to coalesce a batch of reports);
    // the resulting re-render updates the wrapper width and re-runs the layout effect
    const flushTabWidthsDebounced = useCallback(
        debounce(50, () => {
            setTabWidths({ ...measuredWidthsRef.current });
        }),
        []
    );

    const handleTabMeasure = useCallback(
        (tabId: string, width: number) => {
            if (measuredWidthsRef.current[tabId] === width) {
                return;
            }
            measuredWidthsRef.current[tabId] = width;
            flushTabWidthsDebounced();
        },
        [flushTabWidthsDebounced]
    );

    // reposition/resize tabs whenever applied widths change (fresh closure over current tabs)
    useEffect(() => {
        setSizeAndPosition();
    }, [tabWidths]);

    // update layout on reinit version
    const reinitVersion = useAtomValue(env.atoms.reinitVersion);
    useEffect(() => {
        if (reinitVersion > 0) {
            setSizeAndPosition();
        }
    }, [reinitVersion]);

    // re-layout when the window regains focus: a window that rendered in the background may have
    // measured/positioned its tabs before layout settled (the tab re-measure is keyed on focus too)
    useEffect(() => {
        if (documentHasFocus) {
            setSizeAndPosition();
        }
    }, [documentHasFocus]);

    // update layout on resize
    useEffect(() => {
        window.addEventListener("resize", handleResizeTabs);
        return () => {
            window.removeEventListener("resize", handleResizeTabs);
        };
    }, [handleResizeTabs]);

    // update layout on changed tabIds, tabsLoaded, newTabId, hideAiButton, appUpdateStatus, or zoomFactor
    useEffect(() => {
        // Check if all tabs are loaded
        const allLoaded = tabIds.length > 0 && tabIds.every((id) => tabsLoaded[id]);
        if (allLoaded) {
            setSizeAndPosition(false);
            if (!prevAllLoadedRef.current) {
                prevAllLoadedRef.current = true;
            }
        }
    }, [
        tabIds,
        tabsLoaded,
        newTabId,
        hideAiButton,
        appUpdateStatus,
        zoomFactor,
        showMenuBar,
    ]);

    const clearDragState = () => {
        dragTabIdRef.current = null;
        dropIndexRef.current = null;
        setDraggingTab(null);
        setDropIndex(null);
    };

    // HTML5 drag: the dragged tab follows the cursor as a native drag image (and the OS shows a
    // move/no-drop cursor based on dropEffect), which also works across windows. Reordering is
    // resolved on drop via a drop-line; cross-window moves are resolved on dragend.
    const handleTabDragStart = (event: React.DragEvent<HTMLDivElement>, tabId: string) => {
        dragTabIdRef.current = tabId;
        event.dataTransfer.effectAllowed = "move";
        event.dataTransfer.setData("text/plain", tabId);
        event.dataTransfer.setData(WaveTabDragType, tabId);
        // float a snapshot of the tab under the cursor while dragging (incl. across windows)
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
        const nextDropIndex = insertBefore ? index : index + 1;
        dropIndexRef.current = nextDropIndex;
        setDropIndex(nextDropIndex);
    };

    const reorderTabs = (targetIndex: number) => {
        const sourceTabId = dragTabIdRef.current;
        if (sourceTabId == null) {
            return;
        }
        const order = [...tabIds];
        const sourceIndex = order.indexOf(sourceTabId);
        if (sourceIndex === -1) {
            return;
        }
        const bounded = Math.max(0, Math.min(targetIndex, order.length));
        const adjusted = sourceIndex < bounded ? bounded - 1 : bounded;
        if (sourceIndex === adjusted) {
            return;
        }
        order.splice(sourceIndex, 1);
        order.splice(adjusted, 0, sourceTabId);
        fireAndForget(() => env.rpc.UpdateWorkspaceTabIdsCommand(TabRpcClient, workspace.oid, order));
    };

    // Drop zone for the empty area to the right of the tabs: dropping there targets the last
    // position. The per-tab handlers cover the tabs themselves; this only acts when the cursor
    // isn't over a tab so it doesn't override the per-tab insert position.
    const handleTabBarDragOver = (event: React.DragEvent<HTMLDivElement>) => {
        if (dragTabIdRef.current == null) {
            return;
        }
        if ((event.target as HTMLElement).closest(".tab") != null) {
            return;
        }
        event.preventDefault();
        event.dataTransfer.dropEffect = "move";
        dropIndexRef.current = tabIds.length;
        setDropIndex(tabIds.length);
    };

    const handleTabDrop = (event: React.DragEvent<HTMLDivElement>) => {
        event.preventDefault();
        if (dropIndexRef.current != null) {
            reorderTabs(dropIndexRef.current);
        }
        clearDragState();
    };

    const handleTabDragEnd = (event: React.DragEvent<HTMLDivElement>) => {
        const sourceTabId = dragTabIdRef.current;
        const screenX = event.screenX;
        const screenY = event.screenY;
        clearDragState();
        if (sourceTabId != null) {
            fireAndForget(() => moveTabToWorkspaceAtPoint(workspace.oid, sourceTabId, screenX, screenY));
        }
    };

    const handleSelectTab = (tabId: string) => {
        env.electron.setActiveTab(tabId);
    };

    const updateScrollDebounced = useCallback(
        debounce(30, () => {
            if (scrollableRef.current) {
                const { viewport } = osInstanceRef.current.elements();
                viewport.scrollLeft = getTotalTabsWidth(tabIds);
            }
        }),
        [tabIds]
    );

    const setNewTabIdDebounced = useCallback(
        debounce(100, (tabId: string) => {
            setNewTabId(tabId);
        }),
        []
    );

    const handleAddTab = () => {
        env.electron.createTab();
        tabsWrapperRef.current.style.setProperty("--tabs-wrapper-transition", "width 0.1s ease");

        updateScrollDebounced();

        setNewTabIdDebounced(null);
    };

    const handleCloseTab = (event: React.MouseEvent<HTMLButtonElement, MouseEvent> | null, tabId: string) => {
        event?.stopPropagation();
        env.electron
            .closeTab(workspace.oid, tabId, confirmClose)
            .then((didClose) => {
                if (didClose) {
                    tabsWrapperRef.current?.style.setProperty("--tabs-wrapper-transition", "width 0.3s ease");
                    deleteLayoutModelForTab(tabId);
                }
            })
            .catch((e) => {
                console.log("error closing tab", e);
            });
    };

    const handleTabLoaded = useCallback((tabId: string) => {
        setTabsLoaded((prev) => {
            if (!prev[tabId]) {
                // Only update if the tab isn't already marked as loaded
                return { ...prev, [tabId]: true };
            }
            return prev;
        });
    }, []);

    const activeTabIndex = tabIds.indexOf(activeTabId);

    function onEllipsisClick() {
        env.electron.showWorkspaceAppMenu(workspace.oid);
    }

    const tabsWrapperWidth = getTotalTabsWidth(tabIds);

    // Drop-line position while reordering. Hidden when the drop would be a no-op (immediately
    // before/after the dragged tab), since dropping there changes nothing.
    let dropLineLeft: number | null = null;
    if (!noTabs && draggingTab != null && dropIndex != null) {
        const sourceIndex = tabIds.indexOf(draggingTab);
        if (dropIndex !== sourceIndex && dropIndex !== sourceIndex + 1) {
            dropLineLeft = getTabOffset(tabIds, dropIndex);
        }
    }

    const showAppMenuButton = env.isWindows() || (!env.isMacOS() && !showMenuBar);

    // Calculate window drag left width based on platform and state
    let windowDragLeftWidth = 10;
    if (env.isMacOS() && !isFullScreen) {
        const trafficLightsWidth = isMacOSTahoeOrLater()
            ? MacOSTahoeTrafficLightsWidth
            : MacOSTrafficLightsWidth;
        if (zoomFactor > 0) {
            windowDragLeftWidth = trafficLightsWidth / zoomFactor;
        } else {
            windowDragLeftWidth = trafficLightsWidth;
        }
    }

    // Calculate window drag right width
    let windowDragRightWidth = 12;
    if (env.isWindows()) {
        if (zoomFactor > 0) {
            windowDragRightWidth = 139 / zoomFactor;
        } else {
            windowDragRightWidth = 139;
        }
    }

    return (
        <div ref={tabbarWrapperRef} className="tab-bar-wrapper">
            <div
                ref={draggerLeftRef}
                className="h-full shrink-0 z-window-drag"
                style={{ width: windowDragLeftWidth, WebkitAppRegion: "drag" } as any}
            />
            {showAppMenuButton && (
                <div
                    ref={appMenuButtonRef}
                    className="flex items-center justify-center pr-1.5 text-[26px] select-none cursor-pointer text-secondary hover:text-primary"
                    style={{ WebkitAppRegion: "no-drag" } as React.CSSProperties}
                    onClick={onEllipsisClick}
                >
                    <i className="fa fa-ellipsis" />
                </div>
            )}
            <WaveAIButton divRef={waveAIButtonRef} />
            <Tooltip
                content="Workspace Switcher"
                placement="bottom"
                hideOnClick
                divRef={workspaceSwitcherRef}
                divClassName="flex items-center"
            >
                <WorkspaceSwitcher />
            </Tooltip>
            <div
                className="tab-bar"
                ref={tabBarRef}
                data-overlayscrollbars-initialize
                onDragOver={handleTabBarDragOver}
                onDrop={handleTabDrop}
            >
                <div
                    className="tabs-wrapper"
                    ref={tabsWrapperRef}
                    style={{
                        width: noTabs ? 0 : tabsWrapperWidth,
                        ...(noTabs ? ({ WebkitAppRegion: "drag" } as React.CSSProperties) : {}),
                    }}
                >
                    {!noTabs &&
                        tabIds.map((tabId, index) => {
                            const isActive = activeTabId === tabId;
                            const showDivider = index !== 0 && !isActive && index !== activeTabIndex + 1;
                            return (
                                <Tab
                                    key={tabId}
                                    ref={tabRefs.current[index]}
                                    id={tabId}
                                    showDivider={showDivider}
                                    onSelect={() => handleSelectTab(tabId)}
                                    active={isActive}
                                    onDragStart={(event) => handleTabDragStart(event, tabId)}
                                    onDragOver={(event) => handleTabDragOver(event, index)}
                                    onDrop={handleTabDrop}
                                    onDragEnd={handleTabDragEnd}
                                    onClose={(event) => handleCloseTab(event, tabId)}
                                    onLoaded={() => handleTabLoaded(tabId)}
                                    isDragging={draggingTab === tabId}
                                    tabWidth={getTabWidth(tabId)}
                                    isNew={tabId === newTabId}
                                    onMeasure={(width) => handleTabMeasure(tabId, width)}
                                />
                            );
                        })}
                </div>
                {dropLineLeft != null && <div className="tab-drop-line" style={{ left: dropLineLeft }} />}
            </div>
            <button
                ref={addBtnRef}
                title="Add Tab"
                className={`flex h-[22px] px-2 mb-1 mx-1 items-center rounded-md box-border cursor-pointer hover:bg-hoverbg transition-colors text-[12px] text-secondary hover:text-primary${noTabs ? " invisible" : ""}`}
                style={{ WebkitAppRegion: "no-drag" } as React.CSSProperties}
                onClick={handleAddTab}
            >
                <i className="fa fa-solid fa-plus" />
            </button>
            <div className="flex-1" />
            <div ref={rightContainerRef} className="flex flex-row gap-1 items-end">
                <UpdateStatusBanner />
                <div
                    className="h-full shrink-0 z-window-drag"
                    style={{ width: windowDragRightWidth, WebkitAppRegion: "drag" } as any}
                />
            </div>
        </div>
    );
});

export { TabBar, WaveAIButton };

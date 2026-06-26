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
import { moveTabToWorkspaceAtPoint } from "./crosswindowmove";
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
    const [dragStartPositions, setDragStartPositions] = useState<number[]>([]);
    const [draggingTab, setDraggingTab] = useState<string>();
    const [tabsLoaded, setTabsLoaded] = useState({});
    const [newTabId, setNewTabId] = useState<string | null>(null);

    const tabbarWrapperRef = useRef<HTMLDivElement>(null);
    const tabBarRef = useRef<HTMLDivElement>(null);
    const tabsWrapperRef = useRef<HTMLDivElement>(null);
    const tabRefs = useRef<React.RefObject<HTMLDivElement>[]>([]);
    const addBtnRef = useRef<HTMLButtonElement>(null);
    const draggingRemovedRef = useRef(false);
    const draggingTabDataRef = useRef({
        tabId: "",
        ref: { current: null },
        tabStartX: 0,
        tabStartIndex: 0,
        tabIndex: 0,
        initialOffsetX: null,
        totalScrollOffset: null,
        dragged: false,
    });
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

    let prevDelta: number;
    let prevDragDirection: string;

    // Update refs when tabIds change
    useEffect(() => {
        tabRefs.current = tabIds.map((_, index) => tabRefs.current[index] || createRef());
    }, [tabIds]);

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

    const saveTabsPosition = useCallback(() => {
        const tabs = tabRefs.current;
        if (tabs === null) return;

        const newStartPositions: number[] = [];
        let cumulativeLeft = 0; // Start from the left edge

        tabRefs.current.forEach((ref) => {
            if (ref.current) {
                newStartPositions.push(cumulativeLeft);
                cumulativeLeft += ref.current.getBoundingClientRect().width; // Add each tab's actual width to the cumulative position
            }
        });

        setDragStartPositions(newStartPositions);
    }, []);

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

    const saveTabsPositionDebounced = useCallback(
        debounce(100, () => saveTabsPosition()),
        [saveTabsPosition]
    );

    const handleResizeTabs = useCallback(() => {
        setSizeAndPosition();
        saveTabsPositionDebounced();
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
        saveTabsPositionDebounced();
    }, [tabWidths]);

    // update layout on reinit version
    const reinitVersion = useAtomValue(env.atoms.reinitVersion);
    useEffect(() => {
        if (reinitVersion > 0) {
            setSizeAndPosition();
        }
    }, [reinitVersion]);

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
            saveTabsPosition();
            if (!prevAllLoadedRef.current) {
                prevAllLoadedRef.current = true;
            }
        }
    }, [
        tabIds,
        tabsLoaded,
        newTabId,
        saveTabsPosition,
        hideAiButton,
        appUpdateStatus,
        zoomFactor,
        showMenuBar,
    ]);

    const getDragDirection = (currentX: number) => {
        let dragDirection: string;
        if (currentX - prevDelta > 0) {
            dragDirection = "+";
        } else if (currentX - prevDelta === 0) {
            dragDirection = prevDragDirection;
        } else {
            dragDirection = "-";
        }
        prevDelta = currentX;
        prevDragDirection = dragDirection;
        return dragDirection;
    };

    const getNewTabIndex = (currentX: number, tabIndex: number, dragDirection: string) => {
        let newTabIndex = tabIndex;
        const draggedWidth = getTabWidth(tabIds[tabIndex]);
        if (dragDirection === "+") {
            // Dragging to the right: move past a neighbor once the dragged tab's right edge clears its midpoint
            for (let i = tabIndex + 1; i < tabIds.length; i++) {
                const otherMidpoint = dragStartPositions[i] + getTabWidth(tabIds[i]) / 2;
                if (currentX + draggedWidth > otherMidpoint) {
                    newTabIndex = i;
                }
            }
        } else {
            // Dragging to the left: move past a neighbor once the dragged tab's left edge clears its midpoint
            for (let i = tabIndex - 1; i >= 0; i--) {
                const otherMidpoint = dragStartPositions[i] + getTabWidth(tabIds[i]) / 2;
                if (currentX < otherMidpoint) {
                    newTabIndex = i;
                }
            }
        }
        return newTabIndex;
    };

    const handleMouseMove = (event: MouseEvent) => {
        const { tabId, ref, tabStartX } = draggingTabDataRef.current;

        let initialOffsetX = draggingTabDataRef.current.initialOffsetX;
        let totalScrollOffset = draggingTabDataRef.current.totalScrollOffset;
        if (initialOffsetX === null) {
            initialOffsetX = event.clientX - tabStartX;
            draggingTabDataRef.current.initialOffsetX = initialOffsetX;
        }
        let currentX = event.clientX - initialOffsetX - totalScrollOffset;
        let tabBarRectWidth = tabBarRef.current.getBoundingClientRect().width;
        // for macos, it's offset to make space for the window buttons
        const tabBarRectLeftOffset = tabBarRef.current.getBoundingClientRect().left;
        const incrementDecrement = tabBarRectLeftOffset * 0.05;
        const dragDirection = getDragDirection(currentX);
        const scrollable = scrollableRef.current;
        const tabWidth = getTabWidth(tabId);

        // Scroll the tab bar if the dragged tab overflows the container bounds
        if (scrollable) {
            const { viewport } = osInstanceRef.current.elements();
            const currentScrollLeft = viewport.scrollLeft;

            if (event.clientX <= tabBarRectLeftOffset) {
                viewport.scrollLeft = Math.max(0, currentScrollLeft - incrementDecrement); // Scroll left
                if (viewport.scrollLeft !== currentScrollLeft) {
                    // Only adjust if the scroll actually changed
                    draggingTabDataRef.current.totalScrollOffset += currentScrollLeft - viewport.scrollLeft;
                }
            } else if (event.clientX >= tabBarRectWidth + tabBarRectLeftOffset) {
                viewport.scrollLeft = Math.min(viewport.scrollWidth, currentScrollLeft + incrementDecrement); // Scroll right
                if (viewport.scrollLeft !== currentScrollLeft) {
                    // Only adjust if the scroll actually changed
                    draggingTabDataRef.current.totalScrollOffset -= viewport.scrollLeft - currentScrollLeft;
                }
            }
        }

        // Re-calculate currentX after potential scroll adjustment
        initialOffsetX = draggingTabDataRef.current.initialOffsetX;
        totalScrollOffset = draggingTabDataRef.current.totalScrollOffset;
        currentX = event.clientX - initialOffsetX - totalScrollOffset;

        setDraggingTab((prev) => (prev !== tabId ? tabId : prev));

        // Check if the tab has moved 5 pixels
        if (Math.abs(currentX - tabStartX) >= 50) {
            draggingTabDataRef.current.dragged = true;
        }

        // Constrain movement within the container bounds
        if (tabBarRef.current) {
            const totalTabsWidth = getTotalTabsWidth(tabIds);
            if (totalTabsWidth < tabBarRectWidth) {
                // Set to the total tabs width if there's vacant space
                tabBarRectWidth = totalTabsWidth;
            } else if (scrollable) {
                // Set to the scrollable width if the tab bar is scrollable
                tabBarRectWidth = tabsWrapperRef.current.scrollWidth;
            }

            const minLeft = 0;
            const maxRight = tabBarRectWidth - tabWidth;

            // Adjust currentX to stay within bounds
            currentX = Math.min(Math.max(currentX, minLeft), maxRight);
        }

        ref.current!.style.transform = `translate3d(${currentX}px,0,0)`;
        ref.current!.style.zIndex = "100";

        const tabIndex = draggingTabDataRef.current.tabIndex;
        const newTabIndex = getNewTabIndex(currentX, tabIndex, dragDirection);

        if (newTabIndex !== tabIndex) {
            // Remove the dragged tab if not already done
            if (!draggingRemovedRef.current) {
                tabIds.splice(tabIndex, 1);
                draggingRemovedRef.current = true;
            }

            // Find current index of the dragged tab in tempTabs
            const currentIndexOfDraggingTab = tabIds.indexOf(tabId);

            // Move the dragged tab to its new position
            if (currentIndexOfDraggingTab !== -1) {
                tabIds.splice(currentIndexOfDraggingTab, 1);
            }
            tabIds.splice(newTabIndex, 0, tabId);

            // Update visual positions of the tabs using cumulative offsets (widths vary per tab)
            tabIds.forEach((localTabId, index) => {
                const ref = tabRefs.current.find((ref) => ref.current.dataset.tabId === localTabId);
                if (ref.current && localTabId !== tabId) {
                    ref.current.style.transform = `translate3d(${getTabOffset(tabIds, index)}px,0,0)`;
                    ref.current.classList.add("animate");
                }
            });

            draggingTabDataRef.current.tabIndex = newTabIndex;
        }
    };

    const setUpdatedTabsDebounced = useCallback(
        debounce(300, (tabIds: string[]) => {
            // Reset styles
            tabRefs.current.forEach((ref) => {
                ref.current.style.zIndex = "0";
                ref.current.classList.remove("animate");
            });
            // Reset dragging state
            setDraggingTab(null);
            // Update workspace tab ids
            fireAndForget(() => env.rpc.UpdateWorkspaceTabIdsCommand(TabRpcClient, workspace.oid, tabIds));
        }),
        []
    );

    const handleMouseUp = (event: MouseEvent) => {
        const { tabIndex, dragged, tabId } = draggingTabDataRef.current;

        document.removeEventListener("mouseup", handleMouseUp);
        document.removeEventListener("mousemove", handleMouseMove);
        draggingRemovedRef.current = false;

        const resetStyles = () => {
            tabRefs.current.forEach((ref) => {
                if (ref.current) {
                    ref.current.style.zIndex = "0";
                    ref.current.classList.remove("animate");
                }
            });
            setDraggingTab(null);
        };

        if (!dragged) {
            resetStyles();
            return;
        }

        // If the tab was released over a different Wave window, move it there instead of
        // reordering in place. The release point is in screen coordinates, so it resolves
        // even when the cursor ended up over another window.
        const screenX = event.screenX;
        const screenY = event.screenY;
        fireAndForget(async () => {
            const moved = await moveTabToWorkspaceAtPoint(workspace.oid, tabId, screenX, screenY);
            if (moved) {
                resetStyles();
                return;
            }
            const draggingTab = tabIds[tabIndex];
            const finalLeftPosition = getTabOffset(tabIds, tabIndex);
            const ref = tabRefs.current.find((r) => r.current.dataset.tabId === draggingTab);
            if (ref?.current) {
                ref.current.classList.add("animate");
                ref.current.style.transform = `translate3d(${finalLeftPosition}px,0,0)`;
            }
            setUpdatedTabsDebounced(tabIds);
        });
    };

    const handleDragStart = useCallback(
        (event: React.MouseEvent<HTMLDivElement, MouseEvent>, tabId: string, ref: React.RefObject<HTMLDivElement>) => {
            if (event.button !== 0) return;

            const tabIndex = tabIds.indexOf(tabId);
            const tabStartX = dragStartPositions[tabIndex]; // Starting X position of the tab

            console.log("handleDragStart", tabId, tabIndex, tabStartX);
            if (ref.current) {
                draggingTabDataRef.current = {
                    tabId: ref.current.dataset.tabId,
                    ref,
                    tabStartX,
                    tabIndex,
                    tabStartIndex: tabIndex,
                    initialOffsetX: null,
                    totalScrollOffset: 0,
                    dragged: false,
                };

                document.addEventListener("mousemove", handleMouseMove);
                document.addEventListener("mouseup", handleMouseUp);
            }
        },
        [tabIds, dragStartPositions]
    );

    const handleSelectTab = (tabId: string) => {
        if (!draggingTabDataRef.current.dragged) {
            env.electron.setActiveTab(tabId);
        }
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
            <div className="tab-bar" ref={tabBarRef} data-overlayscrollbars-initialize>
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
                                    onDragStart={(event) => handleDragStart(event, tabId, tabRefs.current[index])}
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

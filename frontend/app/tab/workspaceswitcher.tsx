// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { SettingsKeyAtomFnType, useWaveEnv, WaveEnv, WaveEnvSubset } from "@/app/waveenv/waveenv";
import {
    ExpandableMenu,
    ExpandableMenuItem,
    ExpandableMenuItemGroup,
    ExpandableMenuItemGroupTitle,
    ExpandableMenuItemLeftElement,
    ExpandableMenuItemRightElement,
} from "@/element/expandablemenu";
import { Popover, PopoverButton, PopoverContent } from "@/element/popover";
import { fireAndForget, makeIconClass, useAtomValueSafe } from "@/util/util";
import clsx from "clsx";
import { atom, PrimitiveAtom, useAtom, useAtomValue, useSetAtom } from "jotai";
import { splitAtom } from "jotai/utils";
import { OverlayScrollbarsComponent } from "overlayscrollbars-react";
import { CSSProperties, forwardRef, useCallback, useEffect, useMemo, useRef, useState } from "react";
import WorkspaceSVG from "../asset/workspace.svg";
import { IconButton } from "../element/iconbutton";
import { globalStore } from "@/app/store/jotaiStore";
import { makeORef } from "../store/wos";
import { waveEventSubscribeSingle } from "../store/wps";
import { WorkspaceEditor } from "./workspaceeditor";
import "./workspaceswitcher.scss";

export type WorkspaceSwitcherEnv = WaveEnvSubset<{
    electron: {
        deleteWorkspace: WaveEnv["electron"]["deleteWorkspace"];
        createWorkspace: WaveEnv["electron"]["createWorkspace"];
        switchWorkspace: WaveEnv["electron"]["switchWorkspace"];
        openWorkspaceInNewWindow: WaveEnv["electron"]["openWorkspaceInNewWindow"];
    };
    atoms: {
        workspace: WaveEnv["atoms"]["workspace"];
    };
    services: {
        workspace: WaveEnv["services"]["workspace"];
    };
    wos: WaveEnv["wos"];
    getSettingsKeyAtom: SettingsKeyAtomFnType<"app:showworkspacename">;
}>;

type WorkspaceListEntry = {
    windowId: string;
    workspace: Workspace;
};

type WorkspaceList = WorkspaceListEntry[];
const workspaceMapAtom = atom<WorkspaceList>([]);
const workspaceSplitAtom = splitAtom(workspaceMapAtom);
const editingWorkspaceAtom = atom<string>();
const workspaceSwitcherOpenAtom: PrimitiveAtom<boolean> = atom(false);
const WorkspaceSwitcher = forwardRef<HTMLDivElement>((_, ref) => {
    const env = useWaveEnv<WorkspaceSwitcherEnv>();
    const setWorkspaceList = useSetAtom(workspaceMapAtom);
    const activeWorkspace = useAtomValueSafe(env.atoms.workspace);
    const workspaceList = useAtomValue(workspaceSplitAtom);
    const rawWorkspaceList = useAtomValue(workspaceMapAtom);
    const setEditingWorkspace = useSetAtom(editingWorkspaceAtom);
    const [isOpen, setIsOpen] = useAtom(workspaceSwitcherOpenAtom);
    const [searchText, setSearchText] = useState("");
    const [selectedIndex, setSelectedIndex] = useState(0);
    const searchInputRef = useRef<HTMLInputElement>(null);
    const initialSelectionSetRef = useRef(false);
    const showWorkspaceName = useAtomValue(env.getSettingsKeyAtom("app:showworkspacename"));

    const updateWorkspaceList = useCallback(async () => {
        const workspaceList = await env.services.workspace.ListWorkspaces();
        if (!workspaceList) {
            return;
        }
        const newList: WorkspaceList = [];
        for (const entry of workspaceList) {
            // This just ensures that the atom exists for easier setting of the object
            globalStore.get(env.wos.getWaveObjectAtom(makeORef("workspace", entry.workspaceid)));
            newList.push({
                windowId: entry.windowid,
                workspace: await env.services.workspace.GetWorkspace(entry.workspaceid),
            });
        }
        setWorkspaceList(newList);
    }, []);

    useEffect(
        () =>
            waveEventSubscribeSingle({
                eventType: "workspace:update",
                handler: () => fireAndForget(updateWorkspaceList),
            }),
        []
    );

    useEffect(() => {
        fireAndForget(updateWorkspaceList);
    }, []);

    useEffect(() => {
        if (!isOpen) {
            initialSelectionSetRef.current = false;
            return;
        }
        setSearchText("");
        setSelectedIndex(0);
        setEditingWorkspace(null);
        fireAndForget(updateWorkspaceList);
        requestAnimationFrame(() => searchInputRef.current?.focus());
    }, [isOpen]);

    const onDeleteWorkspace = useCallback((workspaceId: string) => {
        env.electron.deleteWorkspace(workspaceId);
    }, []);

    const filteredWorkspaces = useMemo(() => {
        const entries = workspaceList.map((entryAtom, i) => ({
            entryAtom,
            workspace: rawWorkspaceList[i]?.workspace,
            windowId: rawWorkspaceList[i]?.windowId,
        }));
        const query = searchText.trim().toLowerCase();
        if (!query) {
            return entries;
        }
        return entries.filter(({ workspace }) => workspace?.name?.toLowerCase().includes(query));
    }, [workspaceList, rawWorkspaceList, searchText]);

    // On open, once the list has loaded, preselect the current (source) workspace instead of the
    // first entry. Only done once per open so it doesn't override keyboard navigation afterwards.
    useEffect(() => {
        if (!isOpen || initialSelectionSetRef.current || searchText || filteredWorkspaces.length === 0) {
            return;
        }
        const idx = filteredWorkspaces.findIndex(({ workspace }) => workspace?.oid === activeWorkspace?.oid);
        setSelectedIndex(idx >= 0 ? idx : 0);
        initialSelectionSetRef.current = true;
    }, [isOpen, filteredWorkspaces, searchText, activeWorkspace]);

    const switchToWorkspace = useCallback((workspace: Workspace) => {
        if (!workspace) {
            return;
        }
        env.electron.switchWorkspace(workspace.oid);
        setIsOpen(false);
    }, []);

    // Shift+Enter: open the workspace in a new window if it isn't open yet; otherwise focus its window.
    const openWorkspaceInNewWindow = useCallback((entry: { workspace: Workspace; windowId: string }) => {
        if (!entry?.workspace) {
            return;
        }
        if (entry.windowId) {
            env.electron.switchWorkspace(entry.workspace.oid);
        } else {
            env.electron.openWorkspaceInNewWindow(entry.workspace.oid);
        }
        setIsOpen(false);
    }, []);

    useEffect(() => {
        if (!isOpen) {
            return;
        }
        const selectedEl = document.querySelector(".workspace-switcher-content .keyboard-selected");
        selectedEl?.scrollIntoView({ block: "nearest" });
    }, [selectedIndex, isOpen]);

    const handleSearchKeyDown = (e: React.KeyboardEvent) => {
        if (e.key === "ArrowDown") {
            e.preventDefault();
            e.stopPropagation();
            setSelectedIndex((prev) => Math.min(prev + 1, filteredWorkspaces.length - 1));
        } else if (e.key === "ArrowUp") {
            e.preventDefault();
            e.stopPropagation();
            setSelectedIndex((prev) => Math.max(prev - 1, 0));
        } else if (e.key === "Enter") {
            e.preventDefault();
            e.stopPropagation();
            const entry = filteredWorkspaces[selectedIndex];
            if (e.shiftKey) {
                openWorkspaceInNewWindow(entry);
            } else {
                switchToWorkspace(entry?.workspace);
            }
        } else if (e.key === "Escape") {
            e.preventDefault();
            e.stopPropagation();
            setIsOpen(false);
        }
    };

    const isActiveWorkspaceSaved = !!(activeWorkspace.name && activeWorkspace.icon);

    const workspaceIcon = isActiveWorkspaceSaved ? (
        <i className={makeIconClass(activeWorkspace.icon, false)} style={{ color: activeWorkspace.color }}></i>
    ) : (
        <WorkspaceSVG />
    );

    const saveWorkspace = () => {
        fireAndForget(async () => {
            await env.services.workspace.UpdateWorkspace(activeWorkspace.oid, "", "", "", true);
            await updateWorkspaceList();
            setEditingWorkspace(activeWorkspace.oid);
        });
    };

    return (
        <Popover
            className="workspace-switcher-popover"
            placement="bottom-start"
            isOpen={isOpen}
            onOpenChange={setIsOpen}
            onDismiss={() => setEditingWorkspace(null)}
            ref={ref}
        >
            <PopoverButton
                className="workspace-switcher-button grey"
                as="div"
                onClick={() => {
                    fireAndForget(updateWorkspaceList);
                }}
            >
                <span className="workspace-icon">{workspaceIcon}</span>
                {showWorkspaceName && activeWorkspace?.name ? (
                    <span className="workspace-name">{activeWorkspace.name}</span>
                ) : null}
            </PopoverButton>
            <PopoverContent className="workspace-switcher-content">
                <div className="title">{isActiveWorkspaceSaved ? "Switch workspace" : "Open workspace"}</div>
                <input
                    ref={searchInputRef}
                    className="workspace-search"
                    type="text"
                    placeholder="Search workspaces..."
                    value={searchText}
                    onChange={(e) => {
                        setSearchText(e.target.value);
                        setSelectedIndex(0);
                    }}
                    onKeyDown={handleSearchKeyDown}
                />
                <OverlayScrollbarsComponent className={"scrollable"} options={{ scrollbars: { autoHide: "leave" } }}>
                    <ExpandableMenu noIndent singleOpen>
                        {filteredWorkspaces.map(({ entryAtom, workspace }, i) => (
                            <WorkspaceSwitcherItem
                                key={workspace?.oid ?? i}
                                entryAtom={entryAtom}
                                isSelected={i === selectedIndex}
                                onHover={() => setSelectedIndex(i)}
                                onSwitchWorkspace={switchToWorkspace}
                                onDeleteWorkspace={onDeleteWorkspace}
                            />
                        ))}
                    </ExpandableMenu>
                </OverlayScrollbarsComponent>

                <div className="actions">
                    {isActiveWorkspaceSaved ? (
                        <ExpandableMenuItem onClick={() => env.electron.createWorkspace()}>
                            <ExpandableMenuItemLeftElement>
                                <i className="fa-sharp fa-solid fa-plus"></i>
                            </ExpandableMenuItemLeftElement>
                            <div className="content">Create new workspace</div>
                        </ExpandableMenuItem>
                    ) : (
                        <ExpandableMenuItem onClick={() => saveWorkspace()}>
                            <ExpandableMenuItemLeftElement>
                                <i className="fa-sharp fa-solid fa-floppy-disk"></i>
                            </ExpandableMenuItemLeftElement>
                            <div className="content">Save workspace</div>
                        </ExpandableMenuItem>
                    )}
                </div>
            </PopoverContent>
        </Popover>
    );
});

const WorkspaceSwitcherItem = ({
    entryAtom,
    isSelected,
    onHover,
    onSwitchWorkspace,
    onDeleteWorkspace,
}: {
    entryAtom: PrimitiveAtom<WorkspaceListEntry>;
    isSelected: boolean;
    onHover: () => void;
    onSwitchWorkspace: (workspace: Workspace) => void;
    onDeleteWorkspace: (workspaceId: string) => void;
}) => {
    const env = useWaveEnv<WorkspaceSwitcherEnv>();
    const activeWorkspace = useAtomValueSafe(env.atoms.workspace);
    const [workspaceEntry, setWorkspaceEntry] = useAtom(entryAtom);
    const [editingWorkspace, setEditingWorkspace] = useAtom(editingWorkspaceAtom);

    const workspace = workspaceEntry.workspace;
    const isCurrentWorkspace = activeWorkspace.oid === workspace.oid;

    const setWorkspace = useCallback((newWorkspace: Workspace) => {
        setWorkspaceEntry({ ...workspaceEntry, workspace: newWorkspace });
        if (newWorkspace.name != "") {
            fireAndForget(() =>
                env.services.workspace.UpdateWorkspace(
                    workspace.oid,
                    newWorkspace.name,
                    newWorkspace.icon,
                    newWorkspace.color,
                    false
                )
            );
        }
    }, []);

    const isActive = !!workspaceEntry.windowId;
    const editIconDecl: IconButtonDecl = {
        elemtype: "iconbutton",
        className: "edit",
        icon: "pencil",
        title: "Edit workspace",
        click: (e) => {
            e.stopPropagation();
            if (editingWorkspace === workspace.oid) {
                setEditingWorkspace(null);
            } else {
                setEditingWorkspace(workspace.oid);
            }
        },
    };
    const windowIconDecl: IconButtonDecl = {
        elemtype: "iconbutton",
        className: "window",
        noAction: true,
        icon: isCurrentWorkspace ? "check" : "window",
        title: isCurrentWorkspace ? "This is your current workspace" : "This workspace is open",
    };

    const isEditing = editingWorkspace === workspace.oid;

    return (
        <ExpandableMenuItemGroup
            key={workspace.oid}
            isOpen={isEditing}
            className={clsx({ "is-current": isCurrentWorkspace, "keyboard-selected": isSelected })}
        >
            <ExpandableMenuItemGroupTitle onClick={() => onSwitchWorkspace(workspace)}>
                <div
                    className="menu-group-title-wrapper"
                    onMouseEnter={onHover}
                    style={
                        {
                            "--workspace-color": workspace.color,
                        } as CSSProperties
                    }
                >
                    <ExpandableMenuItemLeftElement>
                        <i
                            className={clsx("left-icon", makeIconClass(workspace.icon, true))}
                            style={{ color: workspace.color }}
                        />
                    </ExpandableMenuItemLeftElement>
                    <div className="label">{workspace.name}</div>
                    <ExpandableMenuItemRightElement>
                        <div className="icons">
                            <IconButton decl={editIconDecl} />
                            {isActive && <IconButton decl={windowIconDecl} />}
                        </div>
                    </ExpandableMenuItemRightElement>
                </div>
            </ExpandableMenuItemGroupTitle>
            <ExpandableMenuItem>
                <WorkspaceEditor
                    title={workspace.name}
                    icon={workspace.icon}
                    color={workspace.color}
                    focusInput={isEditing}
                    onTitleChange={(title) => setWorkspace({ ...workspace, name: title })}
                    onColorChange={(color) => setWorkspace({ ...workspace, color })}
                    onIconChange={(icon) => setWorkspace({ ...workspace, icon })}
                    onDeleteWorkspace={() => onDeleteWorkspace(workspace.oid)}
                />
            </ExpandableMenuItem>
        </ExpandableMenuItemGroup>
    );
};

export { WorkspaceSwitcher, workspaceSwitcherOpenAtom };

// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { atoms, getApi } from "@/app/store/global";
import { RpcApi } from "@/app/store/wshclientapi";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import { fireAndForget } from "@/util/util";
import { useAtomValue } from "jotai";
import { useEffect } from "react";

// A pending cross-window drag is only honored briefly after the last drag movement. The
// source stops refreshing the timestamp the moment the cursor leaves its window, so this only
// needs to cover the gap from the source window's edge to the release on this window. Keeping
// it tight means a stale handoff (a drop released over the desktop, which never clears) can't
// silently move a tab on an unrelated click a moment later.
const PendingDragMaxAgeMs = 1500;

// TabDropHandler completes a cross-window tab drag: when a tab dragged out of another
// workspace window is released over this window, the source arms a pending drag in the main
// process and this handler (in the window under the cursor at mouseup) performs the move.
export const TabDropHandler = () => {
    const workspace = useAtomValue(atoms.workspace);
    const workspaceId = workspace?.oid;

    useEffect(() => {
        if (!workspaceId) {
            return;
        }
        const handleMouseUp = (event: MouseEvent) => {
            if (event.button !== 0) {
                return;
            }
            fireAndForget(async () => {
                const pending = await getApi().getPendingTabDrag();
                if (pending == null) {
                    return;
                }
                if (pending.sourceworkspaceid === workspaceId) {
                    return;
                }
                if (Date.now() - pending.ts > PendingDragMaxAgeMs) {
                    return;
                }
                getApi().clearPendingTabDrag();
                await RpcApi.MoveTabCommand(TabRpcClient, {
                    sourceworkspaceid: pending.sourceworkspaceid,
                    destworkspaceid: workspaceId,
                    tabid: pending.tabid,
                });
            });
        };
        document.addEventListener("mouseup", handleMouseUp);
        return () => document.removeEventListener("mouseup", handleMouseUp);
    }, [workspaceId]);

    return null;
};

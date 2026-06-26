// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { getApi } from "@/app/store/global";
import { RpcApi } from "@/app/store/wshclientapi";
import { TabRpcClient } from "@/app/store/wshrpcutil";

// Completes a cross-window tab drag from the source side. Given the screen point where the
// drag ended, it finds the Wave window under the cursor and, if it is a different workspace,
// moves the tab there. Returns true if a cross-window move was issued. Used by both tab bars
// so they share one mechanism regardless of how each detects its drag end (mouseup vs dragend).
export async function moveTabToWorkspaceAtPoint(
    sourceWorkspaceId: string,
    tabId: string,
    screenX: number,
    screenY: number
): Promise<boolean> {
    const targetWorkspaceId = await getApi().getWorkspaceAtPoint(screenX, screenY);
    if (!targetWorkspaceId || targetWorkspaceId === sourceWorkspaceId) {
        return false;
    }
    await RpcApi.MoveTabCommand(TabRpcClient, {
        sourceworkspaceid: sourceWorkspaceId,
        destworkspaceid: targetWorkspaceId,
        tabid: tabId,
    });
    return true;
}

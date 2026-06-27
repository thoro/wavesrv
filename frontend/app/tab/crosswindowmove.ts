// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { getApi } from "@/app/store/global";
import { RpcApi } from "@/app/store/wshclientapi";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import type { DragEvent } from "react";

// Custom dataTransfer type stamped on a tab drag so any window can recognize it (e.g. to show
// the move cursor while a tab is dragged over it).
export const WaveTabDragType = "application/x-wave-tab";

// Sets the drag ghost to a snapshot of the tab. Chromium renders a BLANK drag image when the
// element handed to setDragImage has a CSS transform — the top tab bar positions each tab via an
// inline translate3d(), which triggers exactly that. So snapshot a transform-free clone rendered
// in place (same on-screen rect → no visible flash) and remove it once the bitmap is captured.
export function setTabDragImage(event: DragEvent<HTMLDivElement>) {
    const source = event.currentTarget;
    const rect = source.getBoundingClientRect();
    const clone = source.cloneNode(true) as HTMLElement;
    clone.style.transform = "none";
    clone.style.opacity = "1";
    clone.style.position = "fixed";
    clone.style.left = `${rect.left}px`;
    clone.style.top = `${rect.top}px`;
    clone.style.width = `${rect.width}px`;
    clone.style.height = `${rect.height}px`;
    clone.style.margin = "0";
    clone.style.pointerEvents = "none";
    clone.style.zIndex = "9999";
    document.body.appendChild(clone);
    event.dataTransfer.setDragImage(clone, event.nativeEvent.offsetX, event.nativeEvent.offsetY);
    // the bitmap is captured right after dragstart returns; drop the clone on the next frame
    requestAnimationFrame(() => clone.remove());
}

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
    const targetWorkspaceId = await getApi().getWorkspaceAtPoint(screenX, screenY, sourceWorkspaceId);
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

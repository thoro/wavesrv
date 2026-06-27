// Copyright 2025, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { App } from "@/app/app";
import { ChromeApp, chromeLoadingAtom, chromeWaveAIOpenAtom } from "@/app/chrome/chromeapp";
import { workspaceSwitcherOpenAtom } from "@/app/tab/workspaceswitcher";
import { loadMonaco } from "@/app/monaco/monaco-env";
import { WorkspaceLayoutModel } from "@/app/workspace/workspace-layout-model";
import { loadBadges } from "@/app/store/badge";
import { GlobalModel } from "@/app/store/global-model";
import {
    globalRefocus,
    registerBuilderGlobalKeys,
    registerControlShiftStateUpdateHandler,
    registerElectronReinjectKeyHandler,
    registerGlobalKeys,
} from "@/app/store/keymodel";
import { modalsModel } from "@/app/store/modalmodel";
import { RpcApi } from "@/app/store/wshclientapi";
import { makeBuilderRouteId, makeTabRouteId, makeWindowRouteId } from "@/app/store/wshrouter";
import { initWshrpc, TabRpcClient } from "@/app/store/wshrpcutil";
import { BuilderApp } from "@/builder/builder-app";
import { getLayoutModelForStaticTab } from "@/layout/index";
import { countersClear, countersPrint } from "@/store/counters";
import {
    atoms,
    getApi,
    globalStore,
    initGlobal,
    initGlobalWaveEventSubs,
    loadConnStatus,
    subscribeToConnEvents,
} from "@/store/global";
import { activeTabIdAtom } from "@/store/tab-model";
import * as WOS from "@/store/wos";
import { loadFonts } from "@/util/fontutil";
import { setKeyUtilPlatform } from "@/util/keyutil";
import { isMacOS, setMacOSVersion } from "@/util/platformutil";
import { createElement } from "react";
import { createRoot } from "react-dom/client";

const platform = getApi().getPlatform();
document.title = `Wave Terminal`;
let savedInitOpts: WaveInitOpts = null;

(window as any).WOS = WOS;
(window as any).globalStore = globalStore;
(window as any).globalAtoms = atoms;
(window as any).RpcApi = RpcApi;
(window as any).isFullScreen = false;
(window as any).countersPrint = countersPrint;
(window as any).countersClear = countersClear;
(window as any).getLayoutModelForStaticTab = getLayoutModelForStaticTab;
(window as any).modalsModel = modalsModel;

function updateZoomFactor(zoomFactor: number) {
    console.log("update zoomfactor", zoomFactor);
    document.documentElement.style.setProperty("--zoomfactor", String(zoomFactor));
    document.documentElement.style.setProperty("--zoomfactor-inv", String(1 / zoomFactor));
}

async function initBare() {
    getApi().sendLog("Init Bare");
    document.body.style.visibility = "hidden";
    document.body.style.opacity = "0";
    document.body.classList.add("is-transparent");
    getApi().onWaveInit(initWaveWrap);
    getApi().onBuilderInit(initBuilderWrap);
    getApi().onChromeInit(initChromeWrap);
    setKeyUtilPlatform(platform);
    loadFonts();
    updateZoomFactor(getApi().getZoomFactor());
    getApi().onZoomFactorChange((zoomFactor) => {
        updateZoomFactor(zoomFactor);
    });
    document.fonts.ready.then(() => {
        console.log("Init Bare Done");
        getApi().setWindowInitStatus("ready");
    });
}

document.addEventListener("DOMContentLoaded", initBare);

async function initWaveWrap(initOpts: WaveInitOpts) {
    try {
        if (savedInitOpts) {
            await reinitWave();
            return;
        }
        savedInitOpts = initOpts;
        await initWave(initOpts);
    } catch (e) {
        getApi().sendLog("Error in initWave " + e.message + "\n" + e.stack);
        console.error("Error in initWave", e);
    } finally {
        document.body.style.visibility = null;
        document.body.style.opacity = null;
        document.body.classList.remove("is-transparent");
    }
}

async function reinitWave() {
    console.log("Reinit Wave");
    getApi().sendLog("Reinit Wave");

    // We use this hack to prevent a flicker of the previously-hovered tab when this view was last active.
    document.body.classList.add("nohover");
    requestAnimationFrame(() =>
        setTimeout(() => {
            document.body.classList.remove("nohover");
        }, 100)
    );

    await WOS.reloadWaveObject<Client>(WOS.makeORef("client", savedInitOpts.clientId));
    const waveWindow = await WOS.reloadWaveObject<WaveWindow>(WOS.makeORef("window", savedInitOpts.windowId));
    const ws = await WOS.reloadWaveObject<Workspace>(WOS.makeORef("workspace", waveWindow.workspaceid));
    const initialTab = await WOS.reloadWaveObject<Tab>(WOS.makeORef("tab", savedInitOpts.tabId));
    await WOS.reloadWaveObject<LayoutState>(WOS.makeORef("layout", initialTab.layoutstate));
    reloadAllWorkspaceTabs(ws);
    document.title = `Wave Terminal - ${initialTab.name}`; // TODO update with tab name change
    getApi().setWindowInitStatus("wave-ready");
    globalStore.set(atoms.reinitVersion, globalStore.get(atoms.reinitVersion) + 1);
    globalStore.set(atoms.updaterStatusAtom, getApi().getUpdaterStatus());
    setTimeout(() => {
        globalRefocus();
    }, 50);
}

function reloadAllWorkspaceTabs(ws: Workspace) {
    if (ws == null || !ws.tabids?.length) {
        return;
    }
    ws.tabids?.forEach((tabid) => {
        WOS.reloadWaveObject<Tab>(WOS.makeORef("tab", tabid));
    });
}

function loadAllWorkspaceTabs(ws: Workspace) {
    if (ws == null || !ws.tabids?.length) {
        return;
    }
    ws.tabids?.forEach((tabid) => {
        WOS.getObjectValue<Tab>(WOS.makeORef("tab", tabid));
    });
}

async function initWave(initOpts: WaveInitOpts) {
    const perfT0 = performance.now();
    const perfMark = (label: string) => console.log(`[perf] initWave ${label}: +${(performance.now() - perfT0).toFixed(0)}ms`);
    getApi().sendLog("Init Wave " + JSON.stringify(initOpts));
    const globalInitOpts: GlobalInitOptions = {
        tabId: initOpts.tabId,
        clientId: initOpts.clientId,
        windowId: initOpts.windowId,
        platform,
        environment: "renderer",
        primaryTabStartup: initOpts.primaryTabStartup,
        separateChrome: initOpts.separateChrome,
    };
    console.log("Wave Init", globalInitOpts);
    globalStore.set(activeTabIdAtom, initOpts.tabId);
    await GlobalModel.getInstance().initialize(globalInitOpts);
    initGlobal(globalInitOpts);
    (window as any).globalAtoms = atoms;
    perfMark("global initialized");

    // Init WPS event handlers
    const globalWS = initWshrpc(makeTabRouteId(initOpts.tabId));
    (window as any).globalWS = globalWS;
    (window as any).TabRpcClient = TabRpcClient;

    // ensures client/window/workspace are loaded into the cache before rendering
    try {
        await loadConnStatus();
        perfMark("connStatus loaded");
        await loadBadges();
        perfMark("badges loaded");
        initGlobalWaveEventSubs(initOpts);
        subscribeToConnEvents();
        if (isMacOS()) {
            const macOSVersion = await RpcApi.MacOSVersionCommand(TabRpcClient);
            setMacOSVersion(macOSVersion);
            perfMark("macOSVersion loaded");
        }
        const [_client, waveWindow, initialTab] = await Promise.all([
            WOS.loadAndPinWaveObject<Client>(WOS.makeORef("client", initOpts.clientId)),
            WOS.loadAndPinWaveObject<WaveWindow>(WOS.makeORef("window", initOpts.windowId)),
            WOS.loadAndPinWaveObject<Tab>(WOS.makeORef("tab", initOpts.tabId)),
        ]);
        perfMark("client/window/tab loaded");
        const [ws, _layoutState] = await Promise.all([
            WOS.loadAndPinWaveObject<Workspace>(WOS.makeORef("workspace", waveWindow.workspaceid)),
            WOS.reloadWaveObject<LayoutState>(WOS.makeORef("layout", initialTab.layoutstate)),
        ]);
        perfMark("workspace/layout loaded");
        loadAllWorkspaceTabs(ws);
        WOS.wpsSubscribeToObject(WOS.makeORef("workspace", waveWindow.workspaceid));
        document.title = `Wave Terminal - ${initialTab.name}`; // TODO update with tab name change
    } catch (e) {
        console.error("Failed initialization error", e);
        getApi().sendLog("Error in initialization (wave.ts, loading required objects) " + e.message + "\n" + e.stack);
    }
    registerGlobalKeys();
    registerElectronReinjectKeyHandler();
    registerControlShiftStateUpdateHandler();
    // chrome strip's AI button toggles this content tab's AI panel
    getApi().onToggleWaveAI(() => {
        const m = WorkspaceLayoutModel.getInstance();
        m.setAIPanelVisible(!m.getAIPanelVisible());
    });
    await loadMonaco();
    perfMark("monaco loaded");
    const fullConfig = await RpcApi.GetFullConfigCommand(TabRpcClient);
    console.log("fullconfig", fullConfig);
    globalStore.set(atoms.fullConfigAtom, fullConfig);
    perfMark("fullConfig loaded");
    const waveaiModeConfig = await RpcApi.GetWaveAIModeConfigCommand(TabRpcClient);
    globalStore.set(atoms.waveaiModeConfigAtom, waveaiModeConfig.configs);
    perfMark("waveAIModeConfig loaded (pre-render)");
    console.log("Wave First Render");
    let firstRenderResolveFn: () => void = null;
    const firstRenderPromise = new Promise<void>((resolve) => {
        firstRenderResolveFn = resolve;
    });
    const reactElem = createElement(App, { onFirstRender: firstRenderResolveFn }, null);
    const elem = document.getElementById("main");
    const root = createRoot(elem);
    root.render(reactElem);
    await firstRenderPromise;
    perfMark("first render done");
    console.log("Wave First Render Done");
    getApi().setWindowInitStatus("wave-ready");
}

async function initChromeWrap(initOpts: ChromeInitOpts) {
    try {
        await initChrome(initOpts);
    } catch (e) {
        getApi().sendLog("Error in initChrome " + e.message + "\n" + e.stack);
        console.error("Error in initChrome", e);
    } finally {
        document.body.style.visibility = null;
        document.body.style.opacity = null;
        document.body.classList.remove("is-transparent");
    }
}

// initChrome boots the persistent chrome renderer: window-scoped (no tabId), renders ChromeApp
// (tab bar + workspace switcher). Mirrors initBuilder's shape — no tab/layout/monaco/global-key
// setup (the tab content renderers own those).
async function initChrome(initOpts: ChromeInitOpts) {
    getApi().sendLog("Init Chrome " + JSON.stringify(initOpts));
    // the chrome renderer is transparent below the strip (so an expanded popover shows the content)
    document.body.classList.add("chrome-window");
    getApi().onChromeWaveAIState((isOpen) => globalStore.set(chromeWaveAIOpenAtom, isOpen));
    getApi().onOpenWorkspaceSwitcher(() => globalStore.set(workspaceSwitcherOpenAtom, true));
    getApi().onChromeLoading((loading) => globalStore.set(chromeLoadingAtom, loading));
    // the chrome renderer persists across workspace switches; reload the window (its workspaceid
    // drives atoms.workspaceId/workspace) and the new workspace's tabs so the tab bar follows
    getApi().onChromeWorkspaceChanged(() => {
        void (async () => {
            const win = await WOS.reloadWaveObject<WaveWindow>(WOS.makeORef("window", initOpts.windowId));
            const newWs = await WOS.loadAndPinWaveObject<Workspace>(WOS.makeORef("workspace", win.workspaceid));
            loadAllWorkspaceTabs(newWs);
            WOS.wpsSubscribeToObject(WOS.makeORef("workspace", win.workspaceid));
        })();
    });
    const globalInitOpts: GlobalInitOptions = {
        clientId: initOpts.clientId,
        windowId: initOpts.windowId,
        platform,
        environment: "renderer",
        isChrome: true,
    };
    console.log("Wave Chrome Init", globalInitOpts);
    await GlobalModel.getInstance().initialize(globalInitOpts);
    initGlobal(globalInitOpts);
    (window as any).globalAtoms = atoms;

    const globalWS = initWshrpc(makeWindowRouteId(initOpts.windowId));
    (window as any).globalWS = globalWS;
    (window as any).TabRpcClient = TabRpcClient;
    await loadConnStatus();

    const [_client, waveWindow] = await Promise.all([
        WOS.loadAndPinWaveObject<Client>(WOS.makeORef("client", initOpts.clientId)),
        WOS.loadAndPinWaveObject<WaveWindow>(WOS.makeORef("window", initOpts.windowId)),
    ]);
    const ws = await WOS.loadAndPinWaveObject<Workspace>(WOS.makeORef("workspace", waveWindow.workspaceid));
    loadAllWorkspaceTabs(ws);
    WOS.wpsSubscribeToObject(WOS.makeORef("workspace", waveWindow.workspaceid));

    const fullConfig = await RpcApi.GetFullConfigCommand(TabRpcClient);
    globalStore.set(atoms.fullConfigAtom, fullConfig);
    const waveaiModeConfig = await RpcApi.GetWaveAIModeConfigCommand(TabRpcClient);
    globalStore.set(atoms.waveaiModeConfigAtom, waveaiModeConfig.configs);

    let firstRenderResolveFn: () => void = null;
    const firstRenderPromise = new Promise<void>((resolve) => {
        firstRenderResolveFn = resolve;
    });
    const reactElem = createElement(ChromeApp, { onFirstRender: firstRenderResolveFn }, null);
    const elem = document.getElementById("main");
    const root = createRoot(elem);
    root.render(reactElem);
    await firstRenderPromise;
    getApi().setWindowInitStatus("wave-ready");
}

async function initBuilderWrap(initOpts: BuilderInitOpts) {
    try {
        await initBuilder(initOpts);
    } catch (e) {
        getApi().sendLog("Error in initBuilder " + e.message + "\n" + e.stack);
        console.error("Error in initBuilder", e);
    } finally {
        document.body.style.visibility = null;
        document.body.style.opacity = null;
        document.body.classList.remove("is-transparent");
    }
}

async function initBuilder(initOpts: BuilderInitOpts) {
    getApi().sendLog("Init Builder " + JSON.stringify(initOpts));
    const globalInitOpts: GlobalInitOptions = {
        clientId: initOpts.clientId,
        windowId: initOpts.windowId,
        platform,
        environment: "renderer",
        builderId: initOpts.builderId,
    };
    console.log("Tsunami Builder Init", globalInitOpts);
    await GlobalModel.getInstance().initialize(globalInitOpts);
    initGlobal(globalInitOpts);
    (window as any).globalAtoms = atoms;

    const globalWS = initWshrpc(makeBuilderRouteId(initOpts.builderId));
    (window as any).globalWS = globalWS;
    (window as any).TabRpcClient = TabRpcClient;
    await loadConnStatus();

    let appIdToUse: string = null;
    try {
        const oref = WOS.makeORef("builder", initOpts.builderId);
        const rtInfo = await RpcApi.GetRTInfoCommand(TabRpcClient, { oref });
        if (rtInfo && rtInfo["builder:appid"]) {
            appIdToUse = rtInfo["builder:appid"];
        }
    } catch (e) {
        console.log("Could not load saved builder appId from rtinfo:", e);
    }

    document.title = appIdToUse ? `WaveApp Builder (${appIdToUse})` : "WaveApp Builder";

    globalStore.set(atoms.builderAppId, appIdToUse);

    const _client = await WOS.loadAndPinWaveObject<Client>(WOS.makeORef("client", initOpts.clientId));

    registerBuilderGlobalKeys();
    registerElectronReinjectKeyHandler();
    await loadMonaco();
    const fullConfig = await RpcApi.GetFullConfigCommand(TabRpcClient);
    console.log("fullconfig", fullConfig);
    globalStore.set(atoms.fullConfigAtom, fullConfig);
    const waveaiModeConfig = await RpcApi.GetWaveAIModeConfigCommand(TabRpcClient);
    globalStore.set(atoms.waveaiModeConfigAtom, waveaiModeConfig.configs);

    console.log("Tsunami Builder First Render");
    let firstRenderResolveFn: () => void = null;
    const firstRenderPromise = new Promise<void>((resolve) => {
        firstRenderResolveFn = resolve;
    });
    const reactElem = createElement(BuilderApp, { initOpts, onFirstRender: firstRenderResolveFn }, null);
    const elem = document.getElementById("main");
    const root = createRoot(elem);
    root.render(reactElem);
    await firstRenderPromise;
    console.log("Tsunami Builder First Render Done");
}

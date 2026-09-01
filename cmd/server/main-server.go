// Copyright 2025, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/joho/godotenv"
	"github.com/wavetermdev/waveterm/pkg/aiusechat"
	"github.com/wavetermdev/waveterm/pkg/authkey"
	"github.com/wavetermdev/waveterm/pkg/blockcontroller"
	"github.com/wavetermdev/waveterm/pkg/blocklogger"
	"github.com/wavetermdev/waveterm/pkg/filebackup"
	"github.com/wavetermdev/waveterm/pkg/filestore"
	"github.com/wavetermdev/waveterm/pkg/jobcontroller"
	"github.com/wavetermdev/waveterm/pkg/panichandler"
	"github.com/wavetermdev/waveterm/pkg/remote/fileshare/wshfs"
	"github.com/wavetermdev/waveterm/pkg/service"
	"github.com/wavetermdev/waveterm/pkg/taskapi"
	"github.com/wavetermdev/waveterm/pkg/util/envutil"
	"github.com/wavetermdev/waveterm/pkg/util/shellutil"
	"github.com/wavetermdev/waveterm/pkg/util/sigutil"
	"github.com/wavetermdev/waveterm/pkg/wavebase"
	"github.com/wavetermdev/waveterm/pkg/waveobj"
	"github.com/wavetermdev/waveterm/pkg/wconfig"
	"github.com/wavetermdev/waveterm/pkg/wcore"
	"github.com/wavetermdev/waveterm/pkg/web"
	"github.com/wavetermdev/waveterm/pkg/wps"
	"github.com/wavetermdev/waveterm/pkg/wshrpc"
	"github.com/wavetermdev/waveterm/pkg/wshrpc/wshclient"
	"github.com/wavetermdev/waveterm/pkg/wshrpc/wshremote"
	"github.com/wavetermdev/waveterm/pkg/wshrpc/wshserver"
	"github.com/wavetermdev/waveterm/pkg/wshutil"
	"github.com/wavetermdev/waveterm/pkg/wstore"

	_ "net/http/pprof"
)

// these are set at build time
var (
	WaveVersion = "0.0.0"
	BuildTime   = "0"
)

const (
	BackupCleanupTick     = 2 * time.Minute
	BackupCleanupInterval = 4 * time.Hour
)

var shutdownOnce sync.Once

func init() {
	envFilePath := os.Getenv("WAVETERM_ENVFILE")
	if envFilePath != "" {
		log.Printf("applying env file: %s\n", envFilePath)
		_ = godotenv.Load(envFilePath)
	}
}

func doShutdown(reason string) {
	shutdownOnce.Do(func() {
		log.Printf("shutting down: %s\n", reason)
		ctx, cancelFn := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelFn()
		go blockcontroller.StopAllBlockControllersForShutdown()
		// TODO deal with flush in progress
		clearTempFiles()
		filestore.WFS.FlushCache(ctx)
		watcher := wconfig.GetWatcher()
		if watcher != nil {
			watcher.Close()
		}
		time.Sleep(500 * time.Millisecond)
		log.Printf("shutdown complete\n")
		os.Exit(0)
	})
}

// watch stdin, kill server if stdin is closed
func stdinReadWatch() {
	defer func() {
		panichandler.PanicHandler("stdinReadWatch", recover())
	}()
	buf := make([]byte, 1024)
	for {
		_, err := os.Stdin.Read(buf)
		if err != nil {
			doShutdown(fmt.Sprintf("stdin closed/error (%v)", err))
			break
		}
	}
}

func startConfigWatcher() {
	watcher := wconfig.GetWatcher()
	if watcher != nil {
		watcher.Start()
	}
}

func backupCleanupLoop() {
	defer func() {
		panichandler.PanicHandler("backupCleanupLoop", recover())
	}()
	var nextCleanup int64
	for {
		if time.Now().Unix() > nextCleanup {
			nextCleanup = time.Now().Add(BackupCleanupInterval).Unix()
			err := filebackup.CleanupOldBackups()
			if err != nil {
				log.Printf("error cleaning up old backups: %v\n", err)
			}
		}
		time.Sleep(BackupCleanupTick)
	}
}

func createMainWshClient() {
	rpc := wshserver.GetMainRpcClient()
	wshutil.DefaultRouter.RegisterTrustedLeaf(rpc, wshutil.DefaultRoute)
	wps.Broker.SetClient(wshutil.DefaultRouter)
	localInitialEnv := envutil.PruneInitialEnv(envutil.SliceToMap(os.Environ()))
	sockName := wavebase.GetDomainSocketName()
	remoteImpl := wshremote.MakeRemoteRpcServerImpl(nil, wshutil.DefaultRouter, wshclient.GetBareRpcClient(), true, localInitialEnv, sockName)
	localConnWsh := wshutil.MakeWshRpc(wshrpc.RpcContext{Conn: wshrpc.LocalConnName}, remoteImpl, "conn:local")
	go wshremote.RunSysInfoLoop(localConnWsh, wshrpc.LocalConnName)
	wshutil.DefaultRouter.RegisterTrustedLeaf(localConnWsh, wshutil.MakeConnectionRouteId(wshrpc.LocalConnName))
	wshfs.RpcClient = localConnWsh
	wshfs.RpcClientRouteId = wshutil.MakeConnectionRouteId(wshrpc.LocalConnName)
}

func grabAndRemoveEnvVars() error {
	err := authkey.SetAuthKeyFromEnv()
	if err != nil {
		return fmt.Errorf("setting auth key: %v", err)
	}
	err = wavebase.CacheAndRemoveEnvVars()
	if err != nil {
		return err
	}

	// Remove WAVETERM env vars that leak from prod => dev
	os.Unsetenv("WAVETERM_CLIENTID")
	os.Unsetenv("WAVETERM_WORKSPACEID")
	os.Unsetenv("WAVETERM_TABID")
	os.Unsetenv("WAVETERM_BLOCKID")
	os.Unsetenv("WAVETERM_CONN")
	os.Unsetenv("WAVETERM_JWT")
	os.Unsetenv("WAVETERM_VERSION")

	return nil
}

func clearTempFiles() error {
	ctx, cancelFn := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelFn()
	client, err := wstore.DBGetSingleton[*waveobj.Client](ctx)
	if err != nil {
		return fmt.Errorf("error getting client: %v", err)
	}
	filestore.WFS.DeleteZone(ctx, client.TempOID)
	return nil
}

func maybeStartPprofServer() {
	settings := wconfig.GetWatcher().GetFullConfig().Settings
	if settings.DebugPprofMemProfileRate != nil {
		runtime.MemProfileRate = *settings.DebugPprofMemProfileRate
		log.Printf("set runtime.MemProfileRate to %d\n", runtime.MemProfileRate)
	}
	if settings.DebugPprofPort == nil {
		return
	}
	pprofPort := *settings.DebugPprofPort
	if pprofPort < 1 || pprofPort > 65535 {
		log.Printf("[error] debug:pprofport must be between 1 and 65535, got %d\n", pprofPort)
		return
	}
	go func() {
		addr := fmt.Sprintf("localhost:%d", pprofPort)
		log.Printf("starting pprof server on %s\n", addr)
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Printf("[error] pprof server failed: %v\n", err)
		}
	}()
}

func main() {
	log.SetFlags(0) // disable timestamp since electron's winston logger already wraps with timestamp
	log.SetPrefix("[wavesrv] ")
	wavebase.WaveVersion = WaveVersion
	wavebase.BuildTime = BuildTime
	wshutil.DefaultRouter = wshutil.NewWshRouter()
	wshutil.DefaultRouter.SetAsRootRouter()

	err := grabAndRemoveEnvVars()
	if err != nil {
		log.Printf("[error] %v\n", err)
		return
	}
	err = service.ValidateServiceMap()
	if err != nil {
		log.Printf("error validating service map: %v\n", err)
		return
	}
	err = wavebase.EnsureWaveDataDir()
	if err != nil {
		log.Printf("error ensuring wave home dir: %v\n", err)
		return
	}
	err = wavebase.EnsureWaveDBDir()
	if err != nil {
		log.Printf("error ensuring wave db dir: %v\n", err)
		return
	}
	err = wavebase.EnsureWaveConfigDir()
	if err != nil {
		log.Printf("error ensuring wave config dir: %v\n", err)
		return
	}

	// TODO: rather than ensure this dir exists, we should let the editor recursively create parent dirs on save
	err = wavebase.EnsureWavePresetsDir()
	if err != nil {
		log.Printf("error ensuring wave presets dir: %v\n", err)
		return
	}
	err = wavebase.EnsureWaveCachesDir()
	if err != nil {
		log.Printf("error ensuring wave caches dir: %v\n", err)
		return
	}
	waveLock, err := wavebase.AcquireWaveLock()
	if err != nil {
		log.Printf("error acquiring wave lock (another instance of Wave is likely running): %v\n", err)
		return
	}
	defer func() {
		err = waveLock.Close()
		if err != nil {
			log.Printf("error releasing wave lock: %v\n", err)
		}
	}()
	log.Printf("wave version: %s (%s)\n", WaveVersion, BuildTime)
	log.Printf("wave data dir: %s\n", wavebase.GetWaveDataDir())
	log.Printf("wave config dir: %s\n", wavebase.GetWaveConfigDir())
	err = filestore.InitFilestore()
	if err != nil {
		log.Printf("error initializing filestore: %v\n", err)
		return
	}
	err = wstore.InitWStore()
	if err != nil {
		log.Printf("error initializing wstore: %v\n", err)
		return
	}
	go func() {
		defer func() {
			panichandler.PanicHandler("InitCustomShellStartupFiles", recover())
		}()
		err := shellutil.InitCustomShellStartupFiles()
		if err != nil {
			log.Printf("error initializing wsh and shell-integration files: %v\n", err)
		}
	}()
	firstLaunch, err := wcore.EnsureInitialData()
	if err != nil {
		log.Printf("error ensuring initial data: %v\n", err)
		return
	}
	if firstLaunch {
		log.Printf("first launch detected")
	}
	// Self-heal orphaned blocks (empty/unparseable parentoref, or a parent chain
	// that no longer reaches a live tab) before any client connects and resolves
	// block controllers. An orphan otherwise makes DBFindTabForBlockId fail and
	// bricks the whole bootstrap. Best-effort: a GC hiccup must not block startup.
	gcCtx, gcCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if gcErr := wcore.GCOrphanedBlocks(gcCtx); gcErr != nil {
		log.Printf("error gc'ing orphaned blocks: %v\n", gcErr)
	}
	gcCancel()
	err = clearTempFiles()
	if err != nil {
		log.Printf("error clearing temp files: %v\n", err)
		return
	}
	err = wcore.InitMainServer()
	if err != nil {
		log.Printf("error initializing mainserver: %v\n", err)
		return
	}

	err = shellutil.FixupWaveZshHistory()
	if err != nil {
		log.Printf("error fixing up wave zsh history: %v\n", err)
	}
	createMainWshClient()
	sigutil.InstallShutdownSignalHandlers(doShutdown)
	sigutil.InstallSIGUSR1Handler()
	wconfig.MigratePresetsBackgrounds()
	startConfigWatcher()
	aiusechat.InitAIModeConfigWatcher()
	maybeStartPprofServer()
	go stdinReadWatch()
	go backupCleanupLoop()
	blocklogger.InitBlockLogger()
	jobcontroller.InitJobController()
	blockcontroller.InitBlockController()
	err = wcore.InitBadgeStore()
	if err != nil {
		log.Printf("error initializing badge store: %v\n", err)
		return
	}
	go func() {
		defer func() {
			panichandler.PanicHandler("GetSystemSummary", recover())
		}()
		wavebase.GetSystemSummary()
	}()

	// No-TCP mode (wave-native): serve the web API and the ws message protocol
	// on unix domain sockets so wavesrv occupies no local port and cannot be
	// reached over the network. Electron keeps loopback TCP (Chromium cannot
	// fetch over unix sockets).
	useUnixWeb := os.Getenv(wavebase.WaveWebUnixSocketVarName) != ""
	var webListener, wsListener net.Listener
	var wsAddrStr, webAddrStr string
	if useUnixWeb {
		dataDir := wavebase.GetWaveDataDir()
		webListener, err = web.MakeUnixListenerAt(filepath.Join(dataDir, "web.sock"), "web")
		if err != nil {
			log.Printf("error creating web unix listener: %v\n", err)
			return
		}
		wsListener, err = web.MakeUnixListenerAt(filepath.Join(dataDir, "ws.sock"), "websocket")
		if err != nil {
			log.Printf("error creating websocket unix listener: %v\n", err)
			return
		}
		// The ESTART line is space-delimited, so socket paths (which may
		// contain spaces -- "Application Support") ride percent-encoded.
		wsAddrStr = "unix:" + (&url.URL{Path: wsListener.Addr().String()}).EscapedPath()
		webAddrStr = "unix:" + (&url.URL{Path: webListener.Addr().String()}).EscapedPath()
		go web.RunWSUnixServer(wsListener)
	} else {
		webListener, err = web.MakeTCPListener("web")
		if err != nil {
			log.Printf("error creating web listener: %v\n", err)
			return
		}
		wsListener, err = web.MakeTCPListener("websocket")
		if err != nil {
			log.Printf("error creating websocket listener: %v\n", err)
			return
		}
		wsAddrStr = wsListener.Addr().String()
		webAddrStr = webListener.Addr().String()
		go web.RunWebSocketServer(wsListener)
	}
	// Always TCP (even in no-TCP mode): serves sandboxed processes (docker
	// containers) that cannot reach unix sockets; auth is a workspace-scoped JWT.
	if err := taskapi.StartServer(); err != nil {
		log.Printf("error starting tasks api server: %v\n", err)
	}
	unixListener, err := web.MakeUnixListener()
	if err != nil {
		log.Printf("error creating unix listener: %v\n", err)
		return
	}
	go func() {
		if BuildTime == "" {
			BuildTime = "0"
		}
		// use fmt instead of log here to make sure it goes directly to stderr
		fmt.Fprintf(os.Stderr, "WAVESRV-ESTART ws:%s web:%s version:%s buildtime:%s\n", wsAddrStr, webAddrStr, WaveVersion, BuildTime)
	}()
	go wshutil.RunWshRpcOverListener(unixListener, nil)
	web.RunWebServer(webListener) // blocking
	runtime.KeepAlive(waveLock)
}

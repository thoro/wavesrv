// Copyright 2025, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/wavetermdev/waveterm/pkg/authkey"
	"github.com/wavetermdev/waveterm/pkg/baseds"
	"github.com/wavetermdev/waveterm/pkg/eventbus"
	"github.com/wavetermdev/waveterm/pkg/panichandler"
	"github.com/wavetermdev/waveterm/pkg/web/webcmd"
	"github.com/wavetermdev/waveterm/pkg/wshutil"
)

const wsReadWaitTimeout = 15 * time.Second
const wsWriteWaitTimeout = 10 * time.Second
const wsPingPeriodTickTime = 10 * time.Second
const wsInitialPingTime = 1 * time.Second
const wsMaxMessageSize = 10 * 1024 * 1024

const DefaultCommandTimeout = 2 * time.Second
const WebSocketChannelSize = 128

type StableConnInfo struct {
	ConnId string
	LinkId baseds.LinkId
}

var GlobalLock = &sync.Mutex{}
var RouteToConnMap = map[string]*StableConnInfo{} // stableid => StableConnInfo

func RunWebSocketServer(listener net.Listener) {
	gr := mux.NewRouter()
	gr.HandleFunc("/ws", HandleWs)
	server := &http.Server{
		ReadTimeout:    HttpReadTimeout,
		WriteTimeout:   HttpWriteTimeout,
		MaxHeaderBytes: HttpMaxHeaderBytes,
		Handler:        gr,
	}
	server.SetKeepAlivesEnabled(false)
	log.Printf("[websocket] running websocket server on %s\n", listener.Addr())
	err := server.Serve(listener)
	if err != nil {
		log.Printf("[websocket] error trying to run websocket server: %v\n", err)
	}
}

var WebSocketUpgrader = websocket.Upgrader{
	ReadBufferSize:   4 * 1024,
	WriteBufferSize:  32 * 1024,
	HandshakeTimeout: 1 * time.Second,
	CheckOrigin:      func(r *http.Request) bool { return true },
}

// wsConnAdapter abstracts the message transport under the ws command loop so the
// same read/write loops serve gorilla websocket conns (Electron) and
// newline-framed JSON over unix domain sockets (wave-native, no TCP port).
type wsConnAdapter interface {
	ReadMessage() ([]byte, error)
	WriteMessage(data []byte) error
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
	Close() error
}

type gorillaWSAdapter struct {
	conn *websocket.Conn
}

func (a *gorillaWSAdapter) ReadMessage() ([]byte, error) {
	_, msg, err := a.conn.ReadMessage()
	return msg, err
}

func (a *gorillaWSAdapter) WriteMessage(data []byte) error {
	return a.conn.WriteMessage(websocket.TextMessage, data)
}

func (a *gorillaWSAdapter) SetReadDeadline(t time.Time) error  { return a.conn.SetReadDeadline(t) }
func (a *gorillaWSAdapter) SetWriteDeadline(t time.Time) error { return a.conn.SetWriteDeadline(t) }
func (a *gorillaWSAdapter) Close() error                       { return a.conn.Close() }

// lineWSAdapter frames each JSON message as one newline-terminated line over a
// raw stream (unix domain socket). JSON never contains literal newlines, so the
// framing is unambiguous.
type lineWSAdapter struct {
	conn   net.Conn
	reader *bufio.Reader
}

func makeLineWSAdapter(conn net.Conn) *lineWSAdapter {
	return &lineWSAdapter{conn: conn, reader: bufio.NewReaderSize(conn, 64*1024)}
}

func (a *lineWSAdapter) ReadMessage() ([]byte, error) {
	var buf []byte
	for {
		chunk, err := a.reader.ReadSlice('\n')
		buf = append(buf, chunk...)
		if err == nil {
			break
		}
		if err != bufio.ErrBufferFull {
			return nil, err
		}
		if len(buf) > wsMaxMessageSize {
			return nil, fmt.Errorf("message exceeds max size %d", wsMaxMessageSize)
		}
	}
	return bytes.TrimRight(buf, "\n"), nil
}

func (a *lineWSAdapter) WriteMessage(data []byte) error {
	_, err := a.conn.Write(append(data, '\n'))
	return err
}

func (a *lineWSAdapter) SetReadDeadline(t time.Time) error  { return a.conn.SetReadDeadline(t) }
func (a *lineWSAdapter) SetWriteDeadline(t time.Time) error { return a.conn.SetWriteDeadline(t) }
func (a *lineWSAdapter) Close() error                       { return a.conn.Close() }

func HandleWs(w http.ResponseWriter, r *http.Request) {
	err := HandleWsInternal(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func getMessageType(jmsg map[string]any) string {
	if str, ok := jmsg["type"].(string); ok {
		return str
	}
	return ""
}

func getStringFromMap(jmsg map[string]any, key string) string {
	if str, ok := jmsg[key].(string); ok {
		return str
	}
	return ""
}

func processWSCommand(jmsg map[string]any, outputCh chan any, rpcInputCh chan baseds.RpcInputChType) {
	var rtnErr error
	var cmdType string
	defer func() {
		panicCtx := "processWSCommand"
		if cmdType != "" {
			panicCtx = fmt.Sprintf("processWSCommand:%s", cmdType)
		}
		panicErr := panichandler.PanicHandler(panicCtx, recover())
		if panicErr != nil {
			rtnErr = panicErr
		}
		if rtnErr == nil {
			return
		}
		rtn := map[string]any{"type": "error", "error": rtnErr.Error()}
		outputCh <- rtn
	}()
	wsCommand, err := webcmd.ParseWSCommandMap(jmsg)
	if err != nil {
		rtnErr = fmt.Errorf("cannot parse wscommand: %v", err)
		return
	}
	cmdType = wsCommand.GetWSCommand()
	switch cmd := wsCommand.(type) {
	case *webcmd.WSRpcCommand:
		rpcMsg := cmd.Message
		if rpcMsg == nil {
			return
		}
		if rpcMsg.Command != "" {
			cmdType = fmt.Sprintf("%s:%s", cmdType, rpcMsg.Command)
		}
		msgBytes, err := json.Marshal(rpcMsg)
		if err != nil {
			// this really should never fail since we just unmarshalled this value
			return
		}
		rpcInputCh <- baseds.RpcInputChType{MsgBytes: msgBytes}
	}
}

func ReadLoop(conn wsConnAdapter, outputCh chan any, closeCh chan any, rpcInputCh chan baseds.RpcInputChType, routeId string) {
	readWait := wsReadWaitTimeout
	conn.SetReadDeadline(time.Now().Add(readWait))
	defer close(closeCh)
	for {
		message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("[websocket] ReadPump error (%s): %v\n", routeId, err)
			break
		}
		jmsg := map[string]any{}
		err = json.Unmarshal(message, &jmsg)
		if err != nil {
			log.Printf("[websocket] error unmarshalling json: %v\n", err)
			break
		}
		conn.SetReadDeadline(time.Now().Add(readWait))
		msgType := getMessageType(jmsg)
		if msgType == "pong" {
			// nothing
			continue
		}
		if msgType == "ping" {
			now := time.Now()
			pongMessage := map[string]interface{}{"type": "pong", "stime": now.UnixMilli()}
			outputCh <- pongMessage
			continue
		}
		wsCommand := getStringFromMap(jmsg, "wscommand")
		if wsCommand == "" {
			continue
		}
		processWSCommand(jmsg, outputCh, rpcInputCh)
	}
}

func WritePing(conn wsConnAdapter) error {
	now := time.Now()
	pingMessage := map[string]interface{}{"type": "ping", "stime": now.UnixMilli()}
	jsonVal, _ := json.Marshal(pingMessage)
	_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWaitTimeout)) // no error
	err := conn.WriteMessage(jsonVal)
	if err != nil {
		return err
	}
	return nil
}

func WriteLoop(conn wsConnAdapter, outputCh chan any, closeCh chan any, routeId string) {
	ticker := time.NewTicker(wsInitialPingTime)
	defer ticker.Stop()
	initialPing := true
	for {
		select {
		case msg := <-outputCh:
			var barr []byte
			var err error
			if _, ok := msg.([]byte); ok {
				barr = msg.([]byte)
			} else {
				barr, err = json.Marshal(msg)
				if err != nil {
					log.Printf("[websocket] cannot marshal websocket message: %v\n", err)
					// just loop again
					break
				}
			}
			// The deadline is ABSOLUTE, so it must be re-armed for every write. When
			// only WritePing armed it, any event write landing >10s after the last
			// ping hit an already-expired deadline and failed instantly with
			// "i/o timeout", killing every busy connection every ~70s.
			_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWaitTimeout))
			err = conn.WriteMessage(barr)
			if err != nil {
				conn.Close()
				log.Printf("[websocket] WritePump error (%s): %v\n", routeId, err)
				return
			}

		case <-ticker.C:
			err := WritePing(conn)
			if err != nil {
				log.Printf("[websocket] WritePump error (%s): %v\n", routeId, err)
				return
			}
			if initialPing {
				initialPing = false
				ticker.Reset(wsPingPeriodTickTime)
			}

		case <-closeCh:
			return
		}
	}
}

func registerConn(wsConnId string, stableId string, wproxy *wshutil.WshRpcProxy) {
	GlobalLock.Lock()
	defer GlobalLock.Unlock()
	curConnInfo := RouteToConnMap[stableId]
	if curConnInfo != nil {
		log.Printf("[websocket] warning: replacing existing connection for stableid %q\n", stableId)
		if curConnInfo.LinkId != baseds.NoLinkId {
			wshutil.DefaultRouter.UnregisterLink(curConnInfo.LinkId)
		}
	}
	linkId := wshutil.DefaultRouter.RegisterTrustedRouter(wproxy)
	RouteToConnMap[stableId] = &StableConnInfo{
		ConnId: wsConnId,
		LinkId: linkId,
	}
}

func unregisterConn(wsConnId string, stableId string) {
	GlobalLock.Lock()
	defer GlobalLock.Unlock()
	curConnInfo := RouteToConnMap[stableId]
	if curConnInfo == nil || curConnInfo.ConnId != wsConnId {
		log.Printf("[websocket] warning: trying to unregister connection %q for stableid %q but it is not the current connection (ignoring)\n", wsConnId, stableId)
		return
	}
	delete(RouteToConnMap, stableId)
	if curConnInfo.LinkId != baseds.NoLinkId {
		wshutil.DefaultRouter.UnregisterLink(curConnInfo.LinkId)
	}
}

func HandleWsInternal(w http.ResponseWriter, r *http.Request) error {
	stableId := r.URL.Query().Get("stableid")
	if stableId == "" {
		return fmt.Errorf("stableid is required")
	}
	err := authkey.ValidateIncomingRequest(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(fmt.Sprintf("error validating authkey: %v", err)))
		log.Printf("[websocket] error validating authkey: %v\n", err)
		return err
	}
	wsConn, err := WebSocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return fmt.Errorf("WebSocket Upgrade Failed: %v", err)
	}
	wsConn.SetReadLimit(wsMaxMessageSize)
	runWSConnection(&gorillaWSAdapter{conn: wsConn}, stableId)
	return nil
}

// wsUnixAuthMsg is the single handshake line a unix-socket client sends before
// the normal message loop (replaces the WS upgrade's query param + header).
type wsUnixAuthMsg struct {
	Type     string `json:"type"`
	AuthKey  string `json:"authkey"`
	StableId string `json:"stableid"`
}

// RunWSUnixServer serves the websocket message protocol over a unix domain
// socket: one newline-framed JSON message per line, authenticated by a
// mandatory first line {"type":"authenticate","authkey":...,"stableid":...}.
// Blocking accept loop (run in a goroutine).
func RunWSUnixServer(listener net.Listener) {
	log.Printf("[websocket] running unix-socket ws server on %s\n", listener.Addr())
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("[websocket] unix accept error: %v\n", err)
			return
		}
		go func() {
			defer func() {
				panichandler.PanicHandler("RunWSUnixServer:conn", recover())
			}()
			handleWSUnixConn(conn)
		}()
	}
}

func handleWSUnixConn(conn net.Conn) {
	defer conn.Close()
	adapter := makeLineWSAdapter(conn)
	adapter.SetReadDeadline(time.Now().Add(wsReadWaitTimeout))
	authLine, err := adapter.ReadMessage()
	if err != nil {
		log.Printf("[websocket] unix conn: error reading auth line: %v\n", err)
		return
	}
	var auth wsUnixAuthMsg
	if err := json.Unmarshal(authLine, &auth); err != nil || auth.Type != "authenticate" {
		log.Printf("[websocket] unix conn: bad auth line\n")
		return
	}
	if auth.StableId == "" || !authkey.CompareAuthKey(auth.AuthKey) {
		log.Printf("[websocket] unix conn: authentication failed\n")
		adapter.WriteMessage([]byte(`{"type":"error","error":"authentication failed"}`))
		return
	}
	runWSConnection(adapter, auth.StableId)
}

// runWSConnection drives one authenticated connection (any transport) through
// the shared read/write loops + rpc proxy registration until it closes.
func runWSConnection(conn wsConnAdapter, stableId string) {
	defer conn.Close()
	wsConnId := uuid.New().String()
	outputCh := make(chan any, WebSocketChannelSize)
	closeCh := make(chan any)
	log.Printf("[websocket] new connection: connid:%s stableid:%s\n", wsConnId, stableId)
	eventbus.RegisterWSChannel(wsConnId, stableId, outputCh)
	defer eventbus.UnregisterWSChannel(wsConnId)
	wproxy := wshutil.MakeRpcProxyWithSize(fmt.Sprintf("ws:%s", stableId), WebSocketChannelSize, WebSocketChannelSize)
	defer wproxy.CloseToRemoteCh()
	registerConn(wsConnId, stableId, wproxy)
	defer unregisterConn(wsConnId, stableId)
	wg := &sync.WaitGroup{}
	wg.Add(2)
	go func() {
		defer func() {
			panichandler.PanicHandler("runWSConnection:outputCh", recover())
		}()
		// no waitgroup add here
		// move values from rpcOutputCh to outputCh
		for msgBytes := range wproxy.ToRemoteCh {
			rpcWSMsg := map[string]any{
				"eventtype": "rpc", // TODO don't hard code this (but def is in eventbus)
				"data":      json.RawMessage(msgBytes),
			}
			outputCh <- rpcWSMsg
		}
	}()
	go func() {
		defer func() {
			panichandler.PanicHandler("runWSConnection:ReadLoop", recover())
		}()
		defer wg.Done()
		ReadLoop(conn, outputCh, closeCh, wproxy.FromRemoteCh, stableId)
	}()
	go func() {
		defer func() {
			panichandler.PanicHandler("runWSConnection:WriteLoop", recover())
		}()
		defer wg.Done()
		WriteLoop(conn, outputCh, closeCh, stableId)
	}()
	wg.Wait()
	close(wproxy.FromRemoteCh)
}

// Copyright 2025, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package wshutil

import (
	"fmt"
	"sync"

	"github.com/wavetermdev/waveterm/pkg/baseds"
	"github.com/wavetermdev/waveterm/pkg/panichandler"
	"github.com/wavetermdev/waveterm/pkg/wshrpc"
)

type WshRpcProxy struct {
	Lock         *sync.Mutex
	RpcContext   *wshrpc.RpcContext
	ToRemoteCh   chan []byte
	FromRemoteCh chan baseds.RpcInputChType
	PeerInfo     string
	toRemoteDone bool
}

func MakeRpcProxy(peerInfo string) *WshRpcProxy {
	return MakeRpcProxyWithSize(peerInfo, DefaultInputChSize, DefaultOutputChSize)
}

func MakeRpcProxyWithSize(peerInfo string, inputChSize int, outputChSize int) *WshRpcProxy {
	return &WshRpcProxy{
		Lock:         &sync.Mutex{},
		ToRemoteCh:   make(chan []byte, inputChSize),
		FromRemoteCh: make(chan baseds.RpcInputChType, outputChSize),
		PeerInfo:     peerInfo,
	}
}

func (p *WshRpcProxy) GetPeerInfo() string {
	return p.PeerInfo
}

func (p *WshRpcProxy) SetPeerInfo(peerInfo string) {
	p.Lock.Lock()
	defer p.Lock.Unlock()
	p.PeerInfo = peerInfo
}

func (p *WshRpcProxy) SendRpcMessage(msg []byte, ingressLinkId baseds.LinkId, debugStr string) bool {
	defer func() {
		panicCtx := "WshRpcProxy.SendRpcMessage"
		if debugStr != "" {
			panicCtx = fmt.Sprintf("%s:%s", panicCtx, debugStr)
		}
		panichandler.PanicHandler(panicCtx, recover())
	}()
	// The lock (not just the recover) guards the send: a router goroutine can
	// publish an event concurrently with connection teardown, and sending on the
	// just-closed channel panics ("send on closed channel").
	p.Lock.Lock()
	defer p.Lock.Unlock()
	if p.toRemoteDone {
		return false
	}
	select {
	case p.ToRemoteCh <- msg:
		return true
	default:
		return false
	}
}

// CloseToRemoteCh closes ToRemoteCh exactly once, synchronized with
// SendRpcMessage so an in-flight send cannot hit the closed channel. All
// teardown paths must use this instead of closing the channel directly.
func (p *WshRpcProxy) CloseToRemoteCh() {
	p.Lock.Lock()
	defer p.Lock.Unlock()
	if p.toRemoteDone {
		return
	}
	p.toRemoteDone = true
	close(p.ToRemoteCh)
}

func (p *WshRpcProxy) RecvRpcMessage() ([]byte, bool) {
	inputVal, more := <-p.FromRemoteCh
	return inputVal.MsgBytes, more
}

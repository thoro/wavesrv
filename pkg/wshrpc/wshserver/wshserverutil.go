// Copyright 2025, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package wshserver

import (
	"sync"

	"github.com/wavetermdev/waveterm/pkg/baseds"
	"github.com/wavetermdev/waveterm/pkg/wshrpc"
	"github.com/wavetermdev/waveterm/pkg/wshutil"
)

const (
	DefaultOutputChSize = 32
	// The main client is the single ingress for every frontend RPC command. A
	// shallow input channel makes a burst of window-startup traffic trip the
	// router into backlog mode (strict FIFO + throttled drain), which stalls
	// all RPCs past their timeouts — so this one gets a deep buffer.
	MainInputChSize = 4096
)

var waveSrvClient_Singleton *wshutil.WshRpc
var waveSrvClient_Once = &sync.Once{}

// returns the wavesrv main rpc client singleton
func GetMainRpcClient() *wshutil.WshRpc {
	waveSrvClient_Once.Do(func() {
		inputCh := make(chan baseds.RpcInputChType, MainInputChSize)
		waveSrvClient_Singleton = wshutil.MakeWshRpcWithChannels(inputCh, nil, wshrpc.RpcContext{}, &WshServerImpl, "main-client")
	})
	return waveSrvClient_Singleton
}

// Copyright 2025, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package iochan_test

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/wavetermdev/waveterm/pkg/util/iochan"
)

const (
	buflen = 1024
)

func TestIochan_Basic(t *testing.T) {
	// Write the packet to the source pipe from a goroutine
	srcPipeReader, srcPipeWriter := io.Pipe()
	packet := []byte("hello world")
	go func() {
		srcPipeWriter.Write(packet)
		srcPipeWriter.Close()
	}()

	// Initialize the reader channel
	readerChanCallback, readerChanCallbackCalled := onceCallback(func() {
		srcPipeReader.Close()
	})
	defer readerChanCallback() // Ensure the callback is called
	ioch := iochan.ReaderChan(context.TODO(), srcPipeReader, buflen, readerChanCallback)

	// Initialize the destination pipe and the writer channel
	destPipeReader, destPipeWriter := io.Pipe()
	writerChanCallback, writerChanCallbackCalled := onceCallback(func() {
		destPipeReader.Close()
		destPipeWriter.Close()
	})
	defer writerChanCallback() // Ensure the callback is called
	iochan.WriterChan(context.TODO(), destPipeWriter, ioch, writerChanCallback, func(err error) {})

	// Read the packet from the destination pipe and compare it to the original packet
	buf := make([]byte, buflen)
	n, err := destPipeReader.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if n != len(packet) {
		t.Fatalf("Read length mismatch: %d != %d", n, len(packet))
	}
	if string(buf[:n]) != string(packet) {
		t.Fatalf("Read data mismatch: %s != %s", buf[:n], packet)
	}

	// Give the callbacks a chance to run before checking if they were called
	waitCalled(t, readerChanCallbackCalled, "ReaderChan")
	waitCalled(t, writerChanCallbackCalled, "WriterChan")
}

// onceCallback wraps fn so the returned channel closes on the first call. iochan
// invokes callbacks on its own goroutines and the test's deferred calls repeat
// them, so a plain bool flag would be a data race.
func onceCallback(fn func()) (func(), <-chan struct{}) {
	called := make(chan struct{})
	var once sync.Once
	return func() {
		fn()
		once.Do(func() { close(called) })
	}, called
}

func waitCalled(t *testing.T, called <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatalf("%s callback not called", name)
	}
}

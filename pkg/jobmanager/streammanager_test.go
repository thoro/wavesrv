// Copyright 2025, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package jobmanager

import (
	"encoding/base64"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wavetermdev/waveterm/pkg/wshrpc"
)

type testWriter struct {
	mu      sync.Mutex
	packets []wshrpc.CommandStreamData
}

func (tw *testWriter) SendData(pkt wshrpc.CommandStreamData) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	tw.packets = append(tw.packets, pkt)
}

func (tw *testWriter) GetPackets() []wshrpc.CommandStreamData {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	result := make([]wshrpc.CommandStreamData, len(tw.packets))
	copy(result, tw.packets)
	return result
}

func (tw *testWriter) Clear() {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	tw.packets = nil
}

func decodeData(data64 string) string {
	decoded, _ := base64.StdEncoding.DecodeString(data64)
	return string(decoded)
}

func TestBasicDisconnectedMode(t *testing.T) {
	tw := &testWriter{}
	sm := MakeStreamManager()

	reader := strings.NewReader("hello world")
	err := sm.AttachReader(reader)
	if err != nil {
		t.Fatalf("AttachReader failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	packets := tw.GetPackets()
	if len(packets) > 0 {
		t.Errorf("Expected no packets in DISCONNECTED mode without client, got %d", len(packets))
	}

	sm.Close()
}

func TestConnectedModeBasicFlow(t *testing.T) {
	tw := &testWriter{}
	sm := MakeStreamManager()

	reader := strings.NewReader("hello")
	err := sm.AttachReader(reader)
	if err != nil {
		t.Fatalf("AttachReader failed: %v", err)
	}

	_, err = sm.ClientConnected("1", tw, CwndSize, 0)
	if err != nil {
		t.Fatalf("ClientConnected failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	packets := tw.GetPackets()
	if len(packets) == 0 {
		t.Fatal("Expected packets after ClientConnected")
	}

	// Verify we got the data
	allData := ""
	for _, pkt := range packets {
		if pkt.Data64 != "" {
			allData += decodeData(pkt.Data64)
		}
	}

	if allData != "hello" {
		t.Errorf("Expected 'hello', got '%s'", allData)
	}

	// Send ACK
	sm.RecvAck(wshrpc.CommandStreamAckData{Id: "1", Seq: 5, RWnd: CwndSize})

	time.Sleep(50 * time.Millisecond)

	// Check for EOF packet
	packets = tw.GetPackets()
	hasEof := false
	for _, pkt := range packets {
		if pkt.Eof {
			hasEof = true
		}
	}

	if !hasEof {
		t.Error("Expected EOF packet after ACKing all data")
	}

	sm.Close()
}

func TestDisconnectedToConnectedTransition(t *testing.T) {
	tw := &testWriter{}
	sm := MakeStreamManager()

	reader := strings.NewReader("test data")
	err := sm.AttachReader(reader)
	if err != nil {
		t.Fatalf("AttachReader failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	_, err = sm.ClientConnected("1", tw, CwndSize, 0)
	if err != nil {
		t.Fatalf("ClientConnected failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	packets := tw.GetPackets()
	if len(packets) == 0 {
		t.Fatal("Expected cirbuf drain after connect")
	}

	allData := ""
	for _, pkt := range packets {
		if pkt.Data64 != "" {
			allData += decodeData(pkt.Data64)
		}
	}

	if allData != "test data" {
		t.Errorf("Expected 'test data', got '%s'", allData)
	}

	sm.Close()
}

func TestConnectedToDisconnectedTransition(t *testing.T) {
	tw := &testWriter{}
	sm := MakeStreamManager()

	reader := &slowReader{data: []byte("slow data"), delay: 50 * time.Millisecond}
	err := sm.AttachReader(reader)
	if err != nil {
		t.Fatalf("AttachReader failed: %v", err)
	}

	_, err = sm.ClientConnected("1", tw, CwndSize, 0)
	if err != nil {
		t.Fatalf("ClientConnected failed: %v", err)
	}

	time.Sleep(150 * time.Millisecond)

	sm.ClientDisconnected()

	time.Sleep(100 * time.Millisecond)

	sm.Close()
}

func TestFlowControl(t *testing.T) {
	cwndSize := 1024
	tw := &testWriter{}
	sm := MakeStreamManagerWithSizes(cwndSize, 8*1024)

	largeData := strings.Repeat("x", cwndSize+500)
	reader := strings.NewReader(largeData)

	err := sm.AttachReader(reader)
	if err != nil {
		t.Fatalf("AttachReader failed: %v", err)
	}

	_, err = sm.ClientConnected("1", tw, cwndSize, 0)
	if err != nil {
		t.Fatalf("ClientConnected failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	packets := tw.GetPackets()
	totalData := 0
	for _, pkt := range packets {
		if pkt.Data64 != "" {
			decoded, _ := base64.StdEncoding.DecodeString(pkt.Data64)
			totalData += len(decoded)
		}
	}

	if totalData > cwndSize {
		t.Errorf("Sent %d bytes without ACK, exceeds cwnd size %d", totalData, cwndSize)
	}

	sm.RecvAck(wshrpc.CommandStreamAckData{Id: "1", Seq: int64(totalData), RWnd: int64(cwndSize)})

	time.Sleep(100 * time.Millisecond)

	sm.Close()
}

func TestSequenceNumbering(t *testing.T) {
	tw := &testWriter{}
	sm := MakeStreamManager()

	reader := strings.NewReader("abcdefghij")
	err := sm.AttachReader(reader)
	if err != nil {
		t.Fatalf("AttachReader failed: %v", err)
	}

	_, err = sm.ClientConnected("1", tw, CwndSize, 0)
	if err != nil {
		t.Fatalf("ClientConnected failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	packets := tw.GetPackets()
	if len(packets) == 0 {
		t.Fatal("Expected packets")
	}

	expectedSeq := int64(0)
	for _, pkt := range packets {
		if pkt.Data64 == "" {
			continue
		}

		if pkt.Seq != expectedSeq {
			t.Errorf("Expected seq %d, got %d", expectedSeq, pkt.Seq)
		}

		decoded, _ := base64.StdEncoding.DecodeString(pkt.Data64)
		expectedSeq += int64(len(decoded))
	}

	sm.Close()
}

func TestTerminalEventOrdering(t *testing.T) {
	tw := &testWriter{}
	sm := MakeStreamManager()

	reader := strings.NewReader("data")
	err := sm.AttachReader(reader)
	if err != nil {
		t.Fatalf("AttachReader failed: %v", err)
	}

	_, err = sm.ClientConnected("1", tw, CwndSize, 0)
	if err != nil {
		t.Fatalf("ClientConnected failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	packets := tw.GetPackets()
	if len(packets) == 0 {
		t.Fatal("Expected data packets")
	}

	hasData := false
	hasEof := false
	eofSeq := int64(-1)

	for _, pkt := range packets {
		if pkt.Data64 != "" {
			hasData = true
		}
		if pkt.Eof {
			hasEof = true
			eofSeq = pkt.Seq
		}
	}

	if !hasData {
		t.Error("Expected data packet")
	}

	if hasEof {
		t.Error("Should not have EOF before ACK")
	}

	sm.RecvAck(wshrpc.CommandStreamAckData{Id: "1", Seq: 4, RWnd: CwndSize})

	time.Sleep(50 * time.Millisecond)

	packets = tw.GetPackets()
	hasEof = false
	for _, pkt := range packets {
		if pkt.Eof {
			hasEof = true
			eofSeq = pkt.Seq
		}
	}

	if !hasEof {
		t.Error("Expected EOF after ACKing all data")
	}

	if eofSeq != 4 {
		t.Errorf("Expected EOF at seq 4, got %d", eofSeq)
	}

	sm.Close()
}

type slowReader struct {
	data  []byte
	pos   int
	delay time.Duration
}

func (sr *slowReader) Read(p []byte) (n int, err error) {
	if sr.pos >= len(sr.data) {
		return 0, io.EOF
	}

	time.Sleep(sr.delay)

	n = copy(p, sr.data[sr.pos:])
	sr.pos += n

	return n, nil
}

// a packet (or its ack) lost on the fire-and-forget link must not deadlock
// the stream: with no ack progress the manager retransmits the unacked window
func TestRetransmitAfterLostPacket(t *testing.T) {
	tw := &testWriter{}
	sm := MakeStreamManagerWithSizes(CwndSize, CirBufSize)
	defer sm.Close()
	sm.lock.Lock()
	sm.retransmitTimeout = 50 * time.Millisecond
	sm.lock.Unlock()

	pr, pw := io.Pipe()
	if err := sm.AttachReader(pr); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if _, err := sm.ClientConnected("s1", tw, CwndSize, 0); err != nil {
		t.Fatalf("connect: %v", err)
	}

	if _, err := pw.Write([]byte("hello world")); err != nil {
		t.Fatalf("write: %v", err)
	}

	// first transmission arrives; pretend it was LOST (never ack it)
	deadline := time.Now().Add(2 * time.Second)
	for len(tw.GetPackets()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	first := tw.GetPackets()
	if len(first) == 0 {
		t.Fatalf("no initial packet sent")
	}
	if decodeData(first[0].Data64) != "hello world" {
		t.Fatalf("unexpected first packet: %q", decodeData(first[0].Data64))
	}
	tw.Clear()

	// with no ack progress, the window must be retransmitted from seq 0
	deadline = time.Now().Add(2 * time.Second)
	for len(tw.GetPackets()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	resent := tw.GetPackets()
	if len(resent) == 0 {
		t.Fatalf("packet was not retransmitted after ack stall")
	}
	if resent[0].Seq != 0 || decodeData(resent[0].Data64) != "hello world" {
		t.Fatalf("bad retransmission: seq=%d data=%q", resent[0].Seq, decodeData(resent[0].Data64))
	}

	// acking the retransmission unsticks the stream: new data flows again
	sm.RecvAck(wshrpc.CommandStreamAckData{Id: "s1", Seq: 11, RWnd: CwndSize})
	tw.Clear()
	if _, err := pw.Write([]byte("more")); err != nil {
		t.Fatalf("write2: %v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for len(tw.GetPackets()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	after := tw.GetPackets()
	if len(after) == 0 || after[0].Seq != 11 || decodeData(after[0].Data64) != "more" {
		t.Fatalf("stream did not resume after retransmit+ack: %+v", after)
	}
}

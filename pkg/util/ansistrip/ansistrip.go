// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

// Package ansistrip provides a streaming ANSI-escape-stripping line scanner
// for raw VT byte streams. Feed parts in order and receive completed logical
// lines with their raw byte offsets; escape sequences and UTF-8 runes split
// across part boundaries are handled by the persistent state machine.
package ansistrip

import "bytes"

const MaxLineLen = 4096

const (
	stGround = iota
	stEsc
	stCSI
	stOSC
	stOSCEsc
	stString
	stStringEsc
)

// Line is one ANSI-stripped logical line of a raw VT byte stream.
type Line struct {
	// Offset is the raw-stream byte offset of the line's first raw byte
	// (including any leading escape sequences).
	Offset int64
	// Line is the number of raw newline bytes before Offset -- the line number
	// in raw-\n space, matching indexes built with bytes.Count(data, "\n").
	// lone-\r boundaries split scanner lines without advancing it.
	Line int64
	// Text is the stripped text, capped at MaxLineLen bytes.
	Text string
}

type LineScanner struct {
	state     int
	lineBuf   []byte
	lineStart int64
	lineNl    int64
	nextOff   int64
	nlTotal   int64
	dropped   bool // lineBuf hit MaxLineLen; keep scanning, stop appending
	sawCR     bool // last ground byte was \r (a following \n is the same boundary)
}

func NewLineScanner(startOffset int64) *LineScanner {
	return &LineScanner{
		lineStart: startOffset,
		nextOff:   startOffset,
	}
}

// NewlineCount returns the number of raw newline bytes seen so far (in any
// state, matching raw bytes.Count semantics).
func (s *LineScanner) NewlineCount() int64 {
	return s.nlTotal
}

// Feed consumes the next raw part and returns the lines completed within it.
func (s *LineScanner) Feed(part []byte) []Line {
	var lines []Line
	for _, b := range part {
		off := s.nextOff
		s.nextOff++
		if b == '\n' {
			s.nlTotal++
		}
		switch s.state {
		case stEsc:
			switch b {
			case '[':
				s.state = stCSI
			case ']':
				s.state = stOSC
			case 'P', 'X', '^', '_':
				s.state = stString
			default:
				// two-byte escape (ESC c, ESC =, charset selection, etc)
				s.state = stGround
			}
			continue
		case stCSI:
			// consume until the final byte
			if b >= 0x40 && b <= 0x7e {
				s.state = stGround
			}
			continue
		case stOSC:
			if b == 0x07 {
				s.state = stGround
			} else if b == 0x1b {
				s.state = stOSCEsc
			}
			continue
		case stOSCEsc:
			// ESC \ (ST) ends the OSC; anything else returns to consuming it
			if b == '\\' {
				s.state = stGround
			} else {
				s.state = stOSC
			}
			continue
		case stString:
			// DCS/SOS/PM/APC consume until ST
			if b == 0x1b {
				s.state = stStringEsc
			}
			continue
		case stStringEsc:
			if b == '\\' {
				s.state = stGround
			} else {
				s.state = stString
			}
			continue
		}
		// ground state
		switch {
		case b == 0x1b:
			s.state = stEsc
			s.sawCR = false
		case b == '\n':
			if s.sawCR {
				// the \r already ended this line; the \n is the same boundary
				s.sawCR = false
				s.lineStart = off + 1
				s.lineNl = s.nlTotal
				continue
			}
			lines = append(lines, s.emit())
			s.lineStart = off + 1
			s.lineNl = s.nlTotal
		case b == '\r':
			lines = append(lines, s.emit())
			s.sawCR = true
			s.lineStart = off + 1
			s.lineNl = s.nlTotal
		case b == '\t' || (b >= 0x20 && b != 0x7f):
			s.sawCR = false
			if len(s.lineBuf) < MaxLineLen {
				s.lineBuf = append(s.lineBuf, b)
			} else {
				s.dropped = true
			}
		default:
			// other C0 controls (BS, BEL, VT, FF, SO, SI, ...) and DEL are dropped
			s.sawCR = false
		}
	}
	return lines
}

// Flush returns the final partial line, or nil if it is empty.
func (s *LineScanner) Flush() *Line {
	if len(s.lineBuf) == 0 {
		return nil
	}
	line := s.emit()
	s.lineStart = s.nextOff
	s.lineNl = s.nlTotal
	return &line
}

func (s *LineScanner) emit() Line {
	line := Line{Offset: s.lineStart, Line: s.lineNl, Text: string(bytes.ToValidUTF8(s.lineBuf, nil))}
	s.lineBuf = s.lineBuf[:0]
	s.dropped = false
	return line
}

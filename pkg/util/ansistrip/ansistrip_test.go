// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package ansistrip

import (
	"strings"
	"testing"
)

func collect(scanner *LineScanner, parts ...string) []Line {
	var lines []Line
	for _, part := range parts {
		lines = append(lines, scanner.Feed([]byte(part))...)
	}
	if last := scanner.Flush(); last != nil {
		lines = append(lines, *last)
	}
	return lines
}

func assertLines(t *testing.T, lines []Line, expected ...Line) {
	t.Helper()
	if len(lines) != len(expected) {
		t.Fatalf("expected %d lines, got %d: %+v", len(expected), len(lines), lines)
	}
	for i, exp := range expected {
		if lines[i].Offset != exp.Offset || lines[i].Text != exp.Text {
			t.Errorf("line %d: expected %+v, got %+v", i, exp, lines[i])
		}
	}
}

func assertLineNumbers(t *testing.T, lines []Line, expected ...int64) {
	t.Helper()
	if len(lines) != len(expected) {
		t.Fatalf("expected %d lines, got %d: %+v", len(expected), len(lines), lines)
	}
	for i, exp := range expected {
		if lines[i].Line != exp {
			t.Errorf("line %d: expected line number %d, got %d", i, exp, lines[i].Line)
		}
	}
}

func TestLineNumbersRawNewlineSpace(t *testing.T) {
	// lone-\r boundaries split scanner lines WITHOUT advancing the raw-\n
	// line number (they rewrite the same visual line)
	lines := collect(NewLineScanner(0), "a\n10%\r20%\r30%\nb\r\nc\n")
	assertLineNumbers(t, lines, 0, 1, 1, 1, 2, 3)
}

func TestPlainLines(t *testing.T) {
	lines := collect(NewLineScanner(0), "hello\nworld\npartial")
	assertLines(t, lines,
		Line{Offset: 0, Text: "hello"},
		Line{Offset: 6, Text: "world"},
		Line{Offset: 12, Text: "partial"},
	)
}

func TestStartOffset(t *testing.T) {
	lines := collect(NewLineScanner(1000), "ab\ncd\n")
	assertLines(t, lines,
		Line{Offset: 1000, Text: "ab"},
		Line{Offset: 1003, Text: "cd"},
	)
}

func TestSGRStripped(t *testing.T) {
	lines := collect(NewLineScanner(0), "\x1b[31mred\x1b[0m text\n")
	assertLines(t, lines, Line{Offset: 0, Text: "red text"})
}

func TestSGRSplitAcrossFeeds(t *testing.T) {
	lines := collect(NewLineScanner(0), "\x1b[3", "1mred\x1b", "[0m\n")
	assertLines(t, lines, Line{Offset: 0, Text: "red"})
}

func TestOSCTerminatedByBEL(t *testing.T) {
	lines := collect(NewLineScanner(0), "\x1b]0;title\x07visible\n")
	assertLines(t, lines, Line{Offset: 0, Text: "visible"})
}

func TestOSCTerminatedByST(t *testing.T) {
	lines := collect(NewLineScanner(0), "\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\\n")
	assertLines(t, lines, Line{Offset: 0, Text: "link"})
}

func TestOSCSplitAcrossFeeds(t *testing.T) {
	lines := collect(NewLineScanner(0), "\x1b]0;ti", "tle\x07ok\n")
	assertLines(t, lines, Line{Offset: 0, Text: "ok"})
}

func TestDCSStripped(t *testing.T) {
	lines := collect(NewLineScanner(0), "\x1bPq#0;stuff\x1b\\after\n")
	assertLines(t, lines, Line{Offset: 0, Text: "after"})
}

func TestCRLFIsOneBoundary(t *testing.T) {
	lines := collect(NewLineScanner(0), "one\r\ntwo\r\n")
	assertLines(t, lines,
		Line{Offset: 0, Text: "one"},
		Line{Offset: 5, Text: "two"},
	)
}

func TestLoneCREndsLine(t *testing.T) {
	// progress redraws: each \r yields a separate short line, no concatenation
	lines := collect(NewLineScanner(0), "10%\r20%\r30%\n")
	assertLines(t, lines,
		Line{Offset: 0, Text: "10%"},
		Line{Offset: 4, Text: "20%"},
		Line{Offset: 8, Text: "30%"},
	)
}

func TestCRLFSplitAcrossFeeds(t *testing.T) {
	lines := collect(NewLineScanner(0), "one\r", "\ntwo\n")
	assertLines(t, lines,
		Line{Offset: 0, Text: "one"},
		Line{Offset: 5, Text: "two"},
	)
}

func TestUTF8SplitAcrossFeeds(t *testing.T) {
	umlaut := "ü" // 2 bytes
	lines := collect(NewLineScanner(0), "gr"+string([]byte{umlaut[0]}), string([]byte{umlaut[1]})+"n\n")
	assertLines(t, lines, Line{Offset: 0, Text: "grün"})
}

func TestControlCharsDropped(t *testing.T) {
	lines := collect(NewLineScanner(0), "a\x08b\x07c\td\n")
	assertLines(t, lines, Line{Offset: 0, Text: "abc\td"})
}

func TestLineCap(t *testing.T) {
	long := strings.Repeat("x", MaxLineLen+100)
	lines := collect(NewLineScanner(0), long+"\nnext\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if len(lines[0].Text) != MaxLineLen {
		t.Errorf("expected capped line of %d, got %d", MaxLineLen, len(lines[0].Text))
	}
	if lines[1].Text != "next" || lines[1].Offset != int64(len(long)+1) {
		t.Errorf("offset accounting broken after cap: %+v", lines[1])
	}
}

func TestEmptyLinesKeepOffsets(t *testing.T) {
	lines := collect(NewLineScanner(0), "a\n\n\nb\n")
	assertLines(t, lines,
		Line{Offset: 0, Text: "a"},
		Line{Offset: 2, Text: ""},
		Line{Offset: 3, Text: ""},
		Line{Offset: 4, Text: "b"},
	)
}

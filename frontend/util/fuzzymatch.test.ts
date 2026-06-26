// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";

import { fuzzyMatch } from "./fuzzymatch";

describe("fuzzyMatch", () => {
    it("matches an empty query against anything", () => {
        const res = fuzzyMatch("", "anything");
        expect(res.matched).toBe(true);
        expect(res.positions).toEqual([]);
    });

    it("matches a contiguous substring and reports its positions", () => {
        const res = fuzzyMatch("bar", "foobar");
        expect(res.matched).toBe(true);
        expect(res.positions).toEqual([3, 4, 5]);
    });

    it("matches a non-contiguous subsequence in order", () => {
        const res = fuzzyMatch("fb", "foobar");
        expect(res.matched).toBe(true);
        expect(res.positions).toEqual([0, 3]);
    });

    it("is case-insensitive", () => {
        const res = fuzzyMatch("BR", "browser");
        expect(res.matched).toBe(true);
    });

    it("fails when the characters are out of order", () => {
        const res = fuzzyMatch("rab", "foobar");
        expect(res.matched).toBe(false);
        expect(res.positions).toEqual([]);
    });

    it("fails when a query character is missing", () => {
        const res = fuzzyMatch("xyz", "foobar");
        expect(res.matched).toBe(false);
    });

    it("scores a start-of-string match higher than the same text mid-string", () => {
        const atStart = fuzzyMatch("term", "terminal");
        const later = fuzzyMatch("term", "xxterminal");
        expect(atStart.matched).toBe(true);
        expect(later.matched).toBe(true);
        expect(atStart.score).toBeGreaterThan(later.score);
    });

    it("picks word-boundary positions for highlighting over an earlier greedy occurrence", () => {
        // "log" should align with the "logs" word, not the 'l' inside "build"
        const res = fuzzyMatch("log", "build logs");
        expect(res.positions).toEqual([6, 7, 8]);
    });

    it("ranks a word-boundary match above a mid-word match", () => {
        // "log" at the start of the "logs" word should beat "log" buried inside "catalog"
        const boundary = fuzzyMatch("log", "build logs");
        const midword = fuzzyMatch("log", "catalog");
        expect(boundary.matched).toBe(true);
        expect(midword.matched).toBe(true);
        expect(boundary.score).toBeGreaterThan(midword.score);
    });

    it("ranks a tighter (more contiguous) match above a scattered one", () => {
        const tight = fuzzyMatch("abc", "abc");
        const scattered = fuzzyMatch("abc", "a-b-c-x-y-z");
        expect(tight.score).toBeGreaterThan(scattered.score);
    });
});

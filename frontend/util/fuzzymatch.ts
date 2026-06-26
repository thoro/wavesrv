// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

export type FuzzyMatchResult = {
    matched: boolean;
    score: number;
    positions: number[];
};

// Sublime-style subsequence fuzzy match. Returns whether every character of `query`
// appears in order within `target` (case-insensitive), a score (higher is a better
// match), and the matched character indices in `target` for highlighting.
export function fuzzyMatch(query: string, target: string): FuzzyMatchResult {
    if (!query) {
        return { matched: true, score: 0, positions: [] };
    }
    const q = query.toLowerCase();
    const t = target.toLowerCase();
    const positions: number[] = [];
    let score = 0;
    let qi = 0;
    let prevMatchIdx = -1;
    for (let ti = 0; ti < t.length && qi < q.length; ti++) {
        if (t[ti] !== q[qi]) {
            continue;
        }
        let bonus = 1;
        if (prevMatchIdx === ti - 1) {
            bonus += 5; // contiguous run of matched chars
        }
        if (ti === 0) {
            bonus += 10; // matches the very start of the string
        } else {
            const prevChar = target[ti - 1];
            const curChar = target[ti];
            if (prevChar === " " || prevChar === "-" || prevChar === "_" || prevChar === "/" || prevChar === ".") {
                bonus += 8; // start of a word
            } else if (
                prevChar === prevChar.toLowerCase() &&
                curChar === curChar.toUpperCase() &&
                curChar !== curChar.toLowerCase()
            ) {
                bonus += 6; // camelCase boundary
            }
        }
        score += bonus;
        prevMatchIdx = ti;
        positions.push(ti);
        qi++;
    }
    if (qi < q.length) {
        return { matched: false, score: 0, positions: [] };
    }
    // tighter matches (less filler between the matched chars) rank higher
    score -= Math.max(0, target.length - q.length) * 0.1;
    return { matched: true, score, positions };
}

// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

export type FuzzyMatchResult = {
    matched: boolean;
    score: number;
    positions: number[];
};

const MatchScore = 16;
const ConsecutiveBonus = 10;
const GapPenalty = 1;
const StartBonus = 12;
const WordBoundaryBonus = 9;
const CamelBonus = 7;
const NegInf = -1e9;

// Per-target-position bonus, independent of the query: rewards matching at the start of the
// string, at the start of a word (after a separator), or at a camelCase boundary.
function positionBonus(target: string): number[] {
    const bonus = new Array<number>(target.length);
    for (let j = 0; j < target.length; j++) {
        if (j === 0) {
            bonus[j] = StartBonus;
            continue;
        }
        const prev = target[j - 1];
        const cur = target[j];
        if (prev === " " || prev === "-" || prev === "_" || prev === "/" || prev === ".") {
            bonus[j] = WordBoundaryBonus;
        } else if (
            prev === prev.toLowerCase() &&
            cur === cur.toUpperCase() &&
            cur !== cur.toLowerCase()
        ) {
            bonus[j] = CamelBonus;
        } else {
            bonus[j] = 0;
        }
    }
    return bonus;
}

// Sublime-style fuzzy match. Returns whether every character of `query` appears in order
// within `target` (case-insensitive), a score (higher is a better match), and the matched
// character indices in `target` for highlighting. Uses an optimal-alignment DP so the chosen
// match positions favor word boundaries and contiguous runs rather than the first greedy
// occurrence.
export function fuzzyMatch(query: string, target: string): FuzzyMatchResult {
    if (!query) {
        return { matched: true, score: 0, positions: [] };
    }
    const q = query.toLowerCase();
    const t = target.toLowerCase();
    const m = q.length;
    const n = t.length;
    if (m > n) {
        return { matched: false, score: 0, positions: [] };
    }
    const posBonus = positionBonus(target);

    // score[i][j] = best score where q[i] is matched at t[j]; back[i][j] = the target index
    // q[i-1] was matched at along that best path (-1 for the first query char).
    const score: number[][] = Array.from({ length: m }, () => new Array<number>(n).fill(NegInf));
    const back: number[][] = Array.from({ length: m }, () => new Array<number>(n).fill(-1));

    for (let i = 0; i < m; i++) {
        let bestPrev = NegInf; // max score[i-1][k] for k < j
        let bestPrevK = -1;
        for (let j = i; j < n; j++) {
            if (i > 0) {
                const prevAtJMinus1 = score[i - 1][j - 1];
                if (prevAtJMinus1 > bestPrev) {
                    bestPrev = prevAtJMinus1;
                    bestPrevK = j - 1;
                }
            }
            if (q[i] !== t[j]) {
                continue;
            }
            if (i === 0) {
                score[i][j] = MatchScore + posBonus[j];
                back[i][j] = -1;
                continue;
            }
            const consecutive =
                score[i - 1][j - 1] > NegInf
                    ? score[i - 1][j - 1] + MatchScore + posBonus[j] + ConsecutiveBonus
                    : NegInf;
            const skipped = bestPrev > NegInf ? bestPrev + MatchScore + posBonus[j] - GapPenalty : NegInf;
            if (consecutive >= skipped && consecutive > NegInf) {
                score[i][j] = consecutive;
                back[i][j] = j - 1;
            } else if (skipped > NegInf) {
                score[i][j] = skipped;
                back[i][j] = bestPrevK;
            }
        }
    }

    let bestJ = -1;
    let bestScore = NegInf;
    for (let j = m - 1; j < n; j++) {
        if (score[m - 1][j] > bestScore) {
            bestScore = score[m - 1][j];
            bestJ = j;
        }
    }
    if (bestJ === -1 || bestScore <= NegInf) {
        return { matched: false, score: 0, positions: [] };
    }

    const positions: number[] = [];
    let i = m - 1;
    let j = bestJ;
    while (i >= 0 && j >= 0) {
        positions.push(j);
        j = back[i][j];
        i--;
    }
    positions.reverse();
    return { matched: true, score: bestScore, positions };
}

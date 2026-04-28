#!/usr/bin/env python3
"""Patch upstream promql-test-queries.yml for Prometheus 3.x.

The upstream compliance tester corpus was last updated for Prom 2.26. A
handful of `should_fail: true` entries no longer fail under Prom 3.x
(UTF-8 label-name support, relaxed duplicate-output-label handling). The
tester hard-aborts at comparer/comparer.go:95 the moment such a query
succeeds against the reference, so we can't just let it run.

This script produces a patched copy of the corpus by dropping the
`should_fail: true` marker from entries whose queries are now consistently
accepted by the reference Prometheus. It also removes entries whose result
changed from syntactic validation to fixture-dependent duplicate-label
runtime behavior, because the upstream tester hard-aborts whether its
`should_fail` expectation is wrong in either direction.

Operates line-by-line to preserve upstream byte layout (comments,
quoting, blank lines) everywhere except the deleted marker lines.
"""

import argparse
import re
import sys

# Queries whose `should_fail: true` marker must be dropped under Prom 3.x.
# Keep in sync with `curl` checks against the reference target after any
# Prometheus bump. See scripts/run-compliance.sh for how this is applied.
QUERIES_NOW_ACCEPTED = {
    # Prom 3.x accepts UTF-8 label names — "~invalid" is now a valid label
    # name, so label_replace/label_join succeed instead of erroring out.
    'label_replace(demo_num_cpus, "~invalid", "", "src", "(.*)")',
    'label_join(demo_num_cpus, "~invalid", "-", "instance")',
}

# This query's Prom 3.x outcome depends on whether the selected fixture data
# produces duplicate output label sets after label mutation. Keep the rest of
# the corpus stable by removing it from the generated Prom 3.x corpus.
QUERIES_REMOVED = {
    'label_replace(demo_num_cpus, "instance", "", "", "")',
}

QUERY_RE = re.compile(r"^\s*-\s+query:\s+(.*)$")
FLAG_RE = re.compile(r"^\s*should_fail:\s*true\s*$")


def unquote(val: str) -> str:
    v = val.strip()
    if len(v) >= 2 and v[0] == v[-1] == "'":
        return v[1:-1].replace("''", "'")
    if len(v) >= 2 and v[0] == v[-1] == '"':
        return v[1:-1]
    return v


def patch(lines):
    out = []
    dropped = []
    removed = []
    i = 0
    while i < len(lines):
        line = lines[i]
        m = QUERY_RE.match(line)
        if not m:
            out.append(line)
            i += 1
            continue
        qtext = unquote(m.group(1))
        if qtext in QUERIES_REMOVED:
            if out and "duplicated identical output label sets" in out[-1]:
                out.pop()
            removed.append((i + 1, qtext))
            i += 1
            while i < len(lines) and not QUERY_RE.match(lines[i]):
                i += 1
            continue
        out.append(line)
        i += 1
        while i < len(lines) and not QUERY_RE.match(lines[i]):
            if qtext in QUERIES_NOW_ACCEPTED and FLAG_RE.match(lines[i]):
                dropped.append((i + 1, qtext))
                i += 1
                continue
            out.append(lines[i])
            i += 1
    return out, dropped, removed


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument("--input", required=True, help="Path to upstream promql-test-queries.yml")
    p.add_argument("--output", required=True, help="Path to write patched YAML")
    args = p.parse_args()

    with open(args.input, "r", encoding="utf-8") as fh:
        lines = fh.readlines()

    patched, dropped, removed = patch(lines)

    with open(args.output, "w", encoding="utf-8") as fh:
        fh.writelines(patched)

    print(f"Patched {args.input} -> {args.output}", file=sys.stderr)
    print(f"Dropped should_fail marker from {len(dropped)} entries:", file=sys.stderr)
    for ln, q in dropped:
        print(f"  line {ln}: {q}", file=sys.stderr)
    print(f"Removed {len(removed)} fixture-dependent entries:", file=sys.stderr)
    for ln, q in removed:
        print(f"  line {ln}: {q}", file=sys.stderr)

    expected_dropped = len(QUERIES_NOW_ACCEPTED)
    expected_removed = len(QUERIES_REMOVED)
    if len(dropped) != expected_dropped or len(removed) != expected_removed:
        print(
            f"warning: expected to drop {expected_dropped} markers and remove {expected_removed} entries; "
            f"dropped {len(dropped)}, removed {len(removed)}. "
            "Upstream corpus may have changed — review QUERIES_NOW_ACCEPTED and QUERIES_REMOVED.",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""Runner for the #492 CJK bigram build-cost re-measurement.

Matrix: arm {ja, en} x every binary passed with --bin, interleaved one round
at a time (bin1-ja, bin2-ja, ..., bin1-en, ..., repeat) so a machine-wide
slowdown mid-run does not land entirely on one cell. Each cell measurement:

  1. delete the index dir for that (arm, bin) cell
  2. run `<bin> index --rebuild` with the cell's env (isolated store,
     isolated index dir, isolated fake HOME so no other harness on this
     machine's real ~/.claude or ~/.codex gets touched or picked up)
  3. record wall time (time.perf_counter) and the rebuilt index dir's total
     size in bytes

Output: results.csv (round,arm,bin,seconds,index_bytes) and
results-summary.txt (min/median per arm x bin cell, then the cost the first
binary carries and each later one does not).

This script only measures; it does not build binaries or generate the corpus (gen_corpus.py does that).
"""
import argparse
import csv
import os
import shutil
import statistics
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))


def dir_size(path: str) -> int:
    if not os.path.exists(path):
        return 0
    if os.path.isfile(path):
        return os.path.getsize(path)
    total = 0
    for root, _dirs, files in os.walk(path):
        for name in files:
            fp = os.path.join(root, name)
            try:
                total += os.path.getsize(fp)
            except OSError:
                pass
    return total


def cell_env(fakehome: str, index_dir: str, claude_root: str) -> dict:
    env = dict(os.environ)
    for k in ("HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "CODEX_HOME"):
        env[k] = fakehome
    env["DEJA_INDEX_DIR"] = index_dir
    env["DEJA_CLAUDE_ROOT"] = claude_root
    env["GOMAXPROCS"] = "2"
    # Keep this benchmark to the claude source only — no other harness
    # should be discoverable under the fake HOME anyway, but DEJA_SOURCE
    # scoping (if the binary reads it) is one more belt for the same buckle.
    return env


def parse_bins(specs, head_bin: str, stub_bin: str):
    """Resolve --bin LABEL=PATH (repeatable) into an ordered [(label, path)].

    --head-bin/--stub-bin are kept as shorthand for the two-binary case this
    script started with, so callers written against it keep working. Order is
    the order given: the first binary is the one the summary subtracts from.
    """
    pairs = list(specs)
    if head_bin:
        pairs.insert(0, f"head={head_bin}")
    if stub_bin:
        pairs.append(f"stub={stub_bin}")
    if not pairs:
        raise SystemExit("run_bench: pass at least one --bin LABEL=PATH (or --head-bin/--stub-bin)")

    out = []
    seen = set()
    for spec in pairs:
        label, sep, path = spec.partition("=")
        if not sep or not label or not path:
            raise SystemExit(f"run_bench: --bin wants LABEL=PATH, got {spec!r}")
        if any(c in label for c in "/\\ ." + os.sep):
            raise SystemExit(f"run_bench: label {label!r} must be a bare word — it names index dirs and log files")
        if label in seen:
            raise SystemExit(f"run_bench: duplicate --bin label {label!r}")
        seen.add(label)
        path = os.path.abspath(path)
        if not os.path.isfile(path):
            raise SystemExit(f"run_bench: binary not found: {path}")
        out.append((label, path))
    return out


def run_cell(binary: str, arm: str, bin_label: str, out_root: str, fakehome: str, log_dir: str, round_no: int):
    index_dir = os.path.join(out_root, f"idx-{arm}-{bin_label}")
    claude_root = os.path.join(out_root, f"store-{arm}", "claude-root")
    if not os.path.isdir(claude_root):
        raise SystemExit(f"run_bench: missing corpus dir {claude_root} — run gen_corpus.py first")

    if os.path.isdir(index_dir):
        shutil.rmtree(index_dir)
    elif os.path.exists(index_dir):
        os.remove(index_dir)

    env = cell_env(fakehome, index_dir, claude_root)
    log_path = os.path.join(log_dir, f"round{round_no:03d}-{bin_label}-{arm}.log")

    t0 = time.perf_counter()
    with open(log_path, "w", encoding="utf-8") as logf:
        proc = subprocess.run(
            [binary, "index", "--rebuild"],
            env=env,
            stdout=logf,
            stderr=subprocess.STDOUT,
        )
    elapsed = time.perf_counter() - t0

    if proc.returncode != 0:
        raise SystemExit(
            f"run_bench: {binary} index --rebuild failed (rc={proc.returncode}) for {bin_label}/{arm} "
            f"round {round_no} — see {log_path}"
        )

    size = dir_size(index_dir)
    return elapsed, size


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--rounds", type=int, default=7)
    ap.add_argument("--out-root", default=HERE)
    ap.add_argument(
        "--bin",
        action="append",
        default=[],
        metavar="LABEL=PATH",
        help="a deja binary to measure, repeatable; the first one is the baseline",
    )
    ap.add_argument("--head-bin", help="shorthand for --bin head=PATH")
    ap.add_argument("--stub-bin", help="shorthand for --bin stub=PATH")
    ap.add_argument("--arms", default="ja,en")
    args = ap.parse_args()

    out_root = os.path.abspath(args.out_root)
    arms = args.arms.split(",")
    binaries = parse_bins(args.bin, args.head_bin, args.stub_bin)

    fakehome = os.path.join(out_root, "fakehome")
    os.makedirs(fakehome, exist_ok=True)
    log_dir = os.path.join(out_root, "logs")
    os.makedirs(log_dir, exist_ok=True)

    rows = []  # (round, arm, bin, seconds, index_bytes)
    csv_path = os.path.join(out_root, "results.csv")
    with open(csv_path, "w", newline="", encoding="utf-8") as csvf:
        writer = csv.writer(csvf)
        writer.writerow(["round", "arm", "bin", "seconds", "index_bytes"])
        for round_no in range(1, args.rounds + 1):
            for bin_label, binpath in binaries:
                for arm in arms:
                    elapsed, size = run_cell(binpath, arm, bin_label, out_root, fakehome, log_dir, round_no)
                    print(f"round {round_no} {bin_label:4s} {arm:2s}  {elapsed:8.3f}s  {size:>12} bytes", flush=True)
                    rows.append((round_no, arm, bin_label, elapsed, size))
                    writer.writerow([round_no, arm, bin_label, f"{elapsed:.6f}", size])
                    csvf.flush()

    summary_path = os.path.join(out_root, "results-summary.txt")
    with open(summary_path, "w", encoding="utf-8") as sf:
        sf.write(f"rounds={args.rounds} arms={arms}\n\n")
        groups = {}
        for round_no, arm, bin_label, elapsed, size in rows:
            groups.setdefault((arm, bin_label), []).append((elapsed, size))
        for arm in arms:
            for bin_label, _ in binaries:
                key = (arm, bin_label)
                vals = groups.get(key, [])
                if not vals:
                    continue
                secs = [v[0] for v in vals]
                sizes = [v[1] for v in vals]
                sf.write(
                    f"{arm:2s} {bin_label:4s}  n={len(vals)}  "
                    f"time min={min(secs):.3f}s median={statistics.median(secs):.3f}s max={max(secs):.3f}s  "
                    f"size min={min(sizes)} median={statistics.median(sizes):.0f} max={max(sizes)}\n"
                )
        base_label = binaries[0][0]
        for other_label, _ in binaries[1:]:
            sf.write(f"\ncost carried by {base_label} and not by {other_label}, by median:\n")
            for arm in arms:
                b = groups.get((arm, base_label))
                o = groups.get((arm, other_label))
                if not b or not o:
                    continue
                b_t = statistics.median(v[0] for v in b)
                o_t = statistics.median(v[0] for v in o)
                b_sz = statistics.median(v[1] for v in b)
                o_sz = statistics.median(v[1] for v in o)
                sf.write(
                    f"  {arm:2s}: time {b_t:.3f}s - {o_t:.3f}s = {b_t - o_t:+.3f}s   "
                    f"size {b_sz:.0f} - {o_sz:.0f} = {b_sz - o_sz:+.0f} bytes\n"
                )

    print(f"wrote {csv_path}")
    print(f"wrote {summary_path}")


if __name__ == "__main__":
    sys.exit(main())

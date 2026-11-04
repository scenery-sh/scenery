#!/usr/bin/env python3
"""Compare JSON paths in an exported baseline and candidate with paired samples.

Run only for an explicitly requested performance measurement. Both sources must
use the same Go toolchain. The baseline must be a disposable git archive; this
script copies identical benchmark definitions into it, never production code.
"""

import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import random
import re
import statistics
import subprocess


def run(command, cwd, env):
    return subprocess.check_output(command, cwd=cwd, env=env, text=True, stderr=subprocess.STDOUT)


def source_digest(root):
    digest = hashlib.sha256()
    files = [root / "go.mod", root / "go.sum"]
    for owner in ("internal/contract", "internal/machine", "internal/appconfig"):
        files.extend(p for p in (root / owner).glob("*.go") if not p.name.endswith("_test.go"))
    for path in sorted(files):
        digest.update(str(path.relative_to(root)).encode() + b"\0" + path.read_bytes() + b"\0")
    return digest.hexdigest()


def ratio_interval(before, after):
    ratios = [math.log(b / a) for a, b in zip(before, after)]
    rng = random.Random(0)
    estimates = sorted(math.exp(statistics.median(rng.choices(ratios, k=len(ratios)))) for _ in range(4000))
    return [estimates[100], estimates[3899]]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--baseline", type=Path, required=True)
    parser.add_argument("--candidate", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--pairs", type=int, default=8)
    parser.add_argument("--benchtime", default="200ms")
    args = parser.parse_args()
    roots = {"baseline": args.baseline.resolve(), "candidate": args.candidate.resolve()}
    if (roots["baseline"] / ".git").exists():
        parser.error("baseline must be a disposable exported snapshot, not a Git checkout")
    if args.pairs < 6:
        parser.error("at least six paired samples are required")
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=True)
    env = dict(os.environ, GOMAXPROCS="1", GOWORK="off")
    for relative in ("internal/machine/json_bench_test.go", "internal/appconfig/document_test.go"):
        (roots["baseline"] / relative).write_bytes((roots["candidate"] / relative).read_bytes())
    packages = {
        "contract": ("./internal/contract", "^Benchmark(CanonicalizeExactJSON|CanonicalJSONStrings)"),
        "machine": ("./internal/machine", "^BenchmarkJSONV2"),
        "appconfig": ("./internal/appconfig", "^BenchmarkJSONV2"),
    }
    report = {
        "go": run(["go", "version"], roots["candidate"], env).strip(),
        "platform": platform.platform(),
        "gomaxprocs": 1,
        "pairs": args.pairs,
        "benchtime": args.benchtime,
        "source_digests": {side: source_digest(root) for side, root in roots.items()},
        "samples": {},
    }
    pattern = re.compile(r"^(Benchmark\S+)\s+\d+\s+([\d.]+) ns/op(?:\s+[\d.]+ MB/s)?\s+([\d.]+) B/op\s+([\d.]+) allocs/op", re.M)
    expected_cases = {"contract": 6, "machine": 2, "appconfig": 4}
    for name, (package, bench) in packages.items():
        binaries = {}
        for side, root in roots.items():
            binaries[side] = out / f"{side}-{name}.test"
            run(["go", "test", "-c", "-o", str(binaries[side]), package], root, env)
        for pair in range(args.pairs):
            for side in (("baseline", "candidate") if pair % 2 == 0 else ("candidate", "baseline")):
                text = run([str(binaries[side]), "-test.run=^$", f"-test.bench={bench}",
                            f"-test.benchtime={args.benchtime}", "-test.benchmem"], roots[side] / package[2:], env)
                (out / f"{name}-{pair:02d}-{side}.txt").write_text(text)
                matches = pattern.findall(text)
                if len(matches) != expected_cases[name]:
                    raise RuntimeError(f"incomplete benchmark observations in {name}/{side}: {len(matches)}")
                for case, ns, size, allocs in matches:
                    sample = report["samples"].setdefault(case, {"baseline": [], "candidate": []})
                    sample[side].append({"ns": float(ns), "bytes": float(size), "allocs": float(allocs)})
        print(f"Measured {name}: {args.pairs} alternating pairs", flush=True)
    report["comparisons"] = {}
    uncertain = []
    for case, samples in report["samples"].items():
        before, after = ([x["ns"] for x in samples[side]] for side in roots)
        interval = ratio_interval(before, after)
        comparison = {"baseline_ns": statistics.median(before), "candidate_ns": statistics.median(after),
                      "ratio": statistics.median(after) / statistics.median(before), "paired_ratio_95ci": interval}
        for field in ("bytes", "allocs"):
            comparison[field] = {side: statistics.median(x[field] for x in samples[side]) for side in roots}
        report["comparisons"][case] = comparison
        if interval[0] > 1 or interval[1] > 1.05:
            uncertain.append(case)
        print(f"{case}: {100 * (comparison['ratio'] - 1):+.1f}% time; paired 95% CI {interval[0]:.3f}..{interval[1]:.3f}")
    report["regression_gate"] = {"reject_detected_slowdown": True, "maximum_uncertainty_ratio": 1.05,
                                 "unresolved": uncertain, "passed": not uncertain}
    (out / "report.json").write_text(json.dumps(report, indent=2) + "\n")
    if uncertain:
        raise SystemExit("Unresolved regressions or measurement uncertainty; inspect report.json")


if __name__ == "__main__":
    main()

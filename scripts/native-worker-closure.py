#!/usr/bin/env python3
"""Inspect a native-worker package cut from complete `go list -deps -json` captures.

This is an explicit architecture experiment, not runtime or performance proof.
It never runs Go, reads package source, changes an app, or activates a process.
"""

import argparse
from collections import deque
import hashlib
import json
from pathlib import Path
import sys


def load_graph(path):
    data = Path(path).read_bytes()
    source = data.decode("utf-8")
    decoder = json.JSONDecoder()
    graph = {}
    offset = 0
    while offset < len(source):
        while offset < len(source) and source[offset].isspace():
            offset += 1
        if offset == len(source):
            break
        package, offset = decoder.raw_decode(source, offset)
        name = package.get("ImportPath")
        if not name or name in graph:
            raise ValueError("capture has a missing or duplicate import path")
        if package.get("Error") or package.get("DepsErrors") or package.get("Incomplete"):
            raise ValueError(f"capture has an incomplete package: {name}")
        graph[name] = package
    if not graph:
        raise ValueError("empty package capture")
    for name, package in graph.items():
        for dependency in package.get("Imports", []):
            if dependency != "C" and dependency not in graph:
                raise ValueError(f"capture omits dependency: {name} -> {dependency}")
    return graph, "sha256:" + hashlib.sha256(data).hexdigest()


def paths_from(graph, root):
    if root not in graph:
        raise ValueError(f"entrypoint is absent: {root}")
    paths = {root: [root]}
    pending = deque([root])
    while pending:
        current = pending.popleft()
        for dependency in sorted(graph[current].get("Imports", [])):
            if dependency == "C" or dependency in paths:
                continue
            paths[dependency] = paths[current] + [dependency]
            pending.append(dependency)
    return paths


def under(name, prefix):
    return name == prefix or name.startswith(prefix + "/")


def inspect(graph, entrypoint, native_packages, kernel_packages):
    paths = paths_from(graph, entrypoint)
    native = sorted(set(native_packages) & paths.keys())
    retained_kernel = sorted(set(kernel_packages) & paths.keys())
    edges = []
    native_kernel_paths = []
    native_set = set(native_packages)
    for name in native:
        native_paths = paths_from(graph, name)
        for kernel in retained_kernel:
            if kernel in native_paths:
                native_kernel_paths.append(native_paths[kernel])
        for dependency in sorted(graph[name].get("Imports", [])):
            if dependency in paths and dependency not in native_set and not graph[dependency].get("Standard"):
                edges.append({"from": name, "to": dependency})
    return {
        "entrypoint": entrypoint,
        "reachable_packages": len(paths),
        "native_packages": native,
        "missing_native_packages": sorted(set(native_packages) - paths.keys()),
        "retained_kernel_paths": [paths[name] for name in retained_kernel],
        "native_kernel_paths": native_kernel_paths,
        "native_external_imports": edges,
    }


def compare(baseline, baseline_entry, candidate, candidate_entry, app_module, generated_prefixes, kernel_packages, baseline_kernel_packages=None):
    baseline_paths = paths_from(baseline, baseline_entry)
    if not kernel_packages:
        raise ValueError("at least one explicit kernel package is required")
    baseline_kernel_packages = baseline_kernel_packages or kernel_packages
    absent_kernel = set(baseline_kernel_packages) - baseline_paths.keys()
    if absent_kernel:
        raise ValueError("kernel packages absent from baseline: " + ", ".join(sorted(absent_kernel)))
    native = sorted(
        name for name in baseline_paths
        if baseline[name].get("Module", {}).get("Path") == app_module
        and name != baseline_entry
        and not any(under(name, prefix) for prefix in generated_prefixes)
    )
    if not native:
        raise ValueError("baseline has no native application packages")
    report = {
        "scope": "package reachability only; not linked-symbol, input-freshness, ABI, behavior or latency proof",
        "app_module": app_module,
        "excluded_generated_prefixes": generated_prefixes,
        "kernel_packages": kernel_packages,
        "baseline_kernel_packages": baseline_kernel_packages,
        "baseline": inspect(baseline, baseline_entry, native, baseline_kernel_packages),
        "candidate": None,
        "structural_gate": "not_run",
    }
    if candidate is not None:
        result = inspect(candidate, candidate_entry, native, kernel_packages)
        report["candidate"] = result
        report["structural_gate"] = (
            "fail" if result["missing_native_packages"] or result["retained_kernel_paths"] else "pass"
        )
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--baseline", required=True, help="complete go list -deps -json capture")
    parser.add_argument("--baseline-entry", required=True)
    parser.add_argument("--app-module", required=True)
    parser.add_argument("--generated-prefix", action="append", default=[], help="explicitly inventoried private generated package prefix; repeatable")
    parser.add_argument("--kernel-package", action="append", required=True, help="exact package required to leave the worker; repeatable")
    parser.add_argument("--baseline-kernel-package", action="append", help="explicit original package when the kernel import path moves; repeatable")
    parser.add_argument("--candidate")
    parser.add_argument("--candidate-entry")
    args = parser.parse_args()
    if bool(args.candidate) != bool(args.candidate_entry):
        parser.error("--candidate and --candidate-entry must be supplied together")
    try:
        baseline, baseline_digest = load_graph(args.baseline)
        candidate, candidate_digest = load_graph(args.candidate) if args.candidate else (None, None)
        report = compare(baseline, args.baseline_entry, candidate, args.candidate_entry,
                         args.app_module, args.generated_prefix, args.kernel_package, args.baseline_kernel_package)
        report["capture_digests"] = {"baseline": baseline_digest, "candidate": candidate_digest}
    except (OSError, UnicodeError, ValueError, TypeError, AttributeError) as error:
        print(f"native-worker-closure: {error}", file=sys.stderr)
        return 1
    print(json.dumps(report, indent=2, sort_keys=True))
    return 2 if report["structural_gate"] == "fail" else 0


if __name__ == "__main__":
    sys.exit(main())

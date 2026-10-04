#!/usr/bin/env python3
"""Run explicitly requested, sequential read-only context evaluations.

Inputs are frozen authored-document/context snapshots, never the live checkout.
This is a decision exercise, not repository verification or performance proof.
"""

import argparse
import hashlib
import json
import pathlib
import shutil
import subprocess
import tempfile
import time

CASES = {
    "docs": (
        "A user requests a typo correction in README.md. The final diff contains "
        "only prose, with no executable example or contract changes. Describe the "
        "minimum implementation and validation workflow. Decide whether a plan, "
        "doctor, quick/full verification, or permission is needed."
    ),
    "typescript": (
        "Pick up an authorized change to integer JSON decoding in "
        "internal/generate/generate_typescript_runtime.go. The final change is "
        "only that renderer, with no CLI or external runtime boundary change. "
        "context/docs.json contains prospective documentation/validation routing. "
        "Identify the normative TypeScript API and default JSON representation "
        "of int64/uint64, which source to edit, and the required validation. "
        "Name the exact contract sections needed, using supplied excerpts when "
        "sufficient. Do not implement the change or execute checks."
    ),
    "resume": (
        "Resume work from docs/plans/0101-public-deploy-edge.md after an interruption. "
        "context/validation.json is the archived repository context. For this "
        "decision exercise, assume its run's inputs match the supplied authored "
        "snapshot; this is a scenario assumption, not a live-state claim. "
        "Describe what still prevents closing that plan, the next action and "
        "its authorization boundary. Is controlled job reload enough, and is "
        "rebooting the machine authorized? Do not repeat the successful same-input "
        "repository/package tests or infer external acceptance from them."
    ),
}


def manifest(root):
    return {
        str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest()
        for p in sorted(root.rglob("*"))
        if p.is_file()
    }


def observe(log):
    commands, output_bytes, usage = [], 0, None
    for line in log.read_text().splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        item = event.get("item", {})
        if event.get("type") == "item.completed" and item.get("type") == "command_execution":
            commands.append(item.get("command", ""))
            output_bytes += len(item.get("aggregated_output", "").encode())
        if event.get("type") == "turn.completed":
            usage = event.get("usage")
    return {"read_commands": commands, "observed_tool_output_bytes": output_bytes,
            "cumulative_cli_usage": usage}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--before", type=pathlib.Path, required=True)
    parser.add_argument("--after", type=pathlib.Path, required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    parser.add_argument("--repetitions", type=int, default=3)
    parser.add_argument("--model", default="gpt-6-astra")
    parser.add_argument("--cases", nargs="+", choices=tuple(CASES), default=list(CASES))
    args = parser.parse_args()
    if args.repetitions < 1:
        parser.error("repetitions must be positive")
    if len(set(args.cases)) != len(args.cases):
        parser.error("cases must be unique")
    variants = {"before": args.before.resolve(), "after": args.after.resolve()}
    required = ("AGENTS.md", "context/docs.json", "context/validation.json",
                "docs/spec/typescript-client.md", "docs/plans/0101-public-deploy-edge.md")
    for name, root in variants.items():
        missing = [path for path in required if not (root / path).is_file()]
        if missing:
            parser.error(f"{name} snapshot is incomplete: {', '.join(missing)}")
    args.output.mkdir(parents=True, exist_ok=False)
    shutil.copyfile(__file__, args.output / "runner.py")
    for name, root in variants.items():
        (args.output / f"{name}-manifest.json").write_text(json.dumps(manifest(root), indent=2)+"\n")
    schema = {
        "type": "object", "additionalProperties": False,
        "properties": {"commands": {"type": "array", "items": {"type": "string"}},
                       "contract_sections": {"type": "array", "items": {"type": "string"}},
                       "requires_permission": {"type": "boolean"},
                       "next_action": {"type": "string"}, "rationale": {"type": "string"}},
        "required": ["commands", "contract_sections", "requires_permission", "next_action", "rationale"],
    }
    schema_path = (args.output / "answer-schema.json").resolve()
    schema_path.write_text(json.dumps(schema))
    results = []
    for repeat in range(args.repetitions):
        # Reverse both orders on alternating repetitions to reduce a fixed-order confound.
        order = list(variants) if repeat % 2 == 0 else list(reversed(variants))
        cases = list(args.cases) if repeat % 2 == 0 else list(reversed(args.cases))
        for case in cases:
            for variant in order:
                name = f"{repeat+1}-{variant}-{case}"
                log = args.output / f"{name}.jsonl"
                stderr = args.output / f"{name}.stderr"
                answer = (args.output / f"{name}.answer.json").resolve()
                prompt = (
                    "This is an authorized read-only instruction evaluation in an isolated "
                    "documentation snapshot. Do not edit files, execute validation, access "
                    "the network, spawn subagents, create tasks, or change external state. "
                    "Read only the local instructions/context needed to answer. You are "
                    "not told a variant label or expected answer. Return concise JSON.\n\n"
                    + CASES[case]
                )
                with tempfile.TemporaryDirectory(prefix="scenery-agent-eval-") as temp:
                    shutil.copytree(variants[variant], temp, dirs_exist_ok=True)
                    command = ["codex", "exec", "--ignore-user-config", "--ephemeral",
                               "--skip-git-repo-check", "--model", args.model,
                               "-c", 'model_reasoning_effort="medium"', "--sandbox", "read-only",
                               "--json", "--output-schema", str(schema_path), "-C", temp,
                               "-o", str(answer), "-"]
                    started = time.monotonic()
                    with log.open("w") as out, stderr.open("w") as err:
                        try:
                            result = subprocess.run(command, input=prompt, text=True,
                                                    stdout=out, stderr=err, timeout=240)
                            code = result.returncode
                        except subprocess.TimeoutExpired:
                            code = "timeout"
                    record = {"repetition": repeat+1, "variant": variant, "case": case,
                              "model": args.model, "reasoning": "medium", "exit_code": code,
                              "elapsed_seconds": round(time.monotonic()-started, 3),
                              "prompt": prompt, **observe(log)}
                    if answer.exists():
                        try:
                            record["answer"] = json.loads(answer.read_text())
                        except json.JSONDecodeError:
                            record["answer_error"] = "invalid JSON"
                    record["snapshot_unchanged"] = manifest(pathlib.Path(temp)) == manifest(variants[variant])
                (args.output / f"{name}.meta.json").write_text(json.dumps(record, indent=2)+"\n")
                results.append(record)
                (args.output / "results.json").write_text(json.dumps(results, indent=2)+"\n")
                print(json.dumps({k: record[k] for k in ["repetition", "variant", "case", "exit_code", "elapsed_seconds", "observed_tool_output_bytes", "snapshot_unchanged"]}), flush=True)
    if any(r["exit_code"] != 0 or "answer" not in r or not r["snapshot_unchanged"] for r in results):
        raise SystemExit("Some sessions did not complete or changed their snapshot; inspect raw evidence.")


if __name__ == "__main__":
    main()

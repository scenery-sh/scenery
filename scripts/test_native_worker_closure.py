"""In-process tests for the experiment's admission boundary."""

import importlib.util
import json
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("closure", Path(__file__).with_name("native-worker-closure.py"))
closure = importlib.util.module_from_spec(spec)
spec.loader.exec_module(closure)


def package(*imports, module="app"):
    return {"Imports": list(imports), "Module": {"Path": module}}


class ClosureTests(unittest.TestCase):
    def setUp(self):
        self.baseline = {
            "app/main": package("app/service", "app/generated/adapter"),
            "app/generated/adapter": package("kernel"),
            "app/service": package("app/dependency"),
            "app/dependency": package(),
            "kernel": package(module="framework"),
        }

    def compare(self, candidate):
        return closure.compare(self.baseline, "app/main", candidate, "app/main", "app", ["app/generated"], ["kernel"])

    def test_unchanged_runtime_fails_even_with_full_app(self):
        report = self.compare(self.baseline)
        self.assertEqual(report["structural_gate"], "fail")
        self.assertEqual(report["candidate"]["retained_kernel_paths"], [["app/main", "app/generated/adapter", "kernel"]])

    def test_toy_worker_cannot_omit_transitive_native_dependency(self):
        candidate = {"app/main": package("app/service"), "app/service": package(), "app/dependency": package()}
        report = self.compare(candidate)
        self.assertEqual(report["structural_gate"], "fail")
        self.assertEqual(report["candidate"]["missing_native_packages"], ["app/dependency"])

    def test_complete_native_cut_passes_only_structural_gate(self):
        candidate = dict(self.baseline, **{"app/main": package("app/service")})
        self.assertEqual(self.compare(candidate)["structural_gate"], "pass")

    def test_relocated_host_is_still_rejected(self):
        candidate = dict(self.baseline)
        candidate["app/generated/adapter"] = package("app/service", "host")
        candidate["host"] = package(module="framework")
        report = closure.compare(self.baseline, "app/main", candidate, "app/main", "app", ["app/generated"], ["host"], ["kernel"])
        self.assertEqual(report["structural_gate"], "fail")
        self.assertEqual(report["baseline_kernel_packages"], ["kernel"])
        self.assertEqual(report["candidate"]["retained_kernel_paths"], [["app/main", "app/generated/adapter", "host"]])

    def test_baseline_only_is_not_acceptance(self):
        report = closure.compare(self.baseline, "app/main", None, None, "app", ["app/generated"], ["kernel"])
        self.assertEqual(report["structural_gate"], "not_run")

    def test_missing_kernel_and_empty_app_reject_vacuous_proof(self):
        with self.assertRaisesRegex(ValueError, "absent from baseline"):
            closure.compare(self.baseline, "app/main", None, None, "app", [], ["absent"])
        with self.assertRaisesRegex(ValueError, "no native application"):
            closure.compare(self.baseline, "app/main", None, None, "other", [], ["kernel"])

    def test_incomplete_captures_are_rejected(self):
        cases = [
            [],
            [{"ImportPath": "main", "Imports": ["missing"]}],
            [{"ImportPath": "main", "Incomplete": True}],
            [{"ImportPath": "main", "Error": {"Err": "broken"}}],
            [{"ImportPath": "main"}, {"ImportPath": "main"}],
        ]
        for packages in cases:
            with self.subTest(packages=packages):
                data = "\n".join(json.dumps(p) for p in packages).encode()
                with patch.object(Path, "read_bytes", return_value=data):
                    with self.assertRaises(ValueError):
                        closure.load_graph("capture.json")

    def test_capture_identity_and_cgo_pseudo_import(self):
        data = b'{"ImportPath":"main","Imports":["C"]}\n'
        with patch.object(Path, "read_bytes", return_value=data):
            graph, digest = closure.load_graph("capture.json")
        self.assertEqual(closure.paths_from(graph, "main"), {"main": ["main"]})
        self.assertEqual(digest, "sha256:" + closure.hashlib.sha256(data).hexdigest())

    def test_missing_entrypoint_is_not_a_cut(self):
        with self.assertRaisesRegex(ValueError, "entrypoint is absent"):
            self.compare({"unreachable": package()})


if __name__ == "__main__":
    unittest.main()

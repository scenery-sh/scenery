package generate

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"scenery.sh/internal/compiler"
	pythonsdk "scenery.sh/sdk/python"
)

func pythonServices(result *compiler.Result) []Resource {
	return slices.Collect(func(yield func(Resource) bool) {
		for _, resource := range result.Manifest.Resources {
			if resource.Kind == "scenery.service" && compiler.IsPythonRuntime(stringValue(resource.Spec["runtime"])) && !yield(resource) {
				return
			}
		}
	})
}

func renderPythonContractFiles(result *compiler.Result, services []Resource) ([]generatedFile, error) {
	idx := newResourceIndex(result.Manifest.Resources)
	var files []generatedFile
	directories := make(map[string]bool)
	for _, service := range services {
		target, err := compiler.SelectPythonWorkerTarget(result, service, "development")
		if err != nil {
			return nil, err
		}
		dir := filepath.Join(result.Root, target.Directory, "scenerycontract")
		if !managedContractDirectory(result, dir) {
			return nil, fmt.Errorf("contract output for Python at %s is outside managed_generated_roots", dir)
		}
		if directories[dir] {
			return nil, fmt.Errorf("python contract directory %s is used by more than one service", dir)
		}
		directories[dir] = true

		entries, err := pythonsdk.SDK.ReadDir("scenery_sdk")
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			data, err := fs.ReadFile(pythonsdk.SDK, "scenery_sdk/"+entry.Name())
			if err != nil {
				return nil, err
			}
			files = append(files, generatedFile{Path: filepath.Join(dir, "sdk", entry.Name()), Bytes: data})
		}

		typesSrc, initSrc, mainSrc, buildSrc, err := renderPythonService(service, target, idx, result.Manifest.Resources)
		if err != nil {
			return nil, err
		}
		sources := map[string]string{
			"_types.py":   typesSrc,
			"__init__.py": initSrc,
			"_main.py":    mainSrc,
			"_build.py":   buildSrc,
		}
		for _, name := range slices.Sorted(maps.Keys(sources)) {
			files = append(files, generatedFile{Path: filepath.Join(dir, name), Bytes: []byte(sources[name])})
		}
	}
	return files, nil
}

type pythonProjection struct {
	module string
	types  map[string]Resource
}

func renderPythonService(service Resource, target compiler.PythonWorkerTarget, idx *resourceIndex, resources []Resource) (string, string, string, string, error) {
	operations := compiler.ServiceOperations(resources, service)
	clients, err := serviceGoClients(idx, service)
	if err != nil {
		return "", "", "", "", err
	}
	reachable := make(map[string]bool)
	collect := func(resource Resource) {
		for _, value := range contractTypeValues([]Resource{resource}) {
			collectABITypeReferences(value, resource.Module, idx.byAddress, reachable)
		}
	}
	for _, operation := range operations {
		collect(operation)
	}
	for _, client := range clients {
		collect(client.Operation)
	}
	for before := -1; before != len(reachable); {
		before = len(reachable)
		for address := range reachable {
			collect(idx.byAddress[address])
		}
	}

	projection := pythonProjection{module: service.Module, types: make(map[string]Resource)}
	var shapes []Resource
	for address := range reachable {
		projection.types[address] = idx.byAddress[address]
		shapes = append(shapes, idx.byAddress[address])
	}
	slices.SortFunc(shapes, func(a, b Resource) int { return strings.Compare(a.Address, b.Address) })

	var types, api, main strings.Builder
	var exported []string

	types.WriteString("# Code generated for the application. DO NOT EDIT.\n" +
		"from __future__ import annotations\n\n" +
		"from dataclasses import dataclass, field\n" +
		"from typing import Any, Literal, Mapping, Sequence, TypeAlias\n\n" +
		"from .sdk.codec import MISSING, ExecutionReceipt, MissingType, Problem\n\n" +
		"Decimal: TypeAlias = str\n" +
		"UUID: TypeAlias = str\n" +
		"Date: TypeAlias = str\n" +
		"DateTime: TypeAlias = str\n" +
		"Duration: TypeAlias = str\n" +
		"URL: TypeAlias = str\n" +
		"RelativePath: TypeAlias = str\n" +
		"JsonValue: TypeAlias = Any\n\n")

	for _, shape := range shapes {
		exported = append(exported, projection.renderShape(&types, shape)...)
	}

	// Build Python TYPE_REGISTRY binding descriptors to generated dataclasses.
	registryEntries := make(map[string]any, len(shapes))
	for _, shape := range shapes {
		key := tsRegistryKey(shape)
		if key == "" {
			continue
		}
		registryEntries[key] = pythonNamedDescriptor(shape)
	}
	registryJSON, err := json.Marshal(registryEntries)
	if err != nil {
		return "", "", "", "", err
	}

	api.WriteString("# Code generated for the application. DO NOT EDIT.\n" +
		"from __future__ import annotations\n\n" +
		"from dataclasses import dataclass\n" +
		"import json\n" +
		"from typing import Any, Awaitable, Callable, Mapping, Protocol\n\n" +
		"from .sdk import (\n" +
		"    MISSING,\n" +
		"    CapabilityError,\n" +
		"    Context,\n" +
		"    EncodedEventStream,\n" +
		"    EventStream,\n" +
		"    ExecutionReceipt,\n" +
		"    InvocationContext,\n" +
		"    MissingType,\n" +
		"    Problem,\n" +
		"    ServerEvent,\n" +
		"    StorageReadResponse,\n" +
		"    Stream,\n" +
		"    decode_worker_value,\n" +
		"    encode_typed_json,\n" +
		"    event_stream,\n" +
		"    outcome_codec,\n" +
		"    sse_event,\n" +
		"    stream,\n" +
		")\n" +
		"from .sdk.worker import maybe_await\n" +
		"from ._types import *\n" +
		"from ._types import (\n" +
		"    Date,\n" +
		"    DateTime,\n" +
		"    Decimal,\n" +
		"    Duration,\n" +
		"    JsonValue,\n" +
		"    RelativePath,\n" +
		"    URL,\n" +
		"    UUID,\n" +
		")\n\n")
	fmt.Fprintf(&api, "SERVICE_ADDRESS = %q\n\n", service.Address)
	fmt.Fprintf(&api, "TYPE_REGISTRY: dict[str, Any] = json.loads(%q)\n", string(registryJSON))
	for _, shape := range shapes {
		key := tsRegistryKey(shape)
		if key == "" {
			continue
		}
		name := projection.name(shape)
		switch shape.Kind {
		case "scenery.record":
			fmt.Fprintf(&api, "TYPE_REGISTRY[%q][\"cls\"] = %s\n", key, name)
		case "scenery.union":
			fmt.Fprintf(&api, "TYPE_REGISTRY[%q][\"variant_classes\"] = {", key)
			for _, variant := range namedChildren(shape.Spec, "variant") {
				wire := wireName(variant, stringValue(variant["name"]))
				cls := name + goName(stringValue(variant["name"]))
				fmt.Fprintf(&api, "%q: %s, ", wire, cls)
			}
			api.WriteString("}\n")
			if shape.Spec["open"] == true {
				fmt.Fprintf(&api, "TYPE_REGISTRY[%q][\"unknown_cls\"] = %sUnknown\n", key, name)
			}
		}
	}
	api.WriteString("\n")

	projection.renderClients(&types, &api, clients, &exported)

	api.WriteString("@dataclass(frozen=True, slots=True)\n" +
		"class ConstructorInput:\n" +
		"    clients: Clients\n" +
		"    config: Any\n" +
		"    databases: Mapping[str, str]\n\n")

	var handlers strings.Builder
	var protocolMethods strings.Builder
	lifecycle := specChild(service, "lifecycle")
	for _, phase := range []string{"start", "stop"} {
		if method := stringValue(lifecycle[phase]); method != "" {
			if !compiler.IsValidPythonIdentifier(method) {
				return "", "", "", "", fmt.Errorf("python lifecycle method %q must be a valid Python identifier", method)
			}
			fmt.Fprintf(&protocolMethods, "    def %s(self) -> None | Awaitable[None]: ...\n", method)
		}
	}
	for _, operation := range operations {
		name := goName(operation.Name)
		method := stringValue(specChild(operation, "handler")["method"])
		if !compiler.IsValidPythonIdentifier(method) {
			return "", "", "", "", fmt.Errorf("python operation %s handler method %q must be a valid Python identifier", operation.Address, method)
		}
		canStream := operationUsesHTTPStream(operation, resources)
		canStreamOut := operationUsesHTTPStreamOut(operation, resources)
		exported = append(exported, projection.renderOperation(&types, operation, name, canStreamOut)...)
		ret := name + "Outcome"
		if canStream {
			ret = fmt.Sprintf("%sOutcome | Stream[%sOutcome]", name, name)
		} else if canStreamOut {
			ret = fmt.Sprintf("%sOutcome | EventStream[%sOutcome, %sEventData]", name, name, name)
		}
		fmt.Fprintf(&protocolMethods, "    def %s(self, ctx: Context, input: %sInput) -> %s | Awaitable[%s]: ...\n", method, name, ret, ret)

		codecVar := "_" + pythonSnake(operation.Name) + "_outcome_codec"
		fmt.Fprintf(&handlers, "%s = outcome_codec(TYPE_REGISTRY, json.loads(%q), %s)\n",
			codecVar, jsOutcomeDescriptors(operation), pythonOutcomeClassesDict(operation, name))
		fmt.Fprintf(&handlers, "_%s_input_descriptor = json.loads(%q)\n", pythonSnake(operation.Name), jsInputDescriptor(operation))
		if canStreamOut {
			resultVariant, _ := operationStreamOutResult(operation)
			resultName := stringValue(resultVariant["name"])
			resultCls := name + goName(resultName)
			eventDescBytes, _ := json.Marshal(tsDescriptor(resultVariant["type"], operation.Module))
			snake := pythonSnake(operation.Name)
			fmt.Fprintf(&handlers, "_%s_event_descriptor = json.loads(%q)\n", snake, string(eventDescBytes))
			fmt.Fprintf(&handlers, "async def _dispatch_%s(service: Any, raw_input: Any, ctx: Context) -> tuple[str, Stream[Any] | EncodedEventStream | None]:\n", snake)
			fmt.Fprintf(&handlers, "    decoded = decode_worker_value(raw_input, _%s_input_descriptor, TYPE_REGISTRY)\n", snake)
			fmt.Fprintf(&handlers, "    output = await maybe_await(service.%s(ctx, decoded))\n", method)
			fmt.Fprintf(&handlers, "    if isinstance(output, EventStream):\n"+
				"        if not isinstance(output.outcome, %s):\n"+
				"            raise TypeError('stream_out EventStream outcome must be %s')\n"+
				"        async def _encode_events():\n"+
				"            source = output.events\n"+
				"            def _line(item: Any) -> str:\n"+
				"                if isinstance(item, ServerEvent):\n"+
				"                    data = encode_typed_json(item.data, _%s_event_descriptor, TYPE_REGISTRY)\n"+
				"                    line = '{\"data\":' + data\n"+
				"                    if item.id is not None:\n"+
				"                        line += ',\"id\":' + json.dumps(item.id)\n"+
				"                    if item.event is not None:\n"+
				"                        line += ',\"event\":' + json.dumps(item.event)\n"+
				"                    return line + '}'\n"+
				"                return '{\"data\":' + encode_typed_json(item, _%s_event_descriptor, TYPE_REGISTRY) + '}'\n"+
				"            if hasattr(source, '__aiter__'):\n"+
				"                async for item in source:\n"+
				"                    yield _line(item)\n"+
				"            else:\n"+
				"                for item in source:\n"+
				"                    yield _line(item)\n"+
				"        return '{\"kind\":\"result\",\"name\":' + json.dumps(%q) + ',\"value\":null}', EncodedEventStream(_encode_events())\n"+
				"    if isinstance(output, %s):\n"+
				"        raise TypeError('stream_out result requires an EventStream')\n"+
				"    return %s.encode(output), None\n\n",
				resultCls, resultCls, snake, snake, resultName, resultCls, codecVar)
			continue
		}
		fmt.Fprintf(&handlers, "async def _dispatch_%s(service: Any, raw_input: Any, ctx: Context) -> tuple[str, Stream[Any] | EncodedEventStream | None]:\n", pythonSnake(operation.Name))
		fmt.Fprintf(&handlers, "    decoded = decode_worker_value(raw_input, _%s_input_descriptor, TYPE_REGISTRY)\n", pythonSnake(operation.Name))
		fmt.Fprintf(&handlers, "    output = await maybe_await(service.%s(ctx, decoded))\n", method)
		if canStream {
			fmt.Fprintf(&handlers, "    if isinstance(output, Stream):\n"+
				"        return %s.encode(output.outcome), output\n"+
				"    return %s.encode(output), None\n\n", codecVar, codecVar)
		} else {
			fmt.Fprintf(&handlers, "    return %s.encode(output), None\n\n", codecVar)
		}
	}

	api.WriteString("class Service(Protocol):\n")
	if protocolMethods.Len() == 0 {
		api.WriteString("    pass\n")
	} else {
		api.WriteString(protocolMethods.String())
	}
	api.WriteString("\nConstructor = Callable[[ConstructorInput], Service | Awaitable[Service]]\n\n")
	api.WriteString(handlers.String())

	api.WriteString("async def dispatch(service: Any, operation: str, raw_input: Any, ctx: Context) -> tuple[str, Stream[Any] | EncodedEventStream | None]:\n")
	if len(operations) == 0 {
		api.WriteString("    raise RuntimeError('unknown worker operation')\n")
	} else {
		for index, operation := range operations {
			kw := "if"
			if index > 0 {
				kw = "elif"
			}
			fmt.Fprintf(&api, "    %s operation == %q:\n        return await _dispatch_%s(service, raw_input, ctx)\n",
				kw, operation.Address, pythonSnake(operation.Name))
		}
		api.WriteString("    raise RuntimeError('unknown worker operation')\n")
	}

	typesAll := append([]string{
		"Date", "DateTime", "Decimal", "Duration", "ExecutionReceipt",
		"JsonValue", "MISSING", "MissingType", "Problem", "RelativePath", "URL", "UUID",
	}, exported...)
	slices.Sort(typesAll)
	typesAll = slices.Compact(typesAll)
	types.WriteString("__all__ = [\n")
	for _, sym := range typesAll {
		fmt.Fprintf(&types, "    %q,\n", sym)
	}
	types.WriteString("]\n")

	apiAll := append([]string{
		"CapabilityError", "Clients", "Constructor", "ConstructorInput", "Context",
		"EncodedEventStream", "EventStream", "InvocationContext", "SERVICE_ADDRESS",
		"ServerEvent", "Service", "StorageReadResponse",
		"Stream", "TYPE_REGISTRY", "clients", "decode_worker_value", "dispatch",
		"encode_typed_json", "event_stream", "outcome_codec", "sse_event", "stream",
	}, typesAll...)
	slices.Sort(apiAll)
	apiAll = slices.Compact(apiAll)
	api.WriteString("\n__all__ = [\n")
	for _, sym := range apiAll {
		fmt.Fprintf(&api, "    %q,\n", sym)
	}
	api.WriteString("]\n")

	constructor := stringValue(specChild(service, "implementation")["constructor"])
	entryModule := pythonModuleName(target.Entry)

	main.WriteString("# Code generated for the application. DO NOT EDIT.\n" +
		"from __future__ import annotations\n\n" +
		"import asyncio\n" +
		"import importlib\n\n" +
		"from . import SERVICE_ADDRESS, ConstructorInput, clients, dispatch\n" +
		"from .sdk.worker import maybe_await, read_launch, serve, startup_inputs\n\n" +
		"async def _run() -> None:\n" +
		"    launch = read_launch()\n" +
		"    if launch['identity']['service'] != SERVICE_ADDRESS:\n" +
		"        raise RuntimeError('worker service mismatch')\n")
	fmt.Fprintf(&main, "    mod = importlib.import_module(%q)\n", entryModule)
	fmt.Fprintf(&main, "    construct = getattr(mod, %q)\n", constructor)
	main.WriteString("    inputs = startup_inputs()\n" +
		"    service = await maybe_await(construct(ConstructorInput(clients=clients, config=inputs.config, databases=inputs.databases)))\n")
	for _, phase := range []string{"start", "stop"} {
		if method := stringValue(lifecycle[phase]); method != "" {
			fmt.Fprintf(&main, "    if not callable(getattr(service, %q, None)):\n        raise RuntimeError('missing lifecycle hook %s')\n", method, method)
		}
	}
	for _, operation := range operations {
		method := stringValue(specChild(operation, "handler")["method"])
		fmt.Fprintf(&main, "    if not callable(getattr(service, %q, None)):\n        raise RuntimeError('missing worker handler %s')\n", method, method)
	}
	if method := stringValue(lifecycle["start"]); method != "" {
		fmt.Fprintf(&main, "    await maybe_await(service.%s())\n", method)
	}
	main.WriteString("    try:\n" +
		"        await serve(launch, lambda op, raw_input, ctx: dispatch(service, op, raw_input, ctx))\n" +
		"    finally:\n")
	if method := stringValue(lifecycle["stop"]); method != "" {
		fmt.Fprintf(&main, "        await maybe_await(service.%s())\n", method)
	} else {
		main.WriteString("        pass\n")
	}
	main.WriteString("\ndef main() -> None:\n    asyncio.run(_run())\n\nif __name__ == '__main__':\n    main()\n")

	buildScript := renderPythonBuildScript(target.Entry, constructor)
	return types.String(), api.String(), main.String(), buildScript, nil
}

func renderPythonBuildScript(entry, constructor string) string {
	return fmt.Sprintf(`# Code generated for the application. DO NOT EDIT.
from __future__ import annotations

import argparse
import ast
import os
from pathlib import Path
import py_compile
import shlex
import stat
import sys
import tempfile
import zipfile

ENTRY = %q
CONSTRUCTOR = %q
SKIP_DIRS = frozenset({
    ".git", ".scenery", ".claude", "node_modules", "dist", "out", "target",
    ".venv", "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache",
})
SKIP_FILES = frozenset({
    ".DS_Store", ".scenery.json", "uv.lock", "Cargo.lock", "Cargo.toml",
    "go.mod", "go.sum", "bun.lock", "package-lock.json",
})
SKIP_EXTS = frozenset({".go", ".rs", ".scn", ".pyc", ".pyo"})


def _is_test_file(name: str) -> bool:
    return (name.startswith("test_") and name.endswith(".py")) or name.endswith("_test.py")


def _skip_file(name: str) -> bool:
    if name in SKIP_FILES or name.startswith(".env"):
        return True
    _, ext = os.path.splitext(name)
    return ext in SKIP_EXTS


def _collect_files(module_dir: Path, include_tests: bool) -> list[tuple[Path, str]]:
    files: list[tuple[Path, str]] = []
    for root, dirs, names in os.walk(module_dir, followlinks=False):
        root_path = Path(root)
        dirs[:] = sorted(d for d in dirs if d not in SKIP_DIRS)
        for d in list(dirs):
            if (root_path / d).is_symlink():
                raise RuntimeError(f"Python worker directory cannot contain symlink: {root_path / d}")
        for name in sorted(names):
            full = root_path / name
            if full.is_symlink():
                raise RuntimeError(f"Python worker file cannot be a symlink: {full}")
            if not full.is_file() or _skip_file(name):
                continue
            rel = full.relative_to(module_dir).as_posix()
            if rel == "scenerycontract/_build.py":
                continue
            if not include_tests and _is_test_file(name):
                continue
            files.append((full, rel))
    files.sort(key=lambda item: item[1])
    return files


def _verify_module(module_dir: Path, files: list[tuple[Path, str]]) -> None:
    entry_path = module_dir / ENTRY
    if not entry_path.is_file() or entry_path.is_symlink():
        raise RuntimeError(f"missing Python entry file: {ENTRY}")
    with tempfile.TemporaryDirectory(prefix="scenery-pycompile-") as tmpdir:
        for full, rel in files:
            if not rel.endswith(".py"):
                continue
            cfile = os.path.join(tmpdir, rel.replace("/", "_") + "c")
            py_compile.compile(str(full), cfile=cfile, doraise=True)
    source = entry_path.read_text(encoding="utf-8")
    tree = ast.parse(source, filename=str(entry_path))
    exported: set[str] = set()
    for node in tree.body:
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            exported.add(node.name)
        elif isinstance(node, ast.Assign):
            for target in node.targets:
                if isinstance(target, ast.Name):
                    exported.add(target.id)
        elif isinstance(node, ast.AnnAssign) and isinstance(node.target, ast.Name):
            exported.add(node.target.id)
        elif isinstance(node, (ast.Import, ast.ImportFrom)):
            for alias in node.names:
                exported.add(alias.asname or alias.name.split(".")[0])
    if CONSTRUCTOR not in exported:
        raise RuntimeError(f"Python entry {ENTRY} does not define constructor {CONSTRUCTOR}")


def _shebang(interpreter: str) -> bytes:
    encoded = interpreter.encode("utf-8")
    if len(encoded) <= 100 and not any(ch.isspace() for ch in interpreter):
        return b"#!" + encoded + b"\n"
    return ("#!/bin/sh\nexec " + shlex.quote(interpreter) + ' "$0" "$@"\n').encode("utf-8")


def _build_zipapp(module_dir: Path, files: list[tuple[Path, str]], output: Path, interpreter: str) -> None:
    output.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp_name = tempfile.mkstemp(prefix=".py-worker-", dir=str(output.parent))
    tmp_path = Path(tmp_name)
    try:
        with os.fdopen(fd, "wb") as out:
            out.write(_shebang(interpreter))
            with zipfile.ZipFile(out, "w", compression=zipfile.ZIP_STORED) as zf:
                main_info = zipfile.ZipInfo("__main__.py", date_time=(1980, 1, 1, 0, 0, 0))
                main_info.external_attr = 0o644 << 16
                zf.writestr(main_info, "from scenerycontract._main import main\nmain()\n")
                for full, rel in files:
                    info = zipfile.ZipInfo(rel, date_time=(1980, 1, 1, 0, 0, 0))
                    info.external_attr = 0o644 << 16
                    zf.writestr(info, full.read_bytes())
        os.chmod(tmp_path, stat.S_IRWXU | stat.S_IRGRP | stat.S_IXGRP | stat.S_IROTH | stat.S_IXOTH)
        os.replace(tmp_path, output)
    except Exception:
        try:
            tmp_path.unlink(missing_ok=True)
        except OSError:
            pass
        raise


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", default="")
    parser.add_argument("--interpreter", default=sys.executable)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    module_dir = Path(__file__).resolve().parents[1]
    files = _collect_files(module_dir, include_tests=False)
    _verify_module(module_dir, files)
    if args.check:
        return
    if not args.output:
        raise RuntimeError("--output is required unless --check is set")
    _build_zipapp(module_dir, files, Path(args.output), args.interpreter)


if __name__ == "__main__":
    main()
`, entry, constructor)
}

func (p pythonProjection) renderClients(types, api *strings.Builder, clients []goClientBinding, exported *[]string) {
	for _, client := range clients {
		op := client.Operation
		name := client.Field + "Client"
		*exported = append(*exported, p.renderOperation(types, op, name, false)...)
		className := "_" + name + "Handle"
		if client.Delivery == "enqueue" {
			fmt.Fprintf(api, "class %s:\n"+
				"    _input = json.loads(%q)\n"+
				"    _receipt = {\"kind\": \"primitive\", \"name\": \"execution_receipt\"}\n"+
				"    async def enqueue(self, ctx: Context, input: %sInput) -> ExecutionReceipt:\n"+
				"        wire = encode_typed_json(input, self._input, TYPE_REGISTRY)\n"+
				"        reply = await ctx.binding_request(%q, wire, enqueue=True)\n"+
				"        return decode_worker_value(reply, self._receipt, TYPE_REGISTRY)\n\n",
				className, jsInputDescriptor(op), name, client.Name)
		} else {
			fmt.Fprintf(api, "class %s:\n"+
				"    _input = json.loads(%q)\n"+
				"    _outcome = outcome_codec(TYPE_REGISTRY, json.loads(%q), %s)\n"+
				"    async def invoke(self, ctx: Context, input: %sInput) -> %sOutcome:\n"+
				"        wire = encode_typed_json(input, self._input, TYPE_REGISTRY)\n"+
				"        reply = await ctx.binding_request(%q, wire)\n"+
				"        return self._outcome.decode(reply)\n\n",
				className, jsInputDescriptor(op), jsOutcomeDescriptors(op), pythonOutcomeClassesDict(op, name), name, name, client.Name)
		}
	}
	api.WriteString("@dataclass(frozen=True, slots=True)\nclass Clients:\n")
	if len(clients) == 0 {
		api.WriteString("    pass\n\n")
	} else {
		for _, client := range clients {
			fmt.Fprintf(api, "    %s: _%sClientHandle\n", pythonFieldName(client.Name), client.Field)
		}
		api.WriteString("\n")
	}
	api.WriteString("clients = Clients(")
	for index, client := range clients {
		if index > 0 {
			api.WriteString(", ")
		}
		fmt.Fprintf(api, "%s=_%sClientHandle()", pythonFieldName(client.Name), client.Field)
	}
	api.WriteString(")\n\n")
}

func (p pythonProjection) renderOperation(source *strings.Builder, operation Resource, name string, streamOut bool) []string {
	var names []string
	input := p.typeExpression(operation.Spec["input"], operation.Module)
	if input != name+"Input" {
		fmt.Fprintf(source, "%sInput: TypeAlias = %s\n", name, input)
		names = append(names, name+"Input")
	}
	if streamOut {
		if resultVariant, ok := operationStreamOutResult(operation); ok {
			fmt.Fprintf(source, "%sEventData: TypeAlias = %s\n", name, p.typeExpression(resultVariant["type"], operation.Module))
			names = append(names, name+"EventData")
		}
	}
	var variants []string
	for _, kind := range []string{"result", "error"} {
		fieldName := "value"
		if kind == "error" {
			fieldName = "problem"
		}
		for _, variant := range namedChildren(operation.Spec, kind) {
			variantName := stringValue(variant["name"])
			cls := name + goName(variantName)
			variants = append(variants, cls)
			names = append(names, cls)
			if streamOut && kind == "result" {
				fmt.Fprintf(source, "@dataclass(frozen=True, slots=True)\nclass %s:\n"+
					"    kind: Literal[%q] = %q\n"+
					"    name: Literal[%q] = %q\n\n",
					cls, kind, kind, variantName, variantName)
				continue
			}
			fmt.Fprintf(source, "@dataclass(frozen=True, slots=True)\nclass %s:\n"+
				"    %s: %s\n"+
				"    kind: Literal[%q] = %q\n"+
				"    name: Literal[%q] = %q\n\n",
				cls, fieldName, p.typeExpression(variant["type"], operation.Module), kind, kind, variantName, variantName)
		}
	}
	if len(variants) == 0 {
		fmt.Fprintf(source, "%sOutcome: TypeAlias = Any\n\n", name)
	} else {
		fmt.Fprintf(source, "%sOutcome: TypeAlias = %s\n\n", name, strings.Join(variants, " | "))
	}
	names = append(names, name+"Outcome")
	return names
}

func pythonOutcomeClassesDict(operation Resource, name string) string {
	var entries []string
	for _, kind := range []string{"result", "error"} {
		for _, variant := range namedChildren(operation.Spec, kind) {
			variantName := stringValue(variant["name"])
			entries = append(entries, fmt.Sprintf("%q: %s", kind+"."+variantName, name+goName(variantName)))
		}
	}
	return "{" + strings.Join(entries, ", ") + "}"
}

func (p pythonProjection) name(resource Resource) string {
	if resource.Module == p.module {
		return goName(resource.Name)
	}
	return goName(strings.ReplaceAll(resource.Module, "/", "_")) + goName(resource.Name)
}

func (p pythonProjection) typeExpression(value any, module string) string {
	return p.renderExpression(qualifyProjectedGoExpression(typeExpression(value), module))
}

func (p pythonProjection) renderExpression(expression string) string {
	if resource, ok := p.types[expression]; ok {
		return p.name(resource)
	}
	if name, arguments, ok := parseTSExpression(expression); ok {
		for index, argument := range arguments {
			arguments[index] = p.renderExpression(argument)
		}
		switch name {
		case "optional":
			return arguments[0] + " | MissingType"
		case "nullable":
			return arguments[0] + " | None"
		case "list", "set":
			return "Sequence[" + arguments[0] + "]"
		case "map":
			return "Mapping[str, " + arguments[0] + "]"
		case "tuple":
			return "tuple[" + strings.Join(arguments, ", ") + "]"
		}
	}
	switch expression {
	case "string":
		return "str"
	case "bool":
		return "bool"
	case "int", "int32", "int64", "uint32", "uint64", "size":
		return "int"
	case "float32", "float64":
		return "float"
	case "bytes":
		return "bytes"
	case "decimal":
		return "Decimal"
	case "uuid":
		return "UUID"
	case "date":
		return "Date"
	case "datetime":
		return "DateTime"
	case "duration":
		return "Duration"
	case "url":
		return "URL"
	case "relative_path":
		return "RelativePath"
	case "json":
		return "JsonValue"
	case "std.type.problem":
		return "Problem"
	case "std.type.execution_receipt":
		return "ExecutionReceipt"
	case "std.type.unit":
		return "Mapping[str, Any]"
	default:
		return "Any"
	}
}

func (p pythonProjection) renderShape(source *strings.Builder, resource Resource) []string {
	name := p.name(resource)
	switch resource.Kind {
	case "scenery.record":
		fields := namedChildren(resource.Spec, "field")
		var required, optional []map[string]any
		for _, f := range fields {
			if isOptionalType(f["type"]) {
				optional = append(optional, f)
			} else {
				required = append(required, f)
			}
		}
		fmt.Fprintf(source, "@dataclass(frozen=True, slots=True)\nclass %s:\n", name)
		hasFields := false
		for _, f := range required {
			hasFields = true
			fmt.Fprintf(source, "    %s: %s\n", pythonFieldName(stringValue(f["name"])), p.typeExpression(f["type"], resource.Module))
		}
		for _, f := range optional {
			hasFields = true
			fmt.Fprintf(source, "    %s: %s = MISSING\n", pythonFieldName(stringValue(f["name"])), p.typeExpression(f["type"], resource.Module))
		}
		if resource.Spec["unknown_fields"] == "preserve" {
			hasFields = true
			source.WriteString("    unknown_fields: Mapping[str, JsonValue] = field(default_factory=dict)\n")
		}
		if !hasFields {
			source.WriteString("    pass\n")
		}
		source.WriteString("\n")
		return []string{name}
	case "scenery.enum":
		values := namedChildren(resource.Spec, "value")
		var literals []string
		for _, item := range values {
			literals = append(literals, strconv.Quote(wireName(item, stringValue(item["name"]))))
		}
		if resource.Spec["open"] == true || len(literals) == 0 {
			fmt.Fprintf(source, "%s: TypeAlias = str\n\n", name)
		} else {
			fmt.Fprintf(source, "%s: TypeAlias = Literal[%s]\n\n", name, strings.Join(literals, ", "))
		}
		return []string{name}
	case "scenery.union":
		var names, variants []string
		for _, variant := range namedChildren(resource.Spec, "variant") {
			vName := stringValue(variant["name"])
			wire := wireName(variant, vName)
			cls := name + goName(vName)
			names = append(names, cls)
			variants = append(variants, cls)
			fmt.Fprintf(source, "@dataclass(frozen=True, slots=True)\nclass %s:\n"+
				"    value: %s\n"+
				"    kind: Literal[%q] = %q\n\n",
				cls, p.typeExpression(variant["type"], resource.Module), wire, wire)
		}
		if resource.Spec["open"] == true {
			cls := name + "Unknown"
			names = append(names, cls)
			variants = append(variants, cls)
			fmt.Fprintf(source, "@dataclass(frozen=True, slots=True)\nclass %s:\n"+
				"    kind: str\n"+
				"    value: Mapping[str, JsonValue]\n"+
				"    unknown: Literal[True] = True\n\n", cls)
		}
		if len(variants) == 0 {
			fmt.Fprintf(source, "%s: TypeAlias = Any\n\n", name)
		} else {
			fmt.Fprintf(source, "%s: TypeAlias = %s\n\n", name, strings.Join(variants, " | "))
		}
		names = append(names, name)
		return names
	}
	return nil
}

func pythonNamedDescriptor(resource Resource) any {
	if resource.Kind != "scenery.record" {
		return tsNamedDescriptor(resource)
	}
	fields := namedChildren(resource.Spec, "field")
	slices.SortFunc(fields, func(a, b map[string]any) int { return strings.Compare(stringValue(a["name"]), stringValue(b["name"])) })
	descriptors := make([]any, 0, len(fields))
	for _, field := range fields {
		name := stringValue(field["name"])
		descriptor := map[string]any{
			"property": pythonFieldName(name),
			"wire":     wireName(field, name),
			"value":    tsDescriptor(field["type"], resource.Module),
			"optional": isOptionalType(field["type"]),
		}
		if constraints := tsFieldConstraints(field); len(constraints) > 0 {
			descriptor["constraints"] = constraints
		}
		descriptors = append(descriptors, descriptor)
	}
	return map[string]any{
		"kind":            "record",
		"fields":          descriptors,
		"preserveUnknown": resource.Spec["unknown_fields"] == "preserve",
	}
}

func pythonFieldName(name string) string {
	snake := pythonSnake(name)
	if !compiler.IsValidPythonIdentifier(snake) {
		return snake + "_"
	}
	return snake
}

func pythonSnake(name string) string {
	var out strings.Builder
	for i, r := range name {
		switch {
		case r == '-' || r == '.' || r == '/':
			out.WriteByte('_')
		case r >= 'A' && r <= 'Z':
			if i > 0 {
				prev := name[i-1]
				if (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9') {
					out.WriteByte('_')
				}
			}
			out.WriteByte(byte(r + ('a' - 'A')))
		default:
			out.WriteRune(r)
		}
	}
	result := out.String()
	if result == "" {
		return "field"
	}
	return result
}

func pythonModuleName(entry string) string {
	trimmed := strings.TrimSuffix(filepath.ToSlash(filepath.Clean(entry)), ".py")
	return strings.ReplaceAll(trimmed, "/", ".")
}

func pythonOperationSignature(method, name string, streaming, streamOut bool) string {
	outcome := name + "Outcome"
	if streaming {
		outcome = outcome + " | Stream[" + outcome + "]"
	} else if streamOut {
		outcome = outcome + " | EventStream[" + outcome + ", " + name + "EventData]"
	}
	return fmt.Sprintf("def %s(self, ctx: Context, input: %sInput) -> %s | Awaitable[%s]", method, name, outcome, outcome)
}

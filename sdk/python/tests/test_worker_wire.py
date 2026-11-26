"""Conformance tests for the Python Worker ABI v1 wire codec, HPACK, and h2c."""

from __future__ import annotations

import asyncio
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest

SDK_ROOT = Path(__file__).resolve().parents[1]
if str(SDK_ROOT) not in sys.path:
    sys.path.insert(0, str(SDK_ROOT))

from scenery_sdk._h2c import H2Client, H2Server, H2ServerStream, H2StreamReset  # noqa: E402
from scenery_sdk._hpack import HPACKDecoder, encode_headers, huffman_decode  # noqa: E402
from scenery_sdk.codec import (  # noqa: E402
    decode_worker_value,
    encode_typed_json,
    outcome_codec,
    parse_exact_json,
)


class WorkerWireCorpusTests(unittest.TestCase):
    def test_worker_wire_v1_corpus(self) -> None:
        corpus = Path(__file__).resolve().parents[3] / "internal" / "contract" / "testdata" / "worker-wire-v1.json"
        examples = json.loads(corpus.read_text(encoding="utf-8"))
        self.assertEqual(len(examples), 33)

        patch = {"kind": "named", "name": "probe/record/patch"}
        registry = {
            "probe/record/patch": {
                "kind": "record",
                "preserveUnknown": False,
                "fields": [
                    {
                        "property": "note",
                        "wire": "note",
                        "optional": True,
                        "value": {"kind": "nullable", "value": {"kind": "primitive", "name": "string"}},
                    }
                ],
            },
            "probe/enum/status": {"kind": "enum", "values": ["in_review"], "open": False},
            "probe/record/note": {
                "kind": "record",
                "preserveUnknown": True,
                "fields": [
                    {
                        "property": "note",
                        "wire": "note",
                        "optional": True,
                        "value": {"kind": "nullable", "value": {"kind": "primitive", "name": "string"}},
                    },
                    {
                        "property": "ratio",
                        "wire": "ratio",
                        "optional": True,
                        "value": {"kind": "primitive", "name": "float64"},
                    },
                    {
                        "property": "text",
                        "wire": "text",
                        "optional": False,
                        "value": {"kind": "primitive", "name": "string"},
                    },
                ],
            },
        }
        outcome = outcome_codec(
            registry,
            {
                "result.ok": patch,
                "error.negative_amount": {"kind": "primitive", "name": "problem"},
            },
        )

        for item in examples:
            name = item["name"]
            typ = item["type"]
            wire = item["wire"]
            reject = bool(item.get("reject", False))

            def roundtrip() -> str:
                incoming = parse_exact_json(wire)
                if typ == "probe/outcome/quote":
                    return outcome.encode(outcome.decode(incoming))
                descriptor = {"kind": "named", "name": typ} if typ in registry else {"kind": "primitive", "name": typ}
                val = decode_worker_value(incoming, descriptor, registry)
                return encode_typed_json(val, descriptor, registry)

            with self.subTest(case=name):
                if reject:
                    with self.assertRaises((ValueError, TypeError)):
                        roundtrip()
                else:
                    self.assertEqual(roundtrip(), wire)


class HPACKAndH2CTests(unittest.TestCase):
    def test_hpack_huffman_and_dynamic_table(self) -> None:
        # RFC 7541 Appendix C.4.1 (Huffman-encoded request with dynamic table indexing).
        block = bytes.fromhex(
            "828684418cf1e3c2e5f23a6ba0ab90f4ff"
        )
        decoder = HPACKDecoder()
        headers = decoder.decode(block)
        self.assertEqual(
            headers,
            [
                (":method", "GET"),
                (":scheme", "http"),
                (":path", "/"),
                (":authority", "www.example.com"),
            ],
        )
        # Second request references dynamic entry 62 (":authority", "www.example.com").
        block2 = bytes.fromhex("828684be5886a8eb10649cbf")
        self.assertEqual(
            decoder.decode(block2),
            [
                (":method", "GET"),
                (":scheme", "http"),
                (":path", "/"),
                (":authority", "www.example.com"),
                ("cache-control", "no-cache"),
            ],
        )
        # Roundtrip our encoder through our decoder.
        sample = [(":method", "POST"), (":path", "/worker/v1/invoke"), ("x-worker-scope", "abc123")]
        self.assertEqual(HPACKDecoder().decode(encode_headers(sample)), sample)
        # Verify Huffman decode of "no-cache".
        self.assertEqual(huffman_decode(bytes.fromhex("a8eb10649cbf")), b"no-cache")

    def test_h2c_uds_roundtrip_streaming_and_cancellation(self) -> None:
        async def run() -> None:
            canceled = asyncio.Event()
            with tempfile.TemporaryDirectory(prefix="pyh2c-") as tmpdir:
                sock = os.path.join(tmpdir, "worker.sock")

                async def handler(stream: H2ServerStream) -> None:
                    path = stream.headers.get(":path")
                    if path == "/echo":
                        data = await stream.body.read_all(1 << 20)
                        await stream.respond(200, {"content-type": "application/octet-stream"}, data)
                    elif path == "/stream":
                        chunk = b"P" * (64 * 1024)
                        await stream.respond(200, {"x-size": str(len(chunk) * 2)}, b"", end_stream=False)
                        await stream.write_chunk(chunk, end_stream=False)
                        await stream.write_chunk(chunk, end_stream=True)
                    elif path == "/slow":
                        try:
                            await asyncio.sleep(30)
                        except asyncio.CancelledError:
                            canceled.set()
                            raise

                server = H2Server(sock, handler)
                await server.start()
                client = H2Client(sock)
                try:
                    # Echo request/response.
                    resp = await client.request("POST", "/echo", {"x-test": "1"}, b"hello h2c")
                    self.assertEqual(resp.status, 200)
                    self.assertEqual(await resp.body.read_all(1024), b"hello h2c")

                    # Flow-controlled 128 KiB streaming response (> 65535 default window).
                    sresp = await client.request("GET", "/stream", {})
                    self.assertEqual(sresp.status, 200)
                    payload = await sresp.body.read_all(1 << 20)
                    self.assertEqual(len(payload), 128 * 1024)
                    self.assertEqual(payload, b"P" * (128 * 1024))

                    # Stream cancellation propagates RST_STREAM to handler.
                    slow_task = asyncio.create_task(client.request("GET", "/slow", {}))
                    await asyncio.sleep(0.05)
                    slow_task.cancel()
                    with self.assertRaises((asyncio.CancelledError, H2StreamReset)):
                        await slow_task
                    await asyncio.wait_for(canceled.wait(), timeout=2.0)

                    # Failing async request-body generator resets stream and raises
                    # promptly instead of hanging while awaiting response headers.
                    async def failing_body():
                        yield b"partial"
                        raise ValueError("producer boom")

                    with self.assertRaisesRegex(ValueError, "producer boom"):
                        await asyncio.wait_for(
                            client.request("POST", "/echo", {}, failing_body()),
                            timeout=2.0,
                        )

                    # Send windows must be reclaimed after completed requests
                    # (including empty responses ended directly in HEADERS).
                    for _ in range(50):
                        r1 = await client.request("POST", "/echo", {}, b"ok")
                        self.assertEqual(await r1.body.read_all(64), b"ok")
                        r2 = await client.request("POST", "/echo", {}, b"")
                        self.assertEqual(await r2.body.read_all(64), b"")
                    self.assertIsNotNone(client._conn)
                    self.assertEqual(len(client._conn._stream_send_windows), 0)
                finally:
                    client.close()
                    await server.drain_and_close()

        asyncio.run(run())

    def test_worker_event_stream_framing(self) -> None:
        from scenery_sdk import EncodedEventStream, serve

        async def run() -> None:
            with tempfile.TemporaryDirectory(prefix="pyev-", dir="/tmp") as tmpdir:
                sock = os.path.join(tmpdir, "w.sock")
                token = "t" * 32
                identity = {
                    "abi": 1,
                    "service": "app/service/ticker",
                    "contract_revision": "sha256:c",
                    "implementation_revision": "sha256:i",
                }
                launch = {
                    "worker_socket": sock,
                    "host_socket": sock,
                    "token": token,
                    "identity": identity,
                }

                async def dispatch(_op: str, _inp: Any, _ctx: Any) -> tuple[str, Any]:
                    async def lines():
                        yield '{"data":{"seq":"1"},"id":"1","event":"tick"}'
                        yield '{"data":{"seq":"2"}}'

                    return '{"kind":"result","name":"event","value":null}', EncodedEventStream(lines())

                task = asyncio.create_task(serve(launch, dispatch))
                for _ in range(50):
                    if os.path.exists(sock):
                        break
                    await asyncio.sleep(0.01)
                client = H2Client(sock)
                try:
                    inv = json.dumps({
                        "service": identity["service"],
                        "operation": "app/operation/watch",
                        "mode": "direct",
                        "context": {
                            "invocation_id": "inv-1",
                            "scope": "s-1",
                            "generation": 1,
                            "contract_revision": identity["contract_revision"],
                        },
                        "input": {},
                    }).encode("utf-8")
                    resp = await client.request(
                        "POST",
                        "/worker/v1/invoke",
                        {"authorization": "Bearer " + token, "content-type": "application/json"},
                        inv,
                    )
                    self.assertEqual(resp.status, 200)
                    self.assertEqual(resp.headers.get("content-type"), "application/vnd.worker.events")
                    raw = await resp.body.read_all(1 << 20)
                    meta_len = int.from_bytes(raw[:4], "big")
                    meta = json.loads(raw[4 : 4 + meta_len].decode("utf-8"))
                    self.assertEqual(meta, {"outcome": {"kind": "result", "name": "event", "value": None}})
                    lines_out = raw[4 + meta_len :].decode("utf-8").splitlines()
                    self.assertEqual(lines_out, ['{"data":{"seq":"1"},"id":"1","event":"tick"}', '{"data":{"seq":"2"}}'])
                finally:
                    client.close()
                    task.cancel()
                    try:
                        await task
                    except asyncio.CancelledError:
                        pass

        asyncio.run(run())


if __name__ == "__main__":
    unittest.main()

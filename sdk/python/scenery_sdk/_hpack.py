"""Minimal RFC 7541 HPACK encoder and decoder using only the Python stdlib."""

from __future__ import annotations

from collections import deque
from typing import Iterable

# RFC 7541 Appendix A: Static Table Definition (1-indexed).
STATIC_TABLE: tuple[tuple[str, str], ...] = (
    ("", ""),  # unused index 0
    (":authority", ""),
    (":method", "GET"),
    (":method", "POST"),
    (":path", "/"),
    (":path", "/index.html"),
    (":scheme", "http"),
    (":scheme", "https"),
    (":status", "200"),
    (":status", "204"),
    (":status", "206"),
    (":status", "304"),
    (":status", "400"),
    (":status", "404"),
    (":status", "500"),
    ("accept-charset", ""),
    ("accept-encoding", "gzip, deflate"),
    ("accept-language", ""),
    ("accept-ranges", ""),
    ("accept", ""),
    ("access-control-allow-origin", ""),
    ("age", ""),
    ("allow", ""),
    ("authorization", ""),
    ("cache-control", ""),
    ("content-disposition", ""),
    ("content-encoding", ""),
    ("content-language", ""),
    ("content-length", ""),
    ("content-location", ""),
    ("content-range", ""),
    ("content-type", ""),
    ("cookie", ""),
    ("date", ""),
    ("etag", ""),
    ("expect", ""),
    ("expires", ""),
    ("from", ""),
    ("host", ""),
    ("if-match", ""),
    ("if-modified-since", ""),
    ("if-none-match", ""),
    ("if-range", ""),
    ("if-unmodified-since", ""),
    ("last-modified", ""),
    ("link", ""),
    ("location", ""),
    ("max-forwards", ""),
    ("proxy-authenticate", ""),
    ("proxy-authorization", ""),
    ("range", ""),
    ("referer", ""),
    ("refresh", ""),
    ("retry-after", ""),
    ("server", ""),
    ("set-cookie", ""),
    ("strict-transport-security", ""),
    ("transfer-encoding", ""),
    ("user-agent", ""),
    ("vary", ""),
    ("via", ""),
    ("www-authenticate", ""),
)

_STATIC_EXACT: dict[tuple[str, str], int] = {}
_STATIC_NAME: dict[str, int] = {}
for _idx in range(1, len(STATIC_TABLE)):
    _k, _v = STATIC_TABLE[_idx]
    _STATIC_EXACT.setdefault((_k, _v), _idx)
    _STATIC_NAME.setdefault(_k, _idx)

# RFC 7541 Appendix B: Huffman Code Table (code, bit_length) for symbols 0..255.
_HUFFMAN_TABLE: tuple[tuple[int, int], ...] = (
    (0x1FF8, 13), (0x7FFF8, 19), (0xFFFFFE2, 28), (0xFFFFFE3, 28),
    (0xFFFFFE4, 28), (0xFFFFFE5, 28), (0xFFFFFE6, 28), (0xFFFFFE7, 28),
    (0xFFFFFE8, 28), (0xFFFFEA, 24), (0x3FFFFFFC, 30), (0xFFFFFE9, 28),
    (0xFFFFFEA, 28), (0x3FFFFFFD, 30), (0xFFFFFEB, 28), (0xFFFFFEC, 28),
    (0xFFFFFED, 28), (0xFFFFFEE, 28), (0xFFFFFEF, 28), (0xFFFFFF0, 28),
    (0xFFFFFF1, 28), (0xFFFFFF2, 28), (0x3FFFFFFE, 30), (0xFFFFFF3, 28),
    (0xFFFFFF4, 28), (0xFFFFFF5, 28), (0xFFFFFF6, 28), (0xFFFFFF7, 28),
    (0xFFFFFF8, 28), (0xFFFFFF9, 28), (0xFFFFFFA, 28), (0xFFFFFFB, 28),
    (0x14, 6), (0x3F8, 10), (0x3F9, 10), (0xFFA, 12),
    (0x1FF9, 13), (0x15, 6), (0xF8, 8), (0x7FA, 11),
    (0x3FA, 10), (0x3FB, 10), (0xF9, 8), (0x7FB, 11),
    (0xFA, 8), (0x16, 6), (0x17, 6), (0x18, 6),
    (0x0, 5), (0x1, 5), (0x2, 5), (0x19, 6),
    (0x1A, 6), (0x1B, 6), (0x1C, 6), (0x1D, 6),
    (0x1E, 6), (0x1F, 6), (0x5C, 7), (0xFB, 8),
    (0x7FFC, 15), (0x20, 6), (0xFFB, 12), (0x3FC, 10),
    (0x1FFA, 13), (0x21, 6), (0x5D, 7), (0x5E, 7),
    (0x5F, 7), (0x60, 7), (0x61, 7), (0x62, 7),
    (0x63, 7), (0x64, 7), (0x65, 7), (0x66, 7),
    (0x67, 7), (0x68, 7), (0x69, 7), (0x6A, 7),
    (0x6B, 7), (0x6C, 7), (0x6D, 7), (0x6E, 7),
    (0x6F, 7), (0x70, 7), (0x71, 7), (0x72, 7),
    (0xFC, 8), (0x73, 7), (0xFD, 8), (0x1FFB, 13),
    (0x7FFF0, 19), (0x1FFC, 13), (0x3FFC, 14), (0x22, 6),
    (0x7FFD, 15), (0x3, 5), (0x23, 6), (0x4, 5),
    (0x24, 6), (0x5, 5), (0x25, 6), (0x26, 6),
    (0x27, 6), (0x6, 5), (0x74, 7), (0x75, 7),
    (0x28, 6), (0x29, 6), (0x2A, 6), (0x7, 5),
    (0x2B, 6), (0x76, 7), (0x2C, 6), (0x8, 5),
    (0x9, 5), (0x2D, 6), (0x77, 7), (0x78, 7),
    (0x79, 7), (0x7A, 7), (0x7B, 7), (0x7FFE, 15),
    (0x7FC, 11), (0x3FFD, 14), (0x1FFD, 13), (0xFFFFFFC, 28),
    (0xFFFE6, 20), (0x3FFFFD2, 26), (0xFFFE7, 20), (0xFFFE8, 20),
    (0x3FFFFD3, 26), (0x3FFFFD4, 26), (0x3FFFFD5, 26), (0x7FFFDD, 23),
    (0x3FFFFD6, 26), (0x7FFFDE, 23), (0x7FFFDF, 23), (0x7FFFE0, 23),
    (0x7FFFE1, 23), (0x7FFFE2, 23), (0x7FFFE3, 23), (0x7FFFE4, 23),
    (0x1FFFDC, 21), (0x3FFF7E, 22), (0x7FFFE5, 23), (0x3FFF7F, 22),
    (0x7FFFE6, 23), (0x7FFFE7, 23), (0x7FFFE8, 23), (0x7FFFE9, 23),
    (0x7FFFEA, 23), (0x7FFFEB, 23), (0x3FFF80, 22), (0x7FFFEC, 23),
    (0x7FFFED, 23), (0x7FFFEE, 23), (0x7FFFEF, 23), (0x7FFFF0, 23),
    (0x3FFF81, 22), (0x3FFF82, 22), (0x7FFFF1, 23), (0x3FFF83, 22),
    (0x3FFF84, 22), (0x7FFFF2, 23), (0x7FFFF3, 23), (0x7FFFF4, 23),
    (0x7FFFF5, 23), (0x3FFF85, 22), (0x7FFFF6, 23), (0x7FFFF7, 23),
    (0x3FFF86, 22), (0x7FFFF8, 23), (0x7FFFF9, 23), (0x7FFFFA, 23),
    (0x3FFF87, 22), (0x3FFF88, 22), (0x3FFF89, 22), (0x3FFF8A, 22),
    (0x7FFFFB, 23), (0x3FFF8B, 22), (0x7FFFFC, 23), (0x7FFFFD, 23),
    (0x3FFF8C, 22), (0x3FFF8D, 22), (0x3FFF8E, 22), (0x3FFF8F, 22),
    (0x7FFFFE, 23), (0x3FFF90, 22), (0x3FFF91, 22), (0x7FFFFF, 23),
    (0x3FFF92, 22), (0x3FFF93, 22), (0x3FFF94, 22), (0x3FFF95, 22),
    (0x3FFF96, 22), (0x3FFF97, 22), (0x3FFF98, 22), (0x3FFF99, 22),
    (0x3FFF9A, 22), (0x3FFF9B, 22), (0x3FFF9C, 22), (0x3FFF9D, 22),
    (0x3FFF9E, 22), (0x3FFF9F, 22), (0x3FFFA0, 22), (0x3FFFA1, 22),
    (0x3FFFA2, 22), (0x3FFFA3, 22), (0x3FFFA4, 22), (0x3FFFA5, 22),
    (0x3FFFA6, 22), (0x3FFFA7, 22), (0x3FFFA8, 22), (0x3FFFA9, 22),
    (0x3FFFAA, 22), (0x3FFFAB, 22), (0x3FFFAC, 22), (0x3FFFAD, 22),
    (0x3FFFAE, 22), (0x3FFFAF, 22), (0x3FFFB0, 22), (0x3FFFB1, 22),
    (0x3FFFB2, 22), (0x3FFFB3, 22), (0x3FFFB4, 22), (0x3FFFB5, 22),
    (0x3FFFB6, 22), (0x3FFFB7, 22), (0x3FFFB8, 22), (0x3FFFB9, 22),
    (0x3FFFBA, 22), (0x3FFFBB, 22), (0x3FFFBC, 22), (0x3FFFBD, 22),
    (0x3FFFBE, 22), (0x3FFFBF, 22), (0x3FFFC0, 22), (0x3FFFC1, 22),
    (0x3FFFC2, 22), (0x3FFFC3, 22), (0x3FFFC4, 22), (0x3FFFC5, 22),
    (0x3FFFC6, 22), (0x3FFFC7, 22), (0x3FFFC8, 22), (0x3FFFC9, 22),
    (0x3FFFCA, 22), (0x3FFFCB, 22), (0x3FFFCC, 22), (0x3FFFCD, 22),
    (0x3FFFCE, 22), (0x3FFFCF, 22), (0x3FFFD0, 22), (0x3FFFD1, 22),
)


class HPACKError(Exception):
    """Raised when an HPACK header block is malformed."""


def _build_huffman_tree() -> list[list[int]]:
    # Binary trie encoded as list of [left, right, symbol] nodes.
    nodes: list[list[int]] = [[-1, -1, -1]]
    for sym, (code, nbits) in enumerate(_HUFFMAN_TABLE):
        node = 0
        for shift in range(nbits - 1, -1, -1):
            bit = (code >> shift) & 1
            nxt = nodes[node][bit]
            if nxt == -1:
                nxt = len(nodes)
                nodes.append([-1, -1, -1])
                nodes[node][bit] = nxt
            node = nxt
        nodes[node][2] = sym
    return nodes


_HUFFMAN_TREE = _build_huffman_tree()


def huffman_decode(data: bytes) -> bytes:
    """Decode RFC 7541 Huffman-encoded bytes."""
    out = bytearray()
    node = 0
    depth = 0
    all_ones = True
    tree = _HUFFMAN_TREE
    for byte in data:
        for shift in range(7, -1, -1):
            bit = (byte >> shift) & 1
            if bit == 0:
                all_ones = False
            nxt = tree[node][bit]
            if nxt == -1:
                raise HPACKError("invalid HPACK Huffman code")
            node = nxt
            depth += 1
            sym = tree[node][2]
            if sym != -1:
                out.append(sym)
                node = 0
                depth = 0
                all_ones = True
    if depth > 7 or (depth > 0 and not all_ones):
        raise HPACKError("invalid HPACK Huffman padding")
    return bytes(out)


def _encode_int(value: int, prefix_bits: int, prefix_mask: int) -> bytes:
    max_prefix = (1 << prefix_bits) - 1
    if value < max_prefix:
        return bytes([prefix_mask | value])
    out = bytearray([prefix_mask | max_prefix])
    value -= max_prefix
    while value >= 128:
        out.append((value & 0x7F) | 0x80)
        value >>= 7
    out.append(value)
    return bytes(out)


def _decode_int(data: memoryview, pos: int, prefix_bits: int) -> tuple[int, int]:
    if pos >= len(data):
        raise HPACKError("truncated HPACK integer")
    max_prefix = (1 << prefix_bits) - 1
    value = data[pos] & max_prefix
    pos += 1
    if value < max_prefix:
        return value, pos
    shift = 0
    while True:
        if pos >= len(data):
            raise HPACKError("truncated HPACK integer continuation")
        b = data[pos]
        pos += 1
        value += (b & 0x7F) << shift
        if (b & 0x80) == 0:
            return value, pos
        shift += 7
        if shift > 28:
            raise HPACKError("HPACK integer overflow")


def _encode_str(text: str) -> bytes:
    raw = text.encode("utf-8")
    return _encode_int(len(raw), 7, 0x00) + raw


def _decode_str(data: memoryview, pos: int) -> tuple[str, int]:
    if pos >= len(data):
        raise HPACKError("truncated HPACK string")
    huffman = (data[pos] & 0x80) != 0
    length, pos = _decode_int(data, pos, 7)
    end = pos + length
    if end > len(data):
        raise HPACKError("truncated HPACK string payload")
    raw = bytes(data[pos:end])
    if huffman:
        raw = huffman_decode(raw)
    try:
        return raw.decode("utf-8"), end
    except UnicodeDecodeError as exc:
        raise HPACKError("invalid UTF-8 in HPACK header") from exc


def encode_headers(headers: Iterable[tuple[str, str]]) -> bytes:
    """Encode headers using static index hits and literal-without-indexing entries."""
    out = bytearray()
    for name, value in headers:
        exact = _STATIC_EXACT.get((name, value))
        if exact is not None:
            out.extend(_encode_int(exact, 7, 0x80))
            continue
        name_idx = _STATIC_NAME.get(name)
        if name_idx is not None:
            out.extend(_encode_int(name_idx, 4, 0x00))
            out.extend(_encode_str(value))
            continue
        out.append(0x00)
        out.extend(_encode_str(name))
        out.extend(_encode_str(value))
    return bytes(out)


class HPACKDecoder:
    """Stateful RFC 7541 HPACK decoder maintaining the connection dynamic table."""

    def __init__(self, max_table_size: int = 4096) -> None:
        self._max_allowed = max_table_size
        self._max_size = max_table_size
        self._size = 0
        self._table: deque[tuple[str, str, int]] = deque()

    def set_max_table_size(self, size: int) -> None:
        self._max_allowed = size
        if self._max_size > size:
            self._max_size = size
            self._evict()

    def _entry_size(self, name: str, value: str) -> int:
        return len(name.encode("utf-8")) + len(value.encode("utf-8")) + 32

    def _evict(self) -> None:
        while self._size > self._max_size and self._table:
            _, _, sz = self._table.pop()
            self._size -= sz

    def _add(self, name: str, value: str) -> None:
        sz = self._entry_size(name, value)
        if sz > self._max_size:
            self._table.clear()
            self._size = 0
            return
        self._table.appendleft((name, value, sz))
        self._size += sz
        self._evict()

    def _get(self, index: int) -> tuple[str, str]:
        if index <= 0:
            raise HPACKError("HPACK index 0 is invalid")
        if index < len(STATIC_TABLE):
            return STATIC_TABLE[index]
        dyn = index - len(STATIC_TABLE)
        if dyn >= len(self._table):
            raise HPACKError(f"HPACK dynamic index {index} out of range")
        name, value, _ = self._table[dyn]
        return name, value

    def decode(self, block: bytes) -> list[tuple[str, str]]:
        view = memoryview(block)
        pos = 0
        headers: list[tuple[str, str]] = []
        allow_size_update = True
        while pos < len(view):
            first = view[pos]
            if first & 0x80:
                allow_size_update = False
                index, pos = _decode_int(view, pos, 7)
                headers.append(self._get(index))
            elif first & 0x40:
                allow_size_update = False
                index, pos = _decode_int(view, pos, 6)
                name, pos = (self._get(index)[0], pos) if index else _decode_str(view, pos)
                value, pos = _decode_str(view, pos)
                self._add(name, value)
                headers.append((name, value))
            elif first & 0x20:
                if not allow_size_update:
                    raise HPACKError("dynamic table size update after header")
                new_size, pos = _decode_int(view, pos, 5)
                if new_size > self._max_allowed:
                    raise HPACKError("dynamic table size exceeds SETTINGS limit")
                self._max_size = new_size
                self._evict()
            else:
                allow_size_update = False
                index, pos = _decode_int(view, pos, 4)
                name, pos = (self._get(index)[0], pos) if index else _decode_str(view, pos)
                value, pos = _decode_str(view, pos)
                headers.append((name, value))
        return headers

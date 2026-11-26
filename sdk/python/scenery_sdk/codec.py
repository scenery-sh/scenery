"""Canonical Worker ABI v1 JSON codec and validator for Python."""

from __future__ import annotations

import base64
from dataclasses import dataclass, fields as dc_fields, is_dataclass
import json
import math
import re
import struct
from typing import Any
from urllib.parse import urlsplit


class MissingType:
    """Sentinel distinguishing an absent optional field from explicit None."""

    __slots__ = ()

    def __repr__(self) -> str:
        return "MISSING"


MISSING = MissingType()


@dataclass(frozen=True, slots=True)
class Problem:
    """Declared domain error payload."""

    code: str
    message: str
    path: str | MissingType = MISSING


@dataclass(frozen=True, slots=True)
class ExecutionReceipt:
    """Durable enqueue receipt returned by Host durable/enqueue."""

    durable_identity: str
    execution_id: str
    accepted_revision: str
    status_url: str | MissingType = MISSING


@dataclass(frozen=True, slots=True)
class JsonNumber:
    """Exact JSON number preserving arbitrary precision and scale."""

    coefficient: str
    scale: int

    @classmethod
    def from_token(cls, token: str) -> "JsonNumber":
        m = _JSON_NUM_RE.fullmatch(token)
        if m is None:
            raise ValueError(f"invalid JSON number: {token!r}")
        sign, whole, frac, exp = m.group(1), m.group(2), m.group(3) or "", m.group(4) or "0"
        exponent = int(exp)
        if abs(exponent) > 1_000_000:
            raise ValueError("JSON number exponent out of range")
        coeff, scale = _normalize_json_number(f"{sign}{whole}{frac}", len(frac) - exponent)
        return cls(coeff, scale)

    def render(self) -> str:
        coeff, scale = _normalize_json_number(self.coefficient, self.scale)
        negative = coeff.startswith("-")
        digits = coeff[1:] if negative else coeff
        if scale == 0:
            return coeff
        prefix = "-" if negative else ""
        if scale >= len(digits):
            return f"{prefix}0.{'0' * (scale - len(digits))}{digits}"
        split = len(digits) - scale
        return f"{prefix}{digits[:split]}.{digits[split:]}"


_JSON_NUM_RE = re.compile(r"(-?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?")
_CANON_INT_RE = re.compile(r"-?(?:0|[1-9][0-9]*)")
_CANON_DEC_RE = re.compile(r"-?(?:0|[1-9][0-9]*)(?:\.[0-9]*[1-9])?")
_UUID_RE = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}")
_DATE_RE = re.compile(r"(\d{4})-(\d{2})-(\d{2})")
_DATETIME_RE = re.compile(r"(\d{4}-\d{2}-\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?Z")
_DURATION_RE = re.compile(r"-?P(?=\d+D|T\d)(?:\d+D)?(?:T(?=\d)(?:\d+H)?(?:\d+M)?(?:\d+(?:\.\d{1,9})?S)?)?")
_BASE64_RE = re.compile(r"(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?")
_URL_RE = re.compile(r"([a-z][a-z0-9+.-]*):\/\/([^/?#]+)(\/[^?#]*)?(?:\?[^#]*)?(?:#.*)?")
_USERINFO_RE = re.compile(r"[A-Za-z0-9._~!$&'()*+,;=:%-]+")
_IPV6_HOST_RE = re.compile(r"\[([0-9a-f:.]+)\](?::(\d+))?")
_REG_HOST_RE = re.compile(r"([a-z0-9.-]+)(?::(\d+))?")
_PERCENT_ESCAPE_RE = re.compile(r"%([0-9A-Fa-f]{2})")
_BAD_PERCENT_RE = re.compile(r"%(?![0-9A-F]{2})")
_UNRESERVED_RE = re.compile(r"[A-Za-z0-9._~-]")
_NON_VISIBLE_ASCII_RE = re.compile(r"[^\x21-\x7e]")
_FORBIDDEN_KEYS = frozenset({"__proto__", "prototype", "constructor"})


def _normalize_json_number(coefficient: str, scale: int) -> tuple[str, int]:
    if not re.fullmatch(r"-?[0-9]+", coefficient) or abs(scale) > 1_000_000:
        raise ValueError("invalid exact JSON number")
    negative = coefficient.startswith("-")
    digits = (coefficient[1:] if negative else coefficient).lstrip("0")
    if not digits:
        return "0", 0
    if scale < 0:
        digits += "0" * (-scale)
        scale = 0
    while scale > 0 and digits.endswith("0"):
        digits = digits[:-1]
        scale -= 1
    return (f"-{digits}" if negative else digits), scale


def _reject_constant(token: str) -> Any:
    raise ValueError(f"non-finite JSON constant: {token}")


def _reject_duplicate_keys(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    out: dict[str, Any] = {}
    for k, v in pairs:
        if k in out:
            raise ValueError(f"duplicate JSON object key: {k!r}")
        out[k] = v
    return out


def parse_exact_json(text: str) -> Any:
    """Parse JSON while preserving exact number tokens and rejecting duplicate keys."""
    if not isinstance(text, str):
        raise ValueError("expected JSON string")
    return json.loads(
        text,
        parse_float=JsonNumber.from_token,
        parse_int=JsonNumber.from_token,
        parse_constant=_reject_constant,
        object_pairs_hook=_reject_duplicate_keys,
    )


def _assert_unicode_scalar(value: str, path: str) -> None:
    try:
        value.encode("utf-8", "strict")
    except UnicodeEncodeError as exc:
        raise ValueError(f"{path}: unpaired Unicode surrogate") from exc


def _json_str(value: str, path: str = "$") -> str:
    _assert_unicode_scalar(value, path)
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))


def encode_exact_json(value: Any, path: str = "$") -> str:
    """Encode an exact JSON value with sorted keys and exact numeric tokens."""
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, str):
        return _json_str(value, path)
    if isinstance(value, JsonNumber):
        return value.render()
    if isinstance(value, int):
        return str(value)
    if isinstance(value, float):
        if not math.isfinite(value) or math.copysign(1.0, value) < 0 and value == 0.0:
            raise ValueError(f"{path}: invalid JSON float")
        return format_float(value, 64)
    if isinstance(value, (list, tuple)):
        return "[" + ",".join(encode_exact_json(v, f"{path}[{i}]") for i, v in enumerate(value)) + "]"
    if isinstance(value, dict):
        members: list[str] = []
        for k in sorted(value.keys()):
            if not isinstance(k, str) or k in _FORBIDDEN_KEYS:
                raise ValueError(f"{path}: invalid object key")
            members.append(f"{_json_str(k, path)}:{encode_exact_json(value[k], f'{path}.{k}')}")
        return "{" + ",".join(members) + "}"
    raise ValueError(f"{path}: invalid exact JSON value")


def _valid_calendar_date(value: str) -> bool:
    m = _DATE_RE.fullmatch(value)
    if m is None:
        return False
    year, month, day = int(m.group(1)), int(m.group(2)), int(m.group(3))
    if month < 1 or month > 12 or day < 1:
        return False
    leap = (year % 4 == 0 and year % 100 != 0) or (year % 400 == 0)
    days = (31, 29 if leap else 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31)
    return day <= days[month - 1]


def _valid_datetime(value: str) -> bool:
    m = _DATETIME_RE.fullmatch(value)
    if m is None or not _valid_calendar_date(m.group(1)):
        return False
    hour, minute, second = int(m.group(2)), int(m.group(3)), int(m.group(4))
    if hour > 23 or minute > 59 or second > 59:
        return False
    frac = m.group(5)
    return frac is None or not frac.endswith("0")


def _valid_canonical_url(value: str) -> bool:
    if _NON_VISIBLE_ASCII_RE.search(value) or "\\" in value:
        return False
    try:
        parsed = urlsplit(value)
        _ = parsed.port
    except ValueError:
        return False
    m = _URL_RE.fullmatch(value)
    if m is None:
        return False
    scheme, authority, path = m.group(1), m.group(2), m.group(3) or "/"
    at = authority.rfind("@")
    userinfo = "" if at < 0 else authority[:at]
    host_auth = authority if at < 0 else authority[at + 1 :]
    if userinfo and not _USERINFO_RE.fullmatch(userinfo):
        return False
    if "%" in host_auth:
        return False
    hm = _IPV6_HOST_RE.fullmatch(host_auth) or _REG_HOST_RE.fullmatch(host_auth)
    if hm is None or not hm.group(1) or ".." in hm.group(1):
        return False
    port = hm.group(2)
    if (scheme == "http" and port == "80") or (scheme == "https" and port == "443"):
        return False
    if any(seg in (".", "..") for seg in path.split("/")):
        return False
    for esc in _PERCENT_ESCAPE_RE.finditer(value):
        hx = esc.group(1)
        if hx != hx.upper() or _UNRESERVED_RE.fullmatch(chr(int(hx, 16))):
            return False
    return _BAD_PERCENT_RE.search(value) is None


def _f32(value: float) -> float:
    return struct.unpack("!f", struct.pack("!f", value))[0]


def _is_neg_zero(value: float) -> bool:
    return value == 0.0 and math.copysign(1.0, value) < 0.0


def _shortest_float32_exp(value: float) -> str:
    """Return shortest exponential decimal reading back as the same float32 (ties-to-even)."""
    if value == 0.0:
        return "0e+00"
    sign = "-" if value < 0 else ""
    av = abs(value)
    for precision in range(1, 10):
        rounded = f"{av:.{precision - 1}e}"
        mant, exp_s = rounded.split("e")
        exp = int(exp_s)
        scale = exp - precision + 1
        nearest = int(mant.replace(".", ""))
        nearest_val = float(f"{nearest}e{scale}")
        other = nearest + (1 if nearest_val < av else -1)

        def _reads(units: int) -> bool:
            if units <= 0:
                return False
            try:
                return _f32(float(f"{units}e{scale}")) == av
            except OverflowError:
                return False

        ok_n = _reads(nearest)
        ok_o = _reads(other)
        chosen: int | None = None
        if ok_n and ok_o:
            full = f"{av:.99e}"
            exact_digits = full.split("e")[0].replace(".", "")
            tie = len(exact_digits) > precision and exact_digits[precision] == "5" and set(
                exact_digits[precision + 1 :]
            ) <= {"0"}
            chosen = other if (tie and nearest % 2 != 0) else nearest
        elif ok_n:
            chosen = nearest
        elif ok_o:
            chosen = other
        if chosen is not None:
            digits = str(chosen).rstrip("0") or "0"
            new_exp = scale + len(str(chosen)) - 1
            frac = f".{digits[1:]}" if len(digits) > 1 else ""
            return f"{sign}{digits[0]}{frac}e{new_exp:+03d}"
    return f"{value:.8e}"


def format_float(value: float, bits: int) -> str:
    """Format finite non-negative-zero float matching Go strconv.FormatFloat(v, 'g', -1, bits)."""
    if not math.isfinite(value) or _is_neg_zero(value):
        raise ValueError("non-finite or negative-zero float")
    if value == 0.0:
        return "0"
    if bits == 32:
        text = _shortest_float32_exp(value)
    else:
        # Python repr(float) uses shortest round-trip decimal digits.
        raw = repr(value)
        if "e" in raw or "E" in raw:
            mant, exp_s = raw.lower().split("e")
            sign = "-" if mant.startswith("-") else ""
            digits = (mant[1:] if sign else mant).replace(".", "").rstrip("0") or "0"
            exp = int(exp_s)
        else:
            sign = "-" if raw.startswith("-") else ""
            body = raw[1:] if sign else raw
            if "." in body:
                whole, frac = body.split(".")
            else:
                whole, frac = body, ""
            if whole != "0":
                digits = (whole + frac).rstrip("0")
                exp = len(whole) - 1
            else:
                stripped = frac.lstrip("0")
                zeros = len(frac) - len(stripped)
                digits = stripped.rstrip("0")
                exp = -(zeros + 1)
        frac = f".{digits[1:]}" if len(digits) > 1 else ""
        text = f"{sign}{digits[0]}{frac}e{exp:+03d}"

    mant, exp_s = text.split("e")
    sign = "-" if mant.startswith("-") else ""
    digits = (mant[1:] if sign else mant).replace(".", "")
    exp = int(exp_s)
    if exp < -4 or exp >= 6:
        frac = f".{digits[1:]}" if len(digits) > 1 else ""
        return f"{sign}{digits[0]}{frac}e{'-' if exp < 0 else '+'}{abs(exp):02d}"
    if exp < 0:
        return f"{sign}0.{'0' * (-exp - 1)}{digits}"
    if len(digits) <= exp + 1:
        return f"{sign}{digits}{'0' * (exp + 1 - len(digits))}"
    return f"{sign}{digits[: exp + 1]}.{digits[exp + 1 :]}"


def _exact_number_parts(value: Any, path: str) -> tuple[str, int]:
    if isinstance(value, JsonNumber):
        return _normalize_json_number(value.coefficient, value.scale)
    if isinstance(value, bool):
        raise ValueError(f"{path}: numeric constraint requires a number")
    if isinstance(value, int):
        return str(value), 0
    if isinstance(value, float):
        if not math.isfinite(value) or _is_neg_zero(value):
            raise ValueError(f"{path}: numeric constraint requires a finite number")
        jn = JsonNumber.from_token(repr(value))
        return jn.coefficient, jn.scale
    if isinstance(value, str) and _JSON_NUM_RE.fullmatch(value):
        jn = JsonNumber.from_token(value)
        return jn.coefficient, jn.scale
    raise ValueError(f"{path}: numeric constraint requires a number")


def _compare_exact_numbers(value: Any, limit: str, path: str) -> int:
    lc, ls = _exact_number_parts(value, path)
    rc, rs = _exact_number_parts(limit, path)
    scale = max(ls, rs)
    lv = int(lc) * (10 ** (scale - ls))
    rv = int(rc) * (10 ** (scale - rs))
    return -1 if lv < rv else (1 if lv > rv else 0)


def _validate_constraints(
    value: Any,
    constraints: dict[str, Any] | None,
    descriptor: dict[str, Any],
    registry: dict[str, Any],
    path: str,
) -> None:
    if not constraints or value is None or isinstance(value, MissingType):
        return
    length: int | None = None
    if isinstance(value, (str, list, tuple, dict)):
        length = len(value)
    min_len = constraints.get("min_length", constraints.get("min_items"))
    max_len = constraints.get("max_length", constraints.get("max_items"))
    if min_len is not None and (length is None or length < int(min_len)):
        raise ValueError(f"{path}: minimum length constraint failed")
    if max_len is not None and (length is None or length > int(max_len)):
        raise ValueError(f"{path}: maximum length constraint failed")
    if "pattern" in constraints:
        if not isinstance(value, str) or re.search(constraints["pattern"], value) is None:
            raise ValueError(f"{path}: pattern constraint failed")
    if "format" in constraints and isinstance(value, str):
        fmt = constraints["format"]
        if fmt not in ("uuid", "date", "datetime", "duration", "url", "relative_path"):
            raise ValueError(f"{path}: unknown string format")
        _encode_primitive(value, fmt, path)
    if "minimum" in constraints and _compare_exact_numbers(value, str(constraints["minimum"]), path) < 0:
        raise ValueError(f"{path}: minimum constraint failed")
    if "maximum" in constraints and _compare_exact_numbers(value, str(constraints["maximum"]), path) > 0:
        raise ValueError(f"{path}: maximum constraint failed")
    if constraints.get("unique_items") is True:
        if not isinstance(value, (list, tuple)):
            raise ValueError(f"{path}: unique_items requires a sequence")
        item_desc = descriptor["value"] if descriptor["kind"] in ("list", "set") else {"kind": "primitive", "name": "json"}
        encoded = [encode_typed_json(item, item_desc, registry, f"{path}[{i}]") for i, item in enumerate(value)]
        if len(set(encoded)) != len(encoded):
            raise ValueError(f"{path}: unique_items constraint failed")


def _encode_primitive(value: Any, name: str, path: str) -> str:
    if name == "json":
        return encode_exact_json(value, path)
    if name == "bool":
        if not isinstance(value, bool):
            raise ValueError(f"{path}: expected bool")
        return "true" if value else "false"
    if name == "int":
        if isinstance(value, bool) or not isinstance(value, int):
            raise ValueError(f"{path}: expected int")
        return _json_str(str(value), path)
    if name == "size":
        if isinstance(value, bool) or not isinstance(value, int) or value < 0:
            raise ValueError(f"{path}: expected size")
        return _json_str(str(value), path)
    if name == "int64":
        if isinstance(value, bool) or not isinstance(value, int) or value < -(1 << 63) or value > (1 << 63) - 1:
            raise ValueError(f"{path}: int64 out of range")
        return _json_str(str(value), path)
    if name == "uint64":
        if isinstance(value, bool) or not isinstance(value, int) or value < 0 or value > (1 << 64) - 1:
            raise ValueError(f"{path}: uint64 out of range")
        return _json_str(str(value), path)
    if name == "int32":
        if isinstance(value, bool) or not isinstance(value, int) or value < -(1 << 31) or value > (1 << 31) - 1:
            raise ValueError(f"{path}: int32 out of range")
        return str(value)
    if name == "uint32":
        if isinstance(value, bool) or not isinstance(value, int) or value < 0 or value > (1 << 32) - 1:
            raise ValueError(f"{path}: uint32 out of range")
        return str(value)
    if name == "float32":
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise ValueError(f"{path}: expected float32")
        f = float(value)
        if not math.isfinite(f) or _is_neg_zero(f):
            raise ValueError(f"{path}: invalid float32")
        try:
            if _f32(f) != f:
                raise ValueError(f"{path}: float32 value is not exactly representable")
        except OverflowError as exc:
            raise ValueError(f"{path}: float32 overflow") from exc
        return format_float(f, 32)
    if name == "float64":
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise ValueError(f"{path}: expected float64")
        f = float(value)
        if not math.isfinite(f) or _is_neg_zero(f):
            raise ValueError(f"{path}: invalid float64")
        return format_float(f, 64)
    if name == "bytes":
        if not isinstance(value, (bytes, bytearray)):
            raise ValueError(f"{path}: expected bytes")
        return _json_str(base64.b64encode(bytes(value)).decode("ascii"), path)
    if name == "decimal":
        if not isinstance(value, str) or value == "-0" or _CANON_DEC_RE.fullmatch(value) is None:
            raise ValueError(f"{path}: invalid canonical decimal")
        return _json_str(value, path)
    if name == "uuid":
        if not isinstance(value, str) or _UUID_RE.fullmatch(value) is None:
            raise ValueError(f"{path}: invalid UUID")
        return _json_str(value, path)
    if name == "date":
        if not isinstance(value, str) or not _valid_calendar_date(value):
            raise ValueError(f"{path}: invalid date")
        return _json_str(value, path)
    if name == "datetime":
        if not isinstance(value, str) or not _valid_datetime(value):
            raise ValueError(f"{path}: invalid normalized datetime")
        return _json_str(value, path)
    if name == "duration":
        if not isinstance(value, str) or _DURATION_RE.fullmatch(value) is None:
            raise ValueError(f"{path}: invalid duration")
        return _json_str(value, path)
    if name == "url":
        if not isinstance(value, str) or not _valid_canonical_url(value):
            raise ValueError(f"{path}: invalid canonical URL")
        return _json_str(value, path)
    if name == "relative_path":
        if (
            not isinstance(value, str)
            or not value
            or value.startswith("/")
            or "\\" in value
            or any(part in ("", ".", "..") for part in value.split("/"))
        ):
            raise ValueError(f"{path}: invalid relative path")
        return _json_str(value, path)
    if name == "string":
        if not isinstance(value, str):
            raise ValueError(f"{path}: expected string")
        return _json_str(value, path)
    if name == "problem":
        if isinstance(value, Problem):
            code, message, p = value.code, value.message, value.path
        elif isinstance(value, dict):
            code, message, p = value.get("code"), value.get("message"), value.get("path", MISSING)
        else:
            raise ValueError(f"{path}: invalid problem")
        if not isinstance(code, str) or not isinstance(message, str):
            raise ValueError(f"{path}: invalid problem")
        parts = [f'"code":{_json_str(code, path)}', f'"message":{_json_str(message, path)}']
        if not isinstance(p, MissingType) and p is not None:
            if not isinstance(p, str):
                raise ValueError(f"{path}: invalid problem path")
            parts.append(f'"path":{_json_str(p, path)}')
        return "{" + ",".join(parts) + "}"
    if name == "unit":
        if isinstance(value, dict) and len(value) == 0:
            return "{}"
        if is_dataclass(value) and len(dc_fields(value)) == 0:
            return "{}"
        raise ValueError(f"{path}: unit must be empty")
    if name == "execution_receipt":
        if isinstance(value, ExecutionReceipt):
            did, eid, rev, url = value.durable_identity, value.execution_id, value.accepted_revision, value.status_url
        elif isinstance(value, dict):
            did = value.get("durable_identity")
            eid = value.get("execution_id")
            rev = value.get("accepted_revision")
            url = value.get("status_url", MISSING)
        else:
            raise ValueError(f"{path}: invalid enqueue receipt")
        if not isinstance(did, str) or not isinstance(eid, str) or not isinstance(rev, str):
            raise ValueError(f"{path}: invalid enqueue receipt")
        parts = [
            f'"accepted_revision":{_json_str(rev, path)}',
            f'"durable_identity":{_json_str(did, path)}',
            f'"execution_id":{_json_str(eid, path)}',
        ]
        if not isinstance(url, MissingType) and url is not None:
            parts.append(f'"status_url":{_encode_primitive(url, "url", path)}')
        return "{" + ",".join(parts) + "}"
    raise ValueError(f"{path}: unsupported primitive type {name!r}")


def _decode_int_str(value: Any, path: str, *, unsigned: bool) -> int:
    if not isinstance(value, str) or value == "-0" or _CANON_INT_RE.fullmatch(value) is None:
        raise ValueError(f"{path}: expected canonical integer string")
    n = int(value)
    if unsigned and n < 0:
        raise ValueError(f"{path}: unsigned integer is negative")
    return n


def _decode_primitive(value: Any, name: str, path: str) -> Any:
    if name == "json":
        return value
    if name == "bool":
        if not isinstance(value, bool):
            raise ValueError(f"{path}: expected bool")
        return value
    if name == "int":
        return _decode_int_str(value, path, unsigned=False)
    if name == "size":
        return _decode_int_str(value, path, unsigned=True)
    if name == "int64":
        n = _decode_int_str(value, path, unsigned=False)
        if n < -(1 << 63) or n > (1 << 63) - 1:
            raise ValueError(f"{path}: int64 out of range")
        return n
    if name == "uint64":
        n = _decode_int_str(value, path, unsigned=True)
        if n > (1 << 64) - 1:
            raise ValueError(f"{path}: uint64 out of range")
        return n
    if name in ("int32", "uint32"):
        if not isinstance(value, JsonNumber) or value.scale != 0:
            raise ValueError(f"{path}: expected integral JSON number")
        n = int(value.coefficient)
        lo, hi = (0, (1 << 32) - 1) if name == "uint32" else (-(1 << 31), (1 << 31) - 1)
        if n < lo or n > hi:
            raise ValueError(f"{path}: {name} out of range")
        return n
    if name in ("float32", "float64"):
        if not isinstance(value, JsonNumber):
            raise ValueError(f"{path}: expected JSON number")
        bits = 32 if name == "float32" else 64
        rendered = value.render()
        wide = float(rendered)
        if not math.isfinite(wide) or _is_neg_zero(wide):
            raise ValueError(f"{path}: non-finite float")
        try:
            decoded = _f32(wide) if bits == 32 else wide
        except OverflowError as exc:
            raise ValueError(f"{path}: float32 overflow") from exc
        if not math.isfinite(decoded) or _is_neg_zero(decoded):
            raise ValueError(f"{path}: lossy or non-finite float")
        canon = JsonNumber.from_token(format_float(decoded, bits))
        orig_c, orig_s = _normalize_json_number(value.coefficient, value.scale)
        if canon.coefficient != orig_c or canon.scale != orig_s:
            raise ValueError(f"{path}: lossy float representation")
        return decoded
    if name == "bytes":
        if not isinstance(value, str) or _BASE64_RE.fullmatch(value) is None:
            raise ValueError(f"{path}: invalid padded base64")
        raw = base64.b64decode(value.encode("ascii"), validate=True)
        if base64.b64encode(raw).decode("ascii") != value:
            raise ValueError(f"{path}: non-canonical base64")
        return raw
    if name in ("decimal", "uuid", "date", "datetime", "duration", "url", "relative_path", "string"):
        if not isinstance(value, str):
            raise ValueError(f"{path}: expected {name}")
        _encode_primitive(value, name, path)
        return value
    if name == "problem":
        if not isinstance(value, dict) or not isinstance(value.get("code"), str) or not isinstance(value.get("message"), str):
            raise ValueError(f"{path}: invalid problem")
        p = value.get("path", MISSING)
        if not isinstance(p, MissingType) and not isinstance(p, str):
            raise ValueError(f"{path}: invalid problem path")
        return Problem(code=value["code"], message=value["message"], path=p)
    if name == "unit":
        if not isinstance(value, dict) or len(value) != 0:
            raise ValueError(f"{path}: unit must be an empty object")
        return {}
    if name == "execution_receipt":
        if (
            not isinstance(value, dict)
            or not isinstance(value.get("durable_identity"), str)
            or not isinstance(value.get("execution_id"), str)
            or not isinstance(value.get("accepted_revision"), str)
        ):
            raise ValueError(f"{path}: invalid enqueue receipt")
        url = value.get("status_url", MISSING)
        if not isinstance(url, MissingType):
            _encode_primitive(url, "url", path)
        return ExecutionReceipt(
            durable_identity=value["durable_identity"],
            execution_id=value["execution_id"],
            accepted_revision=value["accepted_revision"],
            status_url=url,
        )
    raise ValueError(f"{path}: unsupported primitive type {name!r}")


def decode_worker_value(
    value: Any,
    descriptor: dict[str, Any],
    registry: dict[str, Any],
    path: str = "$",
    resolving: set[str] | None = None,
) -> Any:
    """Decode an exact-JSON value according to a Scenery type descriptor."""
    if resolving is None:
        resolving = set()
    kind = descriptor["kind"]
    if kind == "named":
        name = descriptor["name"]
        resolved = registry.get(name)
        if resolved is None or name in resolving:
            raise ValueError(f"{path}: invalid named type descriptor {name!r}")
        resolving.add(name)
        try:
            return decode_worker_value(value, resolved, registry, path, resolving)
        finally:
            resolving.remove(name)
    if kind == "optional":
        return decode_worker_value(value, descriptor["value"], registry, path, resolving)
    if kind == "nullable":
        return None if value is None else decode_worker_value(value, descriptor["value"], registry, path, resolving)
    if kind == "primitive":
        return _decode_primitive(value, descriptor["name"], path)
    if kind in ("list", "set"):
        if not isinstance(value, list):
            raise ValueError(f"{path}: expected array")
        decoded = tuple(
            decode_worker_value(item, descriptor["value"], registry, f"{path}[{i}]", resolving)
            for i, item in enumerate(value)
        )
        if kind == "set":
            canon = [encode_exact_json(item, f"{path}[{i}]").encode("utf-8") for i, item in enumerate(value)]
            for i in range(1, len(canon)):
                if canon[i - 1] >= canon[i]:
                    raise ValueError(f"{path}: set is not in unique canonical order")
        return decoded
    if kind == "map":
        if not isinstance(value, dict):
            raise ValueError(f"{path}: expected map")
        return {
            k: decode_worker_value(v, descriptor["value"], registry, f"{path}.{k}", resolving)
            for k, v in value.items()
        }
    if kind == "tuple":
        items = descriptor["values"]
        if not isinstance(value, list) or len(value) != len(items):
            raise ValueError(f"{path}: tuple length mismatch")
        return tuple(
            decode_worker_value(value[i], items[i], registry, f"{path}[{i}]", resolving) for i in range(len(items))
        )
    if kind == "enum":
        if not isinstance(value, str) or (not descriptor.get("open") and value not in descriptor["values"]):
            raise ValueError(f"{path}: invalid enum value")
        return value
    if kind == "union":
        disc = descriptor["discriminator"]
        if not isinstance(value, dict) or not isinstance(value.get(disc), str):
            raise ValueError(f"{path}: invalid tagged union")
        tag = value[disc]
        payload = {k: v for k, v in value.items() if k != disc}
        variant = descriptor["variants"].get(tag)
        if variant is None:
            if not descriptor.get("open"):
                raise ValueError(f"{path}: unknown closed-union variant")
            cls = descriptor.get("unknown_cls")
            if cls is not None:
                return cls(kind=tag, value=payload)
            return {"kind": tag, "value": payload, "unknown": True}
        decoded_val = decode_worker_value(payload, variant, registry, f"{path}.value", resolving)
        variant_cls = descriptor.get("variant_classes", {}).get(tag)
        if variant_cls is not None:
            return variant_cls(value=decoded_val)
        return {"kind": tag, "value": decoded_val}
    if kind == "record":
        if not isinstance(value, dict):
            raise ValueError(f"{path}: expected record")
        fields = descriptor["fields"]
        by_wire = {f["wire"]: f for f in fields}
        kwargs: dict[str, Any] = {}
        unknown: dict[str, Any] = {}
        for wire, item in value.items():
            f = by_wire.get(wire)
            if f is None:
                if not descriptor.get("preserveUnknown"):
                    raise ValueError(f"{path}.{wire}: unknown record field")
                unknown[wire] = item
                continue
            prop = f["property"]
            decoded_val = decode_worker_value(item, f["value"], registry, f"{path}.{prop}", resolving)
            _validate_constraints(decoded_val, f.get("constraints"), f["value"], registry, f"{path}.{prop}")
            kwargs[prop] = decoded_val
        for f in fields:
            prop = f["property"]
            if prop not in kwargs:
                if not f.get("optional"):
                    raise ValueError(f"{path}.{prop}: required field is absent")
                kwargs[prop] = MISSING
        if descriptor.get("preserveUnknown"):
            kwargs["unknown_fields"] = unknown
        cls = descriptor.get("cls")
        if cls is not None:
            return cls(**kwargs)
        # Plain dict representation (omits MISSING keys).
        return {k: v for k, v in kwargs.items() if not isinstance(v, MissingType)}
    raise ValueError(f"{path}: unsupported descriptor kind {kind!r}")


def encode_typed_json(
    value: Any,
    descriptor: dict[str, Any],
    registry: dict[str, Any],
    path: str = "$",
    resolving: set[str] | None = None,
) -> str:
    """Encode a Python value into canonical Scenery JSON according to descriptor."""
    if resolving is None:
        resolving = set()
    kind = descriptor["kind"]
    if kind == "named":
        name = descriptor["name"]
        resolved = registry.get(name)
        if resolved is None or name in resolving:
            raise ValueError(f"{path}: invalid named type descriptor {name!r}")
        resolving.add(name)
        try:
            return encode_typed_json(value, resolved, registry, path, resolving)
        finally:
            resolving.remove(name)
    if kind == "optional":
        return encode_typed_json(value, descriptor["value"], registry, path, resolving)
    if kind == "nullable":
        return "null" if value is None else encode_typed_json(value, descriptor["value"], registry, path, resolving)
    if kind == "primitive":
        return _encode_primitive(value, descriptor["name"], path)
    if kind in ("list", "set"):
        if not isinstance(value, (list, tuple)):
            raise ValueError(f"{path}: expected list or tuple")
        items = [encode_typed_json(item, descriptor["value"], registry, f"{path}[{i}]", resolving) for i, item in enumerate(value)]
        if kind == "set":
            items.sort(key=lambda s: s.encode("utf-8"))
            for i in range(1, len(items)):
                if items[i - 1] == items[i]:
                    raise ValueError(f"{path}: duplicate set element")
        return "[" + ",".join(items) + "]"
    if kind == "map":
        if not isinstance(value, dict):
            raise ValueError(f"{path}: expected dict")
        members: list[str] = []
        for k in sorted(value.keys()):
            if not isinstance(k, str) or k in _FORBIDDEN_KEYS:
                raise ValueError(f"{path}: invalid map key")
            members.append(f"{_json_str(k, path)}:{encode_typed_json(value[k], descriptor['value'], registry, f'{path}.{k}', resolving)}")
        return "{" + ",".join(members) + "}"
    if kind == "tuple":
        items = descriptor["values"]
        if not isinstance(value, (list, tuple)) or len(value) != len(items):
            raise ValueError(f"{path}: tuple length mismatch")
        return "[" + ",".join(
            encode_typed_json(value[i], items[i], registry, f"{path}[{i}]", resolving) for i in range(len(items))
        ) + "]"
    if kind == "enum":
        if not isinstance(value, str) or (not descriptor.get("open") and value not in descriptor["values"]):
            raise ValueError(f"{path}: invalid enum value")
        return _json_str(value, path)
    if kind == "union":
        disc = descriptor["discriminator"]
        if is_dataclass(value):
            tag = getattr(value, "kind", None)
            payload = getattr(value, "value", MISSING)
            is_unknown = bool(getattr(value, "unknown", False))
        elif isinstance(value, dict):
            tag = value.get("kind")
            payload = value.get("value", MISSING)
            is_unknown = bool(value.get("unknown", False))
        else:
            raise ValueError(f"{path}: invalid union value")
        if not isinstance(tag, str) or isinstance(payload, MissingType):
            raise ValueError(f"{path}: invalid union value")
        variant = descriptor["variants"].get(tag)
        if variant is None:
            if not descriptor.get("open") or not is_unknown or not isinstance(payload, dict):
                raise ValueError(f"{path}: unknown union variant")
            combined = {disc: tag, **payload}
            return encode_exact_json(combined, path)
        encoded_obj = encode_typed_json(payload, variant, registry, f"{path}.value", resolving)
        parsed = parse_exact_json(encoded_obj)
        if not isinstance(parsed, dict):
            raise ValueError(f"{path}: union variant must encode as object")
        parsed[disc] = tag
        return encode_exact_json(parsed, path)
    if kind == "record":
        if is_dataclass(value):
            get_prop = lambda prop: getattr(value, prop, MISSING)
            unknown_fields = getattr(value, "unknown_fields", None) if descriptor.get("preserveUnknown") else None
        elif isinstance(value, dict):
            known_props = {f["property"] for f in descriptor["fields"]}
            allowed = known_props | ({"unknown_fields"} if descriptor.get("preserveUnknown") else set())
            for k in value:
                if k not in allowed:
                    raise ValueError(f"{path}.{k}: unknown record field")
            get_prop = lambda prop: value.get(prop, MISSING)
            unknown_fields = value.get("unknown_fields") if descriptor.get("preserveUnknown") else None
        else:
            raise ValueError(f"{path}: expected record")
        entries: dict[str, str] = {}
        for f in descriptor["fields"]:
            prop = f["property"]
            wire = f["wire"]
            fv = get_prop(prop)
            if isinstance(fv, MissingType):
                if not f.get("optional"):
                    raise ValueError(f"{path}.{prop}: required field is absent")
                continue
            _validate_constraints(fv, f.get("constraints"), f["value"], registry, f"{path}.{prop}")
            entries[wire] = encode_typed_json(fv, f["value"], registry, f"{path}.{prop}", resolving)
        if descriptor.get("preserveUnknown") and unknown_fields is not None:
            if not isinstance(unknown_fields, dict):
                raise ValueError(f"{path}.unknown_fields: expected dict")
            for k, v in unknown_fields.items():
                if not isinstance(k, str) or k in entries or k in _FORBIDDEN_KEYS:
                    raise ValueError(f"{path}.{k}: invalid unknown field key")
                entries[k] = encode_exact_json(v, f"{path}.{k}")
        return "{" + ",".join(f"{_json_str(k, path)}:{entries[k]}" for k in sorted(entries.keys())) + "}"
    raise ValueError(f"{path}: unsupported descriptor kind {kind!r}")


class OutcomeCodec:
    """Encodes and decodes declared operation outcomes (result | error)."""

    def __init__(
        self,
        registry: dict[str, Any],
        variants: dict[str, dict[str, Any]],
        classes: dict[str, type[Any]] | None = None,
    ) -> None:
        self._registry = registry
        self._variants = variants
        self._classes = classes or {}

    def _inspect(self, outcome: Any) -> tuple[str, str, str, dict[str, Any], Any]:
        if is_dataclass(outcome):
            kind = getattr(outcome, "kind", None)
            name = getattr(outcome, "name", None)
            field = "problem" if kind == "error" else "value"
            if kind not in ("result", "error") or not isinstance(name, str) or not hasattr(outcome, field):
                raise TypeError("invalid worker outcome")
            payload = getattr(outcome, field)
            key = f"{kind}.{name}"
            descriptor = self._variants.get(key)
            if descriptor is None:
                raise TypeError("undeclared worker outcome")
            return kind, name, field, descriptor, payload
        if isinstance(outcome, dict):
            kind = outcome.get("kind")
            name = outcome.get("name")
            if kind not in ("result", "error") or not isinstance(name, str):
                raise TypeError("invalid worker outcome")
            field = "problem" if kind == "error" else "value"
            key = f"{kind}.{name}"
            descriptor = self._variants.get(key)
            if descriptor is None or len(outcome) != 3 or field not in outcome:
                raise TypeError("undeclared or malformed worker outcome")
            return kind, name, field, descriptor, outcome[field]
        raise TypeError("invalid worker outcome")

    def decode(self, outcome: Any) -> Any:
        kind, name, field, descriptor, payload = self._inspect(outcome)
        decoded = decode_worker_value(payload, descriptor, self._registry)
        cls = self._classes.get(f"{kind}.{name}")
        if cls is not None:
            return cls(**{field: decoded})
        return {"kind": kind, "name": name, field: decoded}

    def encode(self, outcome: Any) -> str:
        kind, name, field, descriptor, payload = self._inspect(outcome)
        encoded = encode_typed_json(payload, descriptor, self._registry)
        return (
            f'{{"kind":{_json_str(kind)},"name":{_json_str(name)},'
            f'"{field}":{encoded}}}'
        )


def outcome_codec(
    registry: dict[str, Any],
    variants: dict[str, dict[str, Any]],
    classes: dict[str, type[Any]] | None = None,
) -> OutcomeCodec:
    """Create an OutcomeCodec for an operation or client binding."""
    return OutcomeCodec(registry, variants, classes)

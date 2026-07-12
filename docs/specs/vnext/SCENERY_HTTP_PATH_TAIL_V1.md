# Scenery HTTP Path Tail v1

Normative companion specification

Codec extension profile: `scenery.http-path-tail/v1`\
Runtime extension profile: `scenery.runtime-http-path-tail/v1`\
Document revision: `0.1-draft`\
Status: draft for Scenery language edition 2027\
Umbrella specification: [SCENERY_LANGUAGE_SPEC.md](SCENERY_LANGUAGE_SPEC.md)\
Base HTTP codec: [SCENERY_HTTP_CODEC_V1.md](SCENERY_HTTP_CODEC_V1.md)

## 1. Purpose

This profile adds typed terminal HTTP path tails without changing
`scenery.http-codec/v1` or `scenery.runtime-http/v1`. It defines:

- source template and mapping syntax;
- zero-or-more segment cardinality;
- route overlap, conflict, and precedence rules;
- segment decoding and typed target construction;
- canonical graph and revision behavior;
- generated Go adapter and runtime router requirements;
- generated TypeScript URL construction;
- OpenAPI projection, security, and conformance rules.

The words MUST, MUST NOT, SHOULD, SHOULD NOT, and MAY are normative.

## 2. Profile identity and dependencies

`scenery.http-path-tail/v1` depends on:

- `scenery.compiler-core/v1`;
- `scenery.http-codec/v1`.

`scenery.runtime-http-path-tail/v1` depends on:

- `scenery.runtime-http/v1`;
- `scenery.http-path-tail/v1`.

The extension adds only terminal path-tail syntax, mapping, matching, decoding,
and projection behavior. Query, header, cookie, body, response, negotiation,
compression, limits, gateway, admission, and guarantee semantics remain those
of `scenery.http-codec/v1`.

A path-tail binding continues to select `std.codec.http_json_v1`. There is no
second JSON codec resource. The compiled binding and every generated artifact
MUST additionally record `scenery.http-path-tail/v1`; a runtime route table
MUST additionally require `scenery.runtime-http-path-tail/v1`.

A tool that does not claim the required extension profile MUST emit
`unsupported_profile`. It MUST NOT lower a path tail as a single-segment
parameter, glob, raw handler, regular expression, or implementation-declared
approximation.

## 3. Source syntax and typing

### 3.1 Terminal path-tail template

A path tail uses three dots inside one complete terminal parameter segment:

~~~hcl
http {
  method        = "GET"
  path          = "/drive/{path...}"
  codec_profile = std.codec.http_json_v1

  path_tail "path" {
    to = operation.download.input.path
  }
}
~~~

`{name...}` is the only native spelling. The name follows the normal
lower-snake-case path parameter rule.

A path tail:

- MUST occupy one complete segment;
- MUST be the final segment;
- MUST be the only path tail in the template;
- MUST have exactly one matching `path_tail` block;
- MUST NOT have a prefix, suffix, modifier, default, or regular expression;
- MUST NOT use `*name`, `{*name}`, `{name*}`, or another wildcard spelling.

The following templates are invalid:

~~~text
/drive/{path...}/metadata
/drive/prefix-{path...}
/drive/{path...}-suffix
/drive/{first...}/{second...}
/drive/*path
/drive/{*path}
/drive/{path*}
~~~

### 3.2 Mapping

The mapping label MUST equal the template name. Every path tail has exactly
one mapping, and every `path_tail` block names exactly one template tail.

The target MUST be one operation input field of exactly one of these types:

- `string`;
- `relative_path`;
- `optional(relative_path)`.

`optional(string)`, nullable values, lists, sets, maps, records, and other
scalar types are unsupported in version 1.

For a non-empty capture, decoded segments are joined with structural `/`
separators before target construction:

- `string` receives the joined decoded text;
- `relative_path` receives a validated canonical `relative_path`;
- `optional(relative_path)` receives a present validated `relative_path`.

For an empty capture:

- `string` receives the empty string;
- `relative_path` produces `transport.invalid_request` before invocation;
- `optional(relative_path)` is absent.

Declared target constraints run after path decoding and typed construction.

## 4. Matching, overlap, and route identity

### 4.1 Cardinality

A path tail captures zero or more complete non-empty path segments:

~~~text
/drive             -> empty capture
/drive/a           -> a
/drive/a/b         -> a/b
/drive/            -> no match
/drive//b          -> no match
~~~

The zero-segment case consumes no slash. A runtime MUST NOT append, remove,
collapse, or redirect slashes to manufacture a match.

Gateway base paths are joined before matching. Gateway and canonical method
continue to scope the route namespace exactly as in `scenery.http-codec/v1`.

### 4.2 Deterministic precedence

When more than one route matches, the router compares complete matches by the
following decision order:

~~~text
literal segment > single-segment parameter > exact end > path tail
~~~

Comparison proceeds from the first differing decision. A more-specific prefix
therefore wins over a broader tail. Source order, filename order, frontend,
module order, and registration order never participate.

Required examples:

| Route A | Route B | Result |
|---|---|---|
| `/drive/health` | `/drive/{path...}` | valid; literal route wins for `/drive/health` |
| `/drive/{bucket}` | `/drive/{path...}` | valid; single parameter wins for one segment |
| `/drive/public/{path...}` | `/drive/{path...}` | valid; longer literal prefix wins under `/drive/public` |
| `/drive` | `/drive/{path...}` | valid; exact route wins for `/drive` |
| `/drive/{path...}` | `/drive/{rest...}` | conflict; equal match and precedence sets |

A literal or parameter branch that does not produce a complete match does not
hide a broader matching path tail. Implementations MAY use a trie or another
router structure, but observable selection MUST compare complete matches.

### 4.3 Conflicts and registration

Parameter names do not distinguish route match sets. Two path-tail templates
with the same gateway, canonical method, and segment shape conflict regardless
of mapping name or target type.

The compiler MUST reject an equal match-and-precedence set. Runtime
registration MUST independently verify the same rule before accepting traffic.
All non-tail route conflicts remain governed by `scenery.http-codec/v1`.

Automatic `HEAD` and generated CORS `OPTIONS` projections remain owned by their
source route and inherit its path-tail match set and precedence.

### 4.4 No fallback after selection

The router first selects the unique most-specific route, then decodes and
validates its mappings. A decoding, type, constraint, admission, or handler
failure MUST NOT fall back to a broader path-tail route.

## 5. Path-tail decoding

### 5.1 Segment boundaries

The runtime MUST split the raw URI path on literal `/` bytes before percent
decoding. Encoded slash or backslash never creates or changes a boundary.

The captured tail excludes:

- the gateway base path;
- every literal or single-parameter prefix segment;
- the separator preceding the first captured segment.

### 5.2 Per-segment decoding

Each captured segment is decoded independently and exactly once. The runtime
MUST reject:

- malformed percent escapes;
- invalid UTF-8 or overlong encodings;
- encoded or decoded `/`;
- literal, encoded, or decoded `\`;
- decoded NUL;
- an empty segment;
- `.` or `..`, including encoded spellings;
- a value that violates the selected target type or its constraints.

Plus is a literal plus and is never decoded as space in a path.

The runtime MUST also reject a once-decoded segment when decoding its remaining
valid escapes one more time would introduce `/`, `\`, or NUL, or would make the
complete segment exactly `.` or `..`. This validation does not replace the
once-decoded semantic value. It prevents double-decoding traversal without
rejecting safe percent text such as `%2Ejson`, and no generated adapter, handler
wrapper, or framework provider may decode the semantic value again.

### 5.3 Typed construction

After decoding, the runtime joins segments with literal `/` separators. It MUST
NOT call a host filesystem path cleaner, convert separators, resolve a working
directory, or interpret the result as a filesystem path.

`relative_path` construction applies the edition-pinned primitive rules after
joining. An empty capture cannot construct a non-optional `relative_path` and
therefore produces `transport.invalid_request` before authorization-dependent
operation invocation.

## 6. Canonical graph and revisions

The canonical HTTP binding records path-tail semantics explicitly. Its
representation contains the equivalent of:

~~~json
{
  "method": "GET",
  "path": "/drive/{path...}",
  "codec_profile": {
    "$ref": "std.codec.http_json_v1"
  },
  "path_tail": {
    "name": "path",
    "to": {
      "$ref": "operation.download.input.path"
    },
    "minimum_segments": 0,
    "target_type": "relative_path",
    "decoding": "segment_rfc3986_once",
    "guarantee": "framework_enforced"
  },
  "required_profiles": [
    "scenery.http-path-tail/v1"
  ]
}
~~~

The resource envelope and named-child encoding follow the umbrella
specification. Effective and expanded views MUST expose the resolved target
type, empty-capture behavior, decoding guarantee, and required profiles.

The template, mapping, target type, empty-capture behavior, and required
profile participate in `contract_revision`, the gateway HTTP-surface revision,
generated-client revision, OpenAPI revision, compatibility comparison, and
source-map provenance.

## 7. Generated Go adapter and runtime router

When `scenery.go-implementation/v1` and
`scenery.runtime-http-path-tail/v1` are active:

- the native unary handler signature is unchanged;
- `string` maps to `string`;
- `relative_path` maps to `scenery.RelativePath`;
- `optional(relative_path)` maps to
  `scenery.Optional[scenery.RelativePath]`;
- the runtime supplies the typed value through the normal generated operation
  input;
- invalid values produce `transport.invalid_request` before handler
  invocation;
- the adapter MUST NOT inspect or decode the raw URI again.

No `http.Request`, `http.ResponseWriter`, router wildcard map, raw URI, or
transport wrapper enters the operation solely because a binding uses a path
tail.

The generated runtime table MUST include the canonical template, tail mapping,
target type, empty-capture behavior, required profiles, and deterministic
precedence data. Registration MUST reject a table whose route conflict result
differs from compiler output.

A conforming runtime router performs these steps:

1. find every complete match under one gateway and canonical method;
2. select the unique most-specific route;
3. capture the terminal remainder as ordered raw segments;
4. decode and validate each captured segment exactly once;
5. construct the typed mapped value;
6. continue through normal admission, pipeline, execution, and response
   handling.

## 8. Generated TypeScript clients

A generated TypeScript client constructs a path-tail URL from the semantic
field value, never from a pre-encoded fragment.

For a non-empty value it MUST:

1. split `string` or `RelativePathString` values on `/`;
2. reject leading, trailing, empty, dot, dot-dot, or backslash segments;
3. UTF-8 encode and RFC 3986 percent-encode each segment independently;
4. join encoded segments with literal `/` separators.

The encoder escapes every byte outside the RFC 3986 unreserved set. A literal
percent is encoded as `%25`; input is never accepted as already encoded.

Examples:

~~~text
assets/logo.svg   -> assets/logo.svg
space here/a+b    -> space%20here/a%2Bb
café/menu         -> caf%C3%A9/menu
~~~

The client MUST NOT encode the whole value as one path component because that
would turn structural slashes into `%2F`.

An empty `string` or absent `optional(relative_path)` emits the fixed binding
prefix with no trailing slash. A non-optional `relative_path` client value
cannot be empty.

The generated descriptor and metadata record both the base HTTP codec and the
path-tail extension profiles. Selecting or removing the extension changes the
client revision.

## 9. OpenAPI and documentation projections

OpenAPI does not define a standard path parameter that consumes slash-separated
segments. A Scenery OpenAPI generator MUST either:

- emit an explicit Scenery vendor extension preserving the original template,
  zero-or-more cardinality, typed target, and per-segment encoding rules; or
- reject projection with `unsupported_profile`.

It MUST NOT emit an ordinary single-segment parameter while claiming semantic
equivalence.

Human and machine documentation MUST state that the path tail is terminal,
captures zero or more complete segments, does not match a trailing slash, and
encodes each non-empty segment independently.

## 10. Compatibility and semantic diff

Changing `{name}` to `{name...}` broadens the accepted path set and changes the
mapping and generated-client contract. Semantic diff MUST report HTTP-surface,
client, and security-review consequences.

Adding a path-tail fallback is additive only when every existing path retains
its prior selected owner and observable behavior. Removing a tail or changing
its prefix, target type, empty-capture behavior, or precedence is classified
from the old client/server direction independently by
`scenery.compatibility-core/v1`.

## 11. Security properties

A path tail increases the set of paths reaching one operation. Compilation and
review tooling MUST expose that reachability change and preserve the binding's
authentication, authorization, pipeline, limits, and gateway exposure.

Segment-first matching and exact single decoding MUST prevent encoded
separators, traversal segments, backslash aliases, invalid Unicode, NUL, empty
segments, and downstream double decoding from changing the semantic tail.

The framework treats the decoded result as application data. It never performs
filesystem conversion or grants object-store, filesystem, or provider access
from route syntax alone.

## 12. Conformance requirements

A tool claiming `scenery.http-path-tail/v1` or
`scenery.runtime-http-path-tail/v1` MUST pass fixtures for:

### 12.1 Syntax and typing

- valid terminal `{path...}` plus matching `path_tail` syntax;
- rejection of non-terminal, prefixed, suffixed, repeated, wildcard, or
  regular-expression spellings;
- exact mapping-name equality and mapping completeness;
- `string`, `relative_path`, and `optional(relative_path)` targets;
- rejection of every unsupported target shape;
- explicit unsupported-profile failure when the extension is unavailable.

### 12.2 Matching and conflicts

- empty, one-segment, and nested captures;
- non-match for trailing slash and empty segments;
- literal, single-parameter, exact-end, and longer-prefix precedence;
- equal-tail conflict independent of parameter names and source order;
- gateway, method, base-path, automatic `HEAD`, and CORS ownership;
- no fallback after selected-route decoding failure;
- compiler and runtime registration agreement.

### 12.3 Decoding

- spaces, plus, percent, Unicode, and reserved characters;
- malformed escape and invalid UTF-8 rejection;
- encoded slash and backslash rejection in every hex case;
- literal backslash, NUL, empty, dot, and dot-dot rejection;
- double-encoded separator, NUL, and traversal rejection;
- exact one-time decoding with no downstream re-decoding;
- canonical `relative_path` construction;
- empty string, invalid empty `relative_path`, and absent optional path behavior;
- target constraint failure as `transport.invalid_request`.

### 12.4 Generated artifacts

- unchanged unary Go ABI and typed input population;
- runtime route-table metadata and conflict verification;
- TypeScript independent segment encoding and structural slash preservation;
- no trailing slash for an empty tail;
- base codec plus extension identities in descriptors and metadata;
- deterministic contract, HTTP-surface, TypeScript, and OpenAPI revisions;
- honest OpenAPI vendor extension or explicit unsupported-profile failure.

## Appendix A: Deliberate exclusions

Version 1 does not define:

- non-terminal path tails;
- multiple tails in one template;
- optional `string`, nullable, list, set, map, or record targets;
- regular-expression parameters or greedy modifiers;
- slash normalization or redirects;
- filesystem conversion or provider-specific object-key semantics;
- raw HTTP request, response, or stream ownership;
- a new JSON codec resource.

Those require separately versioned profiles or ordinary operation logic.

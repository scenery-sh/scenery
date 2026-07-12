# Scenery HTTP Codec v1

Normative companion specification

Profile: `scenery.http-codec/v1`  
Runtime profile: `scenery.runtime-http/v1`  
Document revision: `0.5-draft`\
Status: draft for Scenery language edition 2027  
Umbrella specification: [SCENERY_LANGUAGE_SPEC.md](SCENERY_LANGUAGE_SPEC.md)

## 1. Purpose

This document defines the exact HTTP boundary produced and consumed by Scenery runtimes, generated clients, conformance tools, and agents. It is independently versioned so wire behavior can evolve without changing the source-language edition.

The profile covers:

- logical HTTP gateways;
- route identity and collision rules;
- request mapping;
- body codecs;
- scalar wire forms;
- response selection and coverage;
- content negotiation, character encoding, compression, and limits;
- CORS, forwarded headers, trusted proxies, and deployment listeners;
- framework-enforced versus implementation-declared guarantees.

The words MUST, MUST NOT, SHOULD, SHOULD NOT, and MAY are normative.

## 2. Profile identity and dependencies

`scenery.http-codec/v1` depends on:

- `scenery.compiler-core/v1`;
- the edition-2027 type system and canonical graph;
- the operation, binding, execution, authentication, authorization, and pipeline resource schemas.

`scenery.runtime-http/v1` depends on this codec profile and implements direct HTTP delivery. Durable enqueue and wait delivery additionally require `scenery.runtime-durable/v1`.

Terminal zero-or-more path segments are an additive extension defined by
[SCENERY_HTTP_PATH_TAIL_V1.md](SCENERY_HTTP_PATH_TAIL_V1.md). The extension
continues to use this codec for all non-tail HTTP semantics and requires
separate `scenery.http-path-tail/v1` and
`scenery.runtime-http-path-tail/v1` profile claims.

A manifest and generated client MUST record the exact codec profile. A runtime MUST reject an unsupported codec profile rather than approximating it.

## 3. HTTP gateways

### 3.1 Logical gateway

An `http_gateway` is a contract resource representing one route namespace and trust boundary:

~~~hcl
http_gateway "public_api" {
  exposure        = "internet"
  base_path       = "/"
  cors            = std.cors.application
  trusted_proxies = std.trusted_proxies.none
  forwarded       = std.forwarded_headers.reject
}
~~~

The gateway owns:

- maximum exposure;
- contract base path;
- CORS policy;
- trusted-proxy policy;
- forwarded-header policy;
- request and response size ceilings unless a binding narrows them;
- listener-wide admission behavior.

It does not own hostnames, IP addresses, ports, certificates, or platform listener identifiers. Those are deployment values.

### 3.2 Binding reference

Every HTTP binding references exactly one gateway:

~~~hcl
binding "process_scene_http" {
  gateway   = http_gateway.public_api
  operation = operation.process_scene
  execution = execution.process_scene_direct
  protocol  = "http"
  delivery  = "call"

  authentication = authentication.standard
  authorization  = authorization.member
  pipeline       = pipeline.http_default

  http {
    method        = "POST"
    path          = "/house/process"
    codec_profile = std.codec.http_json_v1
  }
}
~~~

The binding inherits gateway exposure. It MAY declare a narrower exposure but MUST NOT widen the gateway. Authentication, authorization, and pipeline remain binding-level references because routes on one listener may have different admission policies.

### 3.3 Route identity

A route key is:

~~~text
(gateway address, canonical method, effective path)
~~~

The effective path is the gateway base path joined with the binding path using exactly one slash at the boundary. Query strings do not participate in route identity.

Two active resources with the same route key are an error. The same method/path pair on different gateways is valid. Mixed legacy/native mode uses the same rule; frontend origin never creates precedence.

Template overlap is deterministic. Routes must have the same segment count to overlap because v1 has no catch-all. At each segment, a literal matches only itself and takes precedence over a parameter. Two templates whose corresponding segments are both parameters or equal literals for the entire path have the same match set and conflict, regardless of parameter names. Thus `/users/me` may coexist with `/users/{user_id}` and wins for `/users/me`; `/users/{id}` conflicts with `/users/{name}`. The router MUST implement this rule exactly.

An explicit `HEAD` route conflicts with another explicit `HEAD` route. An edition-provided automatic `HEAD` projection from `GET` is derived from the owning `GET` route and does not create an independent owner. `OPTIONS` generated for CORS follows the same ownership rule.

### 3.4 Deployment listeners

A deployment binds one or more concrete listeners to a gateway:

~~~hcl
deployment "production" {
  http_gateway {
    target = http_gateway.public_api

    listener {
      host = "api.onlv.dev"
      port = 443
      tls  = "required"
    }
  }
}
~~~

Listener fields include host, address, port, TLS mode, certificate or secret reference, HTTP protocol versions, and platform-specific listener identity. They are deployment-domain fields and participate in `deployment_revision`, not `contract_revision`.

One deployment may bind multiple listeners to one gateway. They expose the same route contract. A listener cannot make a gateway more exposed than its contract permits.

## 4. HTTP binding contract

An HTTP binding declares:

- gateway;
- operation and execution;
- delivery mode;
- authentication, authorization, and pipeline;
- method and relative path;
- exact codec profile;
- complete input mappings;
- complete mappings for all reachable outcomes.

Method, path, codec, gateway identity, mappings, and response surface are contract-domain fields.

Delivery is:

- `call`: invoke a compatible direct execution and return completion;
- `enqueue`: return after durable dispatch acceptance;
- `wait`: dispatch durably and wait for completion;
- `stream`: use a separately supported stream result profile.

Every binding MUST select an execution explicitly. This profile performs no execution inference.

## 5. Paths

### 5.1 Template syntax

Literal path segments use lower-kebab-case by convention. A parameter uses `{lower_snake_case}` and maps to exactly one scalar operation input field:

~~~hcl
http {
  method = "GET"
  path   = "/house/scenes/{scene_id}"

  path_parameter "scene_id" {
    to = operation.get_scene.input.scene_id
  }
}
~~~

Every template parameter has one mapping. Every path mapping names a template
parameter. This base profile supports only single-segment parameters.
`{name...}` plus a matching `path_tail` block is defined only by the separately
claimed [HTTP path-tail profile](SCENERY_HTTP_PATH_TAIL_V1.md). All other
wildcard and catch-all spellings remain unsupported.

### 5.2 Decoding and normalization

The runtime:

- matches the raw URI path by segment;
- accepts only valid percent escapes;
- decodes each matched parameter exactly once as UTF-8;
- rejects encoded slash, encoded backslash, decoded NUL, dot segments, invalid UTF-8, and overlong encodings;
- never treats plus as space in a path;
- validates the decoded scalar before invocation.

The router MUST NOT normalize two distinct external paths into one route silently. A deployment proxy must preserve the path contract or be represented by the gateway base path.

## 6. Query strings

Query names use explicit wire names. Components use RFC 3986 percent encoding. Plus is a literal plus, not space.

Defaults:

- scalar fields accept exactly one value;
- lists use repeated names and preserve list order;
- sets use repeated names in canonical semantic set order from the language specification;
- maps and nested records are rejected unless `encoding = "json"` is explicit;
- comma-delimited collections require `encoding = "comma"`;
- a scalar repeated unexpectedly is a decoding error.

Example:

~~~hcl
query_parameter "tag" {
  to       = operation.search.input.tags
  encoding = "repeated"
}
~~~

A JSON-valued query parameter contains one canonical UTF-8 JSON value before percent encoding.

## 7. Headers and cookies

Header names are lower-case in canonical IR and case-insensitive on the wire. Leading and trailing optional whitespace is removed according to HTTP field rules.

- A scalar header rejects multiple field values.
- A list uses repeated field values and preserves order.
- A set uses repeated values in canonical semantic order.
- Comma joining is allowed only when explicitly selected and the scalar codec cannot contain an unescaped comma.
- `Set-Cookie` is never comma-joined.

Cookie names use their declared wire names. Cookie values use UTF-8 followed by RFC 3986 percent encoding. Response cookies explicitly declare or inherit visible effective defaults for `SameSite`, `Secure`, `HttpOnly`, `Path`, `Domain`, `Max-Age`, and expiry.

## 8. Request input completeness

Every required operation input field is populated exactly once by one of:

- path mapping;
- query mapping;
- header mapping;
- cookie mapping;
- trusted context or principal mapping;
- body mapping;
- deterministic field default.

An optional field may remain absent. A nullable field must be present and may be null. Two mappings to one field, an unmapped required field, an unknown target, or a body/include collision is a compile error.

Context mappings read only profile-authorized fields from `principal` or `context`. Untrusted request values cannot populate runtime-owned context fields.

## 9. Body codecs

### 9.1 Core codecs

The profile defines:

- `json`;
- `problem_json`;
- `text`;
- `bytes`;
- `form`;
- `multipart`.

`server_sent_events` belongs to a separately claimed streaming subprofile even though its schema name is reserved.

### 9.2 No raw transport handle

There is no `raw` codec in v1. A buffered opaque payload uses `codec = "bytes"` and a Scenery `bytes` value:

~~~hcl
body {
  codec = "bytes"
  to    = operation.upload_archive.input.archive
}
~~~

The framework owns reading, limits, cancellation, and defensive copying. `http.Request`, `ResponseWriter`, sockets, and transport stream handles are not operation input types.

Unbuffered or transport-coupled access requires a distinct profile such as `scenery.go-http-coupled/v1`, an explicit handler ABI, and `implementation_declared` guarantees. A v1 runtime MUST reject it.

### 9.3 JSON bodies

JSON is UTF-8 only. The decoder rejects:

- byte-order marks;
- duplicate object member names;
- invalid UTF-8 or unpaired surrogates;
- invalid JSON numbers;
- trailing non-whitespace bytes;
- unknown fields in closed records;
- unknown closed-enum values;
- unknown closed-union tags.

An absent `optional(T)` field is absent. A present `nullable(T)` field may be JSON null. `optional(nullable(T))` distinguishes absent, null, and concrete T.

Records with `unknown_fields = "preserve"` retain unknown members as canonical `json` values in the generated dedicated unknown-field map. Encoding rejects a collision with a declared effective wire name.

### 9.4 Form bodies

`application/x-www-form-urlencoded` uses UTF-8 and form rules where plus means space. Collection behavior follows query rules unless explicitly overridden. Nested values require explicit JSON encoding.

### 9.5 Multipart bodies

A multipart schema distinguishes text fields, byte fields, and file parts. Every file part declares:

- target field;
- accepted media types;
- maximum bytes;
- whether a filename is retained;
- whether multiple parts are allowed.

Part names are exact wire names. Undeclared parts are rejected unless an explicit preserving map exists. Temporary files and transport streams are runtime details and never operation values in v1.

## 10. Scalar wire forms

| Type | Canonical HTTP form |
|---|---|
| bool | `true` or `false` |
| int | base-10 with no plus or unnecessary leading zero |
| int32, uint32 | JSON/base-10 number within exact bounds |
| int64, uint64 | base-10 string by default; JSON number only with JavaScript-safe constraints and explicit option |
| decimal | canonical exact decimal string |
| float32, float64 | shortest finite round-trip decimal; NaN, infinity, and negative zero rejected |
| string | exact UTF-8 scalar sequence |
| bytes in JSON | padded RFC 4648 base64 |
| bytes outside JSON | exact octets |
| uuid | lower-case canonical hyphenated UUID |
| date | `YYYY-MM-DD` |
| datetime | RFC 3339 normalized to UTC `Z`, at most nanosecond precision |
| duration | ISO 8601 elapsed duration using only days, hours, minutes, and seconds |
| size | base-10 integer byte count |
| url | normalized absolute hierarchical RFC 3986 URI; apply IDNA2008 non-transitional processing with Unicode 15.1, emit lower-case ASCII A-label hosts, reject opaque URIs and IPv6 zones, lower-case scheme, remove HTTP/HTTPS default ports and dot segments, uppercase percent hex, and decode unreserved escapes |
| enum | declared string wire value |

Duration years and calendar months are forbidden. Source weeks normalize to days. Fractions are permitted only on seconds and must be an exact nanosecond.

JSON `int` values are strings because Scenery integers are arbitrary precision. A field may select `json_number` only when declared constraints fit the signed JavaScript-safe integer range. Decimal is a JSON string unless an equivalently bounded explicit option is selected.

JSON sets encode as arrays in canonical semantic order. Duplicate elements are errors rather than silently deduplicated.

## 11. Canonical JSON bytes

When a codec requires canonical JSON, it uses the umbrella specification's RFC 8785-based representation after Scenery exact scalars are transformed into tagged or schema-directed wire values. Values of Scenery type `json` first transform every exact JSON numeric token into the umbrella specification's normalized coefficient/scale object; implementations MUST NOT round through JavaScript or IEEE-754 before canonicalization.

Ordinary strings are preserved exactly. They are not NFC-normalized. Invalid UTF-8 and unpaired surrogates are rejected. Solidus is not escaped; non-ASCII scalars, including U+2028 and U+2029, are emitted directly as UTF-8.

Ordinary request JSON need not arrive in canonical member order, but its decoded semantic value and any canonical re-encoding are deterministic.

## 12. Content negotiation

Every request body codec declares accepted media types. Missing or unsupported `Content-Type` reaches `transport.unsupported_media_type`. Invalid syntax or incompatible charset reaches `transport.invalid_request`.

Every response mapping declares or derives one produced media type. `Accept` negotiation is deterministic:

1. highest quality value;
2. most specific media range;
3. binding-declared response media order;
4. bytewise media type order as a final tie-breaker.

No acceptable representation reaches `transport.not_acceptable`.

JSON and text codecs use UTF-8. A declared non-UTF-8 text codec requires another profile. Media type parameters are normalized in canonical IR.

## 13. Compression and limits

Request decompression and response compression are gateway policies visible in the effective graph. Unknown content encodings are rejected. Limits apply to both compressed and decompressed bytes to prevent expansion attacks.

The effective graph exposes at least:

- maximum request header bytes;
- maximum request body bytes;
- maximum decompressed body bytes;
- maximum multipart parts and per-part bytes;
- maximum response bytes for buffered responses;
- supported compression algorithms;
- compression thresholds;
- read, write, idle, and total invocation timeouts.

A binding may narrow a gateway limit but cannot widen it.

## 14. Responses and outcome coverage

Reachable outcomes are grouped into:

1. transport/admission outcomes;
2. dispatch outcomes;
3. completion outcomes.

Every reachable operation result, declared error, and delivery-specific dispatch outcome has exactly one response mapping or a documented edition-provided mapping. `system.internal` is always reachable and uses the standard problem response unless explicitly narrowed by a permitted policy.

Response selection is by typed outcome reference, never by Go error text, dynamic status value, or source order.

A response declares status, headers/cookies, and zero or one body mapping. Status/body combinations forbidden by HTTP, including bodies on 204 and 304, are compile errors.

## 15. Security boundary

Gateway exposure does not authenticate or authorize. Internet and private-network gateways still require explicit binding authentication and authorization. Intentional anonymous access uses `std.authentication.none` plus an explicit allow policy such as `std.authorization.public`; `std.authorization.none` grants nothing and denies invocation.

Forwarded headers are rejected unless the gateway has both:

- a trusted-proxy policy that identifies accepted network hops;
- a forwarded-header policy specifying which values may affect scheme, host, client address, and trace context.

Untrusted forwarded values remain ordinary headers and cannot alter security context.

CORS is a gateway policy. Preflight behavior is generated from active routes and the effective CORS policy. CORS never grants authorization.

TLS requirements belong to the deployment listener but cannot contradict gateway exposure or project security policy.

## 16. Guarantees

Each compiled facet records one guarantee:

- `framework_enforced`: generated/runtime adapters enforce the manifest;
- `implementation_declared`: the implementation claims behavior Scenery cannot enforce.

All codecs in `scenery.http-codec/v1` are framework-enforced. A future coupled profile may use implementation-declared body semantics, but admission, limits, and pipeline facets remain independently classified.

Generated clients MUST NOT present an implementation-declared facet as framework-verified.

## 17. Artifact projections and descriptors

The compiler computes `http_surface_revision[gateway]` from the gateway, every active route under it, reachable request/response types, effective codec/mapping/negotiation/limit contract, and framework guarantee classifications. Deployment listeners and implementation bodies are excluded.

`openapi_revision[gateway]` additionally includes the exact OpenAPI generator profile and projection options. A TypeScript client uses `typescript_client_revision[target]` from [SCENERY_TYPESCRIPT_CLIENT_V1.md](SCENERY_TYPESCRIPT_CLIENT_V1.md), which may cover one or more gateways.

Every generated HTTP runtime table, OpenAPI document, and client descriptor records its smallest projection revision plus the whole `contract_revision` for provenance. A policy or internal-resource change outside the projection does not invalidate the artifact. A route, codec, reachable type, admission guarantee exposed in metadata, or mapping change does.

## 18. House example

~~~hcl
http_gateway "public_api" {
  exposure        = "internet"
  base_path       = "/"
  cors            = std.cors.application
  trusted_proxies = std.trusted_proxies.none
  forwarded       = std.forwarded_headers.reject
}

binding "process_scene_http" {
  gateway   = http_gateway.public_api
  operation = operation.process_scene
  execution = execution.process_scene_direct
  protocol  = "http"
  delivery  = "call"

  authentication = authentication.standard
  authorization  = authorization.member
  pipeline       = pipeline.http_default

  http {
    method        = "POST"
    path          = "/house/process"
    codec_profile = std.codec.http_json_v1

    context "tenant_id" {
      from = principal.tenant_id
      to   = operation.process_scene.input.tenant_id
    }

    body {
      codec = "json"
      to    = operation.process_scene.input
      except = [
        operation.process_scene.input.tenant_id,
      ]
    }

    response "processed" {
      when   = result.processed
      status = 200

      body {
        codec = "json"
        from  = result.processed
      }
    }

    response "invalid_request" {
      when   = transport.invalid_request
      status = 422

      body {
        codec = "problem_json"
        from  = transport.problem
      }
    }

    response "invalid_input" {
      when   = error.invalid_input
      status = 422

      body {
        codec = "problem_json"
        from  = error.invalid_input
      }
    }
  }
}
~~~

## 19. Conformance requirements

A conforming implementation passes fixtures for:

- gateway-scoped route collision and non-collision;
- base-path joining;
- binding exposure narrowing and widening rejection;
- path percent-decoding rejection cases;
- query list/set/scalar behavior;
- header and cookie repetition;
- every scalar form;
- arbitrary-precision integers and decimals;
- optional versus nullable JSON behavior;
- closed and preserving records;
- enum and union unknown values;
- canonical set order;
- media negotiation tie-breakers;
- unsupported media and unacceptable response outcomes;
- compressed and decompressed limits;
- multipart limits and undeclared parts;
- trusted and untrusted forwarded headers;
- CORS preflight projection;
- complete response coverage;
- rejection of `codec = "raw"`;
- gateway, OpenAPI, and TypeScript artifact projection revisions;
- deterministic manifest and client generation.

## Appendix A: Deliberate exclusions

Version 1 does not define:

- raw Go HTTP objects;
- WebSocket or arbitrary upgraded connections;
- unbuffered request bodies;
- bidirectional streams;
- non-UTF-8 text;
- wildcard path segments other than terminal `{name...}` under the separately claimed path-tail profile;
- implementation-selected status codes outside declared outcomes.

These require separately versioned profiles.

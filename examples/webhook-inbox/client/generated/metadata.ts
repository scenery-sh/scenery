// Code generated for the application. DO NOT EDIT.
import { freezeMetadata } from "./runtime.js";
export const clientMetadata = /* @__PURE__ */ freezeMetadata({
  "bindings": [
    "inbox/binding/receive_http",
    "inbox/binding/status_http"
  ],
  "compatibilityCatalog": "compatibility-core",
  "contractRevision": "sha256:66a1b3527215128f71957aec631d3e74c8ccc1ac443c1f99f79164979bfdfeb4",
  "gateways": [
    "http_gateway.public_api"
  ],
  "generator": "generate.typescript",
  "guaranteeClassifications": {
    "inbox/binding/receive_http": "framework_enforced",
    "inbox/binding/status_http": "framework_enforced"
  },
  "operationMethods": {
    "inbox/operation/process": [
      "process"
    ],
    "inbox/operation/status": [
      "status"
    ]
  },
  "resources": [
    "inbox/operation/process",
    "inbox/operation/status",
    "inbox/record/event",
    "inbox/record/lookup",
    "inbox/record/processed_event"
  ],
  "target": "app/typescript_client/public_api",
  "typescriptClientRevision": "sha256:c4d467174cef837b4725316acf8911319bbdde55179dff24d813964af83aa24d"
} as const);

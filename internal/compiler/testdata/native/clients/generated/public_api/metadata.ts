// Code generated for the application. DO NOT EDIT.
import { freezeMetadata } from "./runtime.js";
export const clientMetadata = /* @__PURE__ */ freezeMetadata({
  "bindings": [
    "house/binding/process_scene_http"
  ],
  "compatibilityCatalog": "compatibility-core",
  "contractRevision": "sha256:9f856c526e0d5f7214ca0993878d028340372c593688118bb82da6b9d5710afd",
  "gateways": [
    "http_gateway.public_api"
  ],
  "generator": "generate.typescript",
  "guaranteeClassifications": {
    "house/binding/process_scene_http": "framework_enforced"
  },
  "operationMethods": {
    "house/operation/process_scene": [
      "processScene"
    ]
  },
  "resources": [
    "house/operation/process_scene",
    "house/record/process_scene_input",
    "house/record/process_scene_result"
  ],
  "target": "app/typescript_client/public_api",
  "typescriptClientRevision": "sha256:e3e81eddcbc20a35cff41244bbcdbd4a13b5421fe47c85cb5f8f19aaa86f3ea6"
} as const);

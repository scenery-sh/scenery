// Code generated for the application. DO NOT EDIT.
import { freezeMetadata } from "./runtime.js";
export const clientMetadata = /* @__PURE__ */ freezeMetadata({
  "bindings": [
    "house/binding/process_scene_http"
  ],
  "compatibilityCatalog": "compatibility-core",
  "contractRevision": "sha256:666e84ce3e8bc1add785f56dd1fa2372be3aff054265883b2cf7c16ee9940024",
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

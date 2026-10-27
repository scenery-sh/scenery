// Code generated for the application. DO NOT EDIT.
import { freezeMetadata } from "./runtime.js";
export const clientMetadata = /* @__PURE__ */ freezeMetadata({
  "bindings": [
    "house/binding/list_scene_events_http",
    "house/binding/process_scene_http"
  ],
  "compatibilityCatalog": "compatibility-core",
  "contractRevision": "sha256:d17d303d42859564e1b1e17e4415fa563f85c6ddb73d6eb4f5b293fcbfc01689",
  "gateways": [
    "http_gateway.public_api"
  ],
  "generator": "generate.typescript",
  "guaranteeClassifications": {
    "house/binding/list_scene_events_http": "framework_enforced",
    "house/binding/process_scene_http": "framework_enforced"
  },
  "operationMethods": {
    "house/operation/list_scene_events": [
      "listSceneEvents"
    ],
    "house/operation/process_scene": [
      "processScene"
    ]
  },
  "resources": [
    "house/operation/list_scene_events",
    "house/operation/process_scene",
    "house/record/process_scene_input",
    "house/record/process_scene_result",
    "house/record/scene_event",
    "house/record/scene_events_input",
    "house/record/scene_events_result"
  ],
  "target": "app/typescript_client/public_api",
  "typescriptClientRevision": "sha256:fa17453e12138951b920ae25df9cf38df77a3c553886d81fd1a274b7dc7f8ca4"
} as const);

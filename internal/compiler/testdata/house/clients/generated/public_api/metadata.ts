// Code generated for the application. DO NOT EDIT.
import { freezeMetadata } from "./runtime.js";
export const clientMetadata = /* @__PURE__ */ freezeMetadata({
  "bindings": [
    "house/binding/list_scene_events_http",
    "house/binding/process_scene_http"
  ],
  "compatibilityCatalog": "compatibility-core",
  "contractRevision": "sha256:e219406853996df625380bc38c39f80dd808b94707c55844881d2cf41961c4ca",
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
  "typescriptClientRevision": "sha256:b56fd4505d1968d83f69f485364d2a7ce53f784037eb724b013342e9528f425e"
} as const);

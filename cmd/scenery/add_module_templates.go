package main

import (
	"fmt"

	cp "scenery.sh/internal/clientprojection"
)

const modulePackageTemplate = `package %q {
  %s
}

input "gateway" { type = resource_ref("http_gateway") }

service %q {
  runtime = %q
  implementation { constructor = %q }
}

operation "ping" {
  service = service.%s
  input = std.type.unit
  handler { method = %q }
  result "ok" { type = string }
}

execution "ping" {
  operation = operation.ping
  mode = "direct"
  timeout = "10s"
}

binding "ping" {
  gateway = var.gateway
  operation = operation.ping
  execution = execution.ping
  protocol = "http"
  delivery = "call"
  authentication = std.authentication.none
  authorization = std.authorization.public
  pipeline = std.pipeline.empty
  http {
    method = "GET"
    path = "/modules/%s/ping"
    codec_profile = std.codec.http_json_v1
    response "ok" {
      when = result.ok
      status = 200
      body {
        codec = "json"
        from = result.ok
      }
    }
  }
}
`

func (s *moduleScaffold) addGoFiles(importPath string) {
	s.create(s.Module+"/service.go", fmt.Sprintf(`package %s

import (
	"context"

	contract %q
)

type Service struct{}

func NewService(context.Context, contract.%sConstructorInput) (*Service, error) {
	return &Service{}, nil
}

func (*Service) Ping(context.Context, contract.PingInput) (contract.PingOutcome, error) {
	return contract.PingOk{Value: "ok"}, nil
}
`, s.Module, importPath+"/scenerycontract", cp.GoName(s.Module)))
	s.create(s.Module+"/service_test.go", fmt.Sprintf(`package %s

import (
	"testing"

	contract %q
)

func TestPing(t *testing.T) {
	service, err := NewService(t.Context(), contract.%sConstructorInput{})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := service.Ping(t.Context(), contract.PingInput{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != (contract.PingOk{Value: "ok"}) {
		t.Fatalf("unexpected outcome: %%#v", outcome)
	}
}
`, s.Module, importPath+"/scenerycontract", cp.GoName(s.Module)))
}

func (s *moduleScaffold) addRustFiles(crate string) {
	manifest := fmt.Sprintf(`[package]
name = %q
version = "0.1.0"
edition = "2024"
publish = false

[dependencies]
scenerycontract = { package = %q, path = "scenerycontract" }
`, crate, crate+"-contract")
	if s.cargoDir == s.Module {
		manifest += "\n[workspace]\nresolver = \"3\"\n"
	}
	s.create(s.Module+"/Cargo.toml", manifest)
	s.create(s.Module+"/src/lib.rs", fmt.Sprintf(`use scenerycontract::{%[1]sConstructorInput, %[1]sService, PingInput, PingOutcome, sdk};

pub struct Service;

pub fn new_service(_: %[1]sConstructorInput) -> sdk::Result<Service> {
    Ok(Service)
}

impl %[1]sService for Service {
    async fn ping(&self, _: sdk::Context, _: PingInput) -> sdk::Result<PingOutcome> {
        Ok(ping())
    }
}

fn ping() -> PingOutcome {
    PingOutcome::Ok("ok".into())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn returns_ok() {
        let PingOutcome::Ok(value) = ping();
        assert_eq!(value, "ok");
    }
}
`, cp.GoName(s.Module)))
}

func (s *moduleScaffold) addJSFiles() {
	var types, constructor, contextType, context string
	if s.Runtime == "ts" {
		types = "import type { Constructor } from \"./scenerycontract/index.js\";\n\n"
		constructor = ": Constructor"
		contextType = ", type Context"
		context = " as Context"
	}
	s.create(s.Module+"/service."+s.Runtime, fmt.Sprintf(`%sexport const newService%s = () => ({
  ping() {
    return { kind: "result", name: "ok", value: "ok" };
  },
});
`, types, constructor))
	s.create(s.Module+"/service.test."+s.Runtime, fmt.Sprintf(`import { expect, test } from "bun:test";
import { clients%s } from "./scenerycontract/index.js";
import { newService } from "./service.js";

test("returns ok", async () => {
  const service = await newService({ clients, config: undefined, databases: {} });
  expect(await service.ping({}%s, {})).toEqual({ kind: "result", name: "ok", value: "ok" });
});
`, contextType, context))
}

func (s *moduleScaffold) addPythonFiles() {
	s.create(s.Module+"/service.py", `from __future__ import annotations

from scenerycontract import ConstructorInput, Context, PingInput, PingOk, PingOutcome


class Service:
    async def ping(self, ctx: Context, input: PingInput) -> PingOutcome:
        return PingOk(value="ok")


def new_service(input: ConstructorInput) -> Service:
    return Service()
`)
	s.create(s.Module+"/test_service.py", `from __future__ import annotations

import asyncio
import unittest
from typing import Any, cast

from scenerycontract import ConstructorInput, Context, PingOk, clients
from service import new_service


class ServiceTests(unittest.TestCase):
    def test_returns_ok(self) -> None:
        service = new_service(ConstructorInput(clients=clients, config=None, databases={}))
        outcome = asyncio.run(service.ping(cast(Context, cast(Any, None)), {}))
        self.assertEqual(outcome, PingOk(value="ok"))


if __name__ == "__main__":
    unittest.main()
`)
}

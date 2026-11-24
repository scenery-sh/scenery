package generate

import (
	"fmt"
	"strings"
)

func (p rustContractProjection) renderType(source *strings.Builder, resource Resource) error {
	name := p.typeName(resource)
	switch resource.Kind {
	case "scenery.record":
		source.WriteString("#[derive(Clone, Debug, Deserialize, Serialize)]\n#[serde(crate = \"serde\")]\n")
		if stringValue(resource.Spec["unknown_fields"]) == "reject" || resource.Spec["unknown_fields"] == nil {
			source.WriteString("#[serde(deny_unknown_fields)]\n")
		}
		fmt.Fprintf(source, "pub struct %s {\n", name)
		for _, field := range namedChildren(resource.Spec, "field") {
			label := stringValue(field["name"])
			expression := typeExpression(field["type"])
			fmt.Fprintf(source, "    #[serde(rename = %q", wireName(field, label))
			switch {
			case strings.HasPrefix(expression, "optional("):
				source.WriteString(", default, skip_serializing_if = \"Option::is_none\", deserialize_with = \"sdk::present\"")
			case strings.HasPrefix(expression, "nullable("):
				source.WriteString(", deserialize_with = \"serde::Deserialize::deserialize\"")
			}
			fmt.Fprintf(source, ")]\n    pub %s: %s,\n", rustIdent(label), p.rustType(field["type"], resource.Module))
		}
		if stringValue(resource.Spec["unknown_fields"]) == "preserve" {
			source.WriteString("    #[serde(flatten)]\n    pub extra: std::collections::BTreeMap<String, Box<RawValue>>,\n")
		}
		source.WriteString("}\n\n")
	case "scenery.enum":
		if resource.Spec["open"] == true {
			fmt.Fprintf(source, "#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]\n#[serde(crate = \"serde\")]\n#[serde(transparent)]\npub struct %s(pub String);\n\n", name)
			return nil
		}
		fmt.Fprintf(source, "#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]\n#[serde(crate = \"serde\")]\npub enum %s {\n", name)
		for _, value := range namedChildren(resource.Spec, "value") {
			label := stringValue(value["name"])
			fmt.Fprintf(source, "    #[serde(rename = %q)]\n    %s,\n", wireName(value, label), goName(label))
		}
		source.WriteString("}\n\n")
	case "scenery.union":
		return p.renderRustUnion(source, resource)
	}
	return nil
}

func (p rustContractProjection) renderRustUnion(source *strings.Builder, resource Resource) error {
	name := p.typeName(resource)
	tag := stringValue(resource.Spec["discriminator"])
	if tag == "" {
		return fmt.Errorf("union %s has no discriminator", resource.Address)
	}
	variants := namedChildren(resource.Spec, "variant")
	open := resource.Spec["open"] == true
	fmt.Fprintf(source, "#[derive(Clone, Debug)]\npub enum %s {\n", name)
	for _, variant := range variants {
		fmt.Fprintf(source, "    %s(%s),\n", goName(stringValue(variant["name"])), p.rustType(variant["type"], resource.Module))
	}
	if open {
		source.WriteString("    Unknown { tag: String, payload: Box<RawValue> },\n")
	}
	source.WriteString("}\n")
	fmt.Fprintf(source, "impl Serialize for %s {\n    fn serialize<S: serde::Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {\n        let (tag, payload) = match self {\n", name)
	for _, variant := range variants {
		label := stringValue(variant["name"])
		fmt.Fprintf(source, "            Self::%s(value) => (%q, serde_json::to_value(value)),\n", goName(label), wireName(variant, label))
	}
	if open {
		source.WriteString("            Self::Unknown { tag, payload } => (tag.as_str(), serde_json::from_str(payload.get())),\n")
	}
	fmt.Fprintf(source, "        };\n        let mut payload = payload.map_err(serde::ser::Error::custom)?;\n        let object = payload.as_object_mut().ok_or_else(|| serde::ser::Error::custom(\"union payload must be an object\"))?;\n        object.insert(%q.into(), tag.into());\n        payload.serialize(serializer)\n    }\n}\n", tag)
	fmt.Fprintf(source, "impl<'de> Deserialize<'de> for %s {\n    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {\n        let mut object = serde_json::Map::<String, serde_json::Value>::deserialize(deserializer)?;\n        let tag = object.remove(%q).and_then(|value| value.as_str().map(String::from)).ok_or_else(|| serde::de::Error::custom(\"union discriminator required\"))?;\n        let value = serde_json::Value::Object(object);\n        match tag.as_str() {\n", name, tag)
	for _, variant := range variants {
		label := stringValue(variant["name"])
		fmt.Fprintf(source, "            %q => serde_json::from_value(value).map(Self::%s).map_err(serde::de::Error::custom),\n", wireName(variant, label), goName(label))
	}
	if open {
		source.WriteString("            _ => Ok(Self::Unknown { tag, payload: serde_json::value::to_raw_value(&value).map_err(serde::de::Error::custom)? }),\n")
	} else {
		source.WriteString("            _ => Err(serde::de::Error::custom(\"unknown union discriminator\")),\n")
	}
	source.WriteString("        }\n    }\n}\n\n")
	return nil
}

func (p rustContractProjection) renderService(source *strings.Builder, service Resource, operations []Resource, clients []goClientBinding, resources []Resource) error {
	serviceName := goName(service.Name)
	fmt.Fprintf(source, "#[derive(Default)]\npub struct %sConstructorInput {\n", serviceName)
	for _, client := range clients {
		fmt.Fprintf(source, "    pub %s: %sClient,\n", rustIdent(client.Name), goName(client.Name))
	}
	source.WriteString("}\n\n")
	for _, client := range clients {
		name := client.operationName()
		if client.Projected {
			p.renderRustOperation(source, client.Operation, name)
		}
		clientName := goName(client.Name) + "Client"
		fmt.Fprintf(source, "#[derive(Clone, Copy, Default)]\npub struct %s;\nimpl %s {\n", clientName, clientName)
		if client.Delivery == "enqueue" {
			fmt.Fprintf(source, "    pub async fn enqueue(&self, ctx: &sdk::Context, input: %sInput) -> sdk::Result<sdk::DispatchReceipt> {\n        ctx.enqueue(%q, &input).await\n    }\n", name, client.Name)
		} else {
			fmt.Fprintf(source, "    pub async fn invoke(&self, ctx: &sdk::Context, input: %sInput) -> sdk::Result<%sOutcome> {\n        let outcome = ctx.invoke(%q, &input).await?;\n        %sOutcome::from_wire(&outcome)\n    }\n", name, name, client.Name, name)
		}
		source.WriteString("}\n\n")
	}
	fmt.Fprintf(source, "pub trait %sService: Send + Sync + 'static {\n", serviceName)
	for _, phase := range []string{"start", "stop"} {
		if method := stringValue(specChild(service, "lifecycle")[phase]); method != "" {
			fmt.Fprintf(source, "    fn %s(&self) -> impl std::future::Future<Output = sdk::Result<()>> + Send;\n", rustMethodName(method))
		}
	}
	for _, operation := range operations {
		name := goName(operation.Name)
		method := stringValue(specChild(operation, "handler")["method"])
		fmt.Fprintf(source, "    %s;\n", rustOperationSignature(method, name, operationUsesHTTPStream(operation, resources)))
	}
	source.WriteString("}\n\n")
	for _, operation := range operations {
		p.renderRustOperation(source, operation, goName(operation.Name))
	}
	fmt.Fprintf(source, "pub async fn dispatch_%s(service: &impl %sService, invocation: sdk::Invocation, ctx: sdk::Context) -> sdk::Result<sdk::Output> {\n    match invocation.operation.rsplit('/').next().unwrap_or_default() {\n", rustIdent(service.Name), serviceName)
	for _, operation := range operations {
		name := goName(operation.Name)
		method := rustMethodName(stringValue(specChild(operation, "handler")["method"]))
		fmt.Fprintf(source, "        %q => {\n            let input: %sInput = serde_json::from_str(invocation.input.get())?;\n", operation.Name, name)
		if operationUsesHTTPStream(operation, resources) {
			fmt.Fprintf(source, "            let (outcome, stream) = service.%s(ctx, input).await?;\n            Ok(sdk::Output { outcome: outcome.to_wire()?, stream })\n", method)
		} else {
			fmt.Fprintf(source, "            let output = service.%s(ctx, input).await?;\n            Ok(sdk::Output::json(output.to_wire()?))\n", method)
		}
		source.WriteString("        }\n")
	}
	source.WriteString("        _ => Err(\"unknown operation\".into()),\n    }\n}\n\n")
	return nil
}

func (p rustContractProjection) renderRustOperation(source *strings.Builder, op Resource, name string) {
	inputType := p.rustType(op.Spec["input"], op.Module)
	if inputType != name+"Input" {
		fmt.Fprintf(source, "pub type %sInput = %s;\n", name, inputType)
	}
	fmt.Fprintf(source, "#[derive(Clone, Debug)]\npub enum %sOutcome {\n", name)
	for _, kind := range []string{"result", "error"} {
		for _, variant := range namedChildren(op.Spec, kind) {
			fmt.Fprintf(source, "    %s(%s),\n", goName(stringValue(variant["name"])), p.rustType(variant["type"], op.Module))
		}
	}
	fmt.Fprintf(source, "}\nimpl %sOutcome {\n    pub fn to_wire(&self) -> sdk::Result<Box<RawValue>> {\n        match self {\n", name)
	for _, kind := range []string{"result", "error"} {
		for _, variant := range namedChildren(op.Spec, kind) {
			label := stringValue(variant["name"])
			fmt.Fprintf(source, "            Self::%s(value) => sdk::encode_outcome(value, %q, %q),\n", goName(label), kind, label)
		}
	}
	source.WriteString("        }\n    }\n    pub fn from_wire(raw: &RawValue) -> sdk::Result<Self> {\n        let (kind, name, value) = sdk::decode_outcome(raw)?;\n        match (kind.as_str(), name.as_str()) {\n")
	for _, kind := range []string{"result", "error"} {
		for _, variant := range namedChildren(op.Spec, kind) {
			label := stringValue(variant["name"])
			fmt.Fprintf(source, "            (%q, %q) => Ok(Self::%s(serde_json::from_str(value.get())?)),\n", kind, label, goName(label))
		}
	}
	source.WriteString("            _ => Err(\"unknown outcome\".into()),\n        }\n    }\n}\n\n")
}

func rustWorkerSource(module rustSourceModule, service Resource) string {
	constructor := rustMethodName(stringValue(specChild(service, "implementation")["constructor"]))
	lifecycle := specChild(service, "lifecycle")
	serviceName := goName(service.Name)
	start, stop := "", "serving"
	if method := stringValue(lifecycle["start"]); method != "" {
		start = fmt.Sprintf("        scenerycontract::%sService::%s(&*service).await?;\n", serviceName, rustMethodName(method))
	}
	if method := stringValue(lifecycle["stop"]); method != "" {
		stop = fmt.Sprintf("serving.and(scenerycontract::%sService::%s(&*service).await)", serviceName, rustMethodName(method))
	}
	return fmt.Sprintf(`// Generated by Scenery. DO NOT EDIT.
use scenerycontract::sdk;
use std::sync::Arc;

fn main() -> sdk::Result<()> {
    sdk::run(async {
        let launch = sdk::Launch::from_args()?;
        let service = Arc::new(%s::%s(scenerycontract::%sConstructorInput::default())?);
%s        let worker = Arc::clone(&service);
        let serving = sdk::serve(launch, move |invocation, context| {
            let service = Arc::clone(&worker);
            Box::pin(async move {
                scenerycontract::dispatch_%s(&*service, invocation, context).await
            })
        }).await;
        %s
    })
}
`, strings.ReplaceAll(module.crate, "-", "_"), constructor, serviceName, start, rustIdent(service.Name), stop)
}

func specChild(resource Resource, key string) map[string]any {
	child, _ := resource.Spec[key].(map[string]any)
	return child
}

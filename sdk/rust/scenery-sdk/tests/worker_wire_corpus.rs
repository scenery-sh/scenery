use scenery_sdk::{self as sdk, serde, serde_json};
use serde::{Deserialize, Serialize};
use serde_json::value::RawValue;

include!("preserved_record.rs.inc");

#[derive(Deserialize)]
struct Case {
    name: String,
    #[serde(rename = "type")]
    wire_type: String,
    wire: String,
    #[serde(default)]
    reject: bool,
}

#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Patch {
    #[serde(
        default,
        deserialize_with = "sdk::present",
        skip_serializing_if = "Option::is_none"
    )]
    note: Option<Option<String>>,
}

#[derive(Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
enum Status {
    InReview,
}

fn round_trip<T: Serialize + serde::de::DeserializeOwned>(wire: &str) -> sdk::Result<String> {
    let value: T = serde_json::from_str(wire)?;
    Ok(serde_json::to_string(&value)?)
}

fn outcome(wire: &str) -> sdk::Result<String> {
    let raw = serde_json::value::RawValue::from_string(wire.to_owned())?;
    let (kind, name, payload) = sdk::decode_outcome(&raw)?;
    Ok(sdk::encode_outcome(&payload, &kind, &name)?
        .get()
        .to_owned())
}

#[test]
fn worker_wire_v1_corpus() {
    let cases: Vec<Case> = serde_json::from_str(include_str!(
        "../../../../internal/contract/testdata/worker-wire-v1.json"
    ))
    .unwrap();
    for entry in cases {
        let wire = entry.wire.as_str();
        let got = match entry.wire_type.as_str() {
            "int64" => round_trip::<sdk::Int64>(wire),
            "float32" => round_trip::<sdk::Float32>(wire),
            "float64" => round_trip::<sdk::Float64>(wire),
            "uint64" => round_trip::<sdk::UInt64>(wire),
            "decimal" => round_trip::<sdk::Decimal>(wire),
            "datetime" => round_trip::<sdk::Timestamp>(wire),
            "duration" => round_trip::<sdk::Duration>(wire),
            "bytes" => round_trip::<sdk::ContractBytes>(wire),
            "probe/record/patch" => round_trip::<Patch>(wire),
            "probe/record/note" => round_trip::<Note>(wire),
            "probe/enum/status" => round_trip::<Status>(wire),
            "probe/outcome/quote" => outcome(wire),
            other => panic!("unexpected corpus type {other}"),
        };
        if entry.reject {
            assert!(got.is_err(), "accepted {}", entry.name);
        } else {
            assert_eq!(got.unwrap(), entry.wire, "{}", entry.name);
        }
    }
}

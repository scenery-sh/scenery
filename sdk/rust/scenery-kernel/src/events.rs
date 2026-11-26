use std::collections::BTreeSet;
use std::sync::{Arc, Mutex as StdMutex};

use anyhow::{Context, Result, ensure};
use hyper::StatusCode;
use hyper::body::Incoming;
use scenery_wire::{EventEmitCall, EventReceipt, MAX_METADATA};
use serde::Deserialize;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use tokio::sync::{OwnedSemaphorePermit, Semaphore};
use tokio::time::Instant;
use tokio_postgres::Client;

use crate::durable::{DuplicateExecution, connect, dispatch_key, dispatch_memo, enqueue_job};
use crate::host::{Call, Fault, Host, Reply, Scope, json_reply};
use crate::plan::{Key, Policy, Service, nullable};
use crate::process::{hex, random_hex};
use crate::{transport, values};

const MAX_EVENT_DEPTH: u32 = 16;
const MAX_TX_CONNECTIONS: usize = 8;

/// Bounded pool of dedicated PostgreSQL connections for multi-statement event
/// transactions. A connection returns to the pool only after a clean commit.
pub(crate) struct TxPool {
    permits: Arc<Semaphore>,
    idle: Arc<StdMutex<Vec<Client>>>,
}

pub(crate) struct TxClient {
    client: Option<Client>,
    idle: Arc<StdMutex<Vec<Client>>>,
    _permit: OwnedSemaphorePermit,
}

impl TxPool {
    pub(crate) fn new() -> Self {
        Self {
            permits: Arc::new(Semaphore::new(MAX_TX_CONNECTIONS)),
            idle: Arc::new(StdMutex::new(Vec::new())),
        }
    }

    pub(crate) async fn checkout(&self) -> Result<TxClient> {
        let permit = self
            .permits
            .clone()
            .acquire_owned()
            .await
            .context("transaction pool closed")?;
        let cached = {
            let mut idle = self.idle.lock().expect("tx pool mutex");
            let mut found = None;
            while let Some(client) = idle.pop() {
                if !client.is_closed() {
                    found = Some(client);
                    break;
                }
            }
            found
        };
        let client = match cached {
            Some(client) => client,
            None => connect().await?.0,
        };
        Ok(TxClient {
            client: Some(client),
            idle: self.idle.clone(),
            _permit: permit,
        })
    }
}

impl TxClient {
    fn client_mut(&mut self) -> &mut Client {
        self.client.as_mut().expect("active transaction client")
    }

    fn release(mut self) {
        if let Some(client) = self.client.take()
            && !client.is_closed()
        {
            self.idle.lock().expect("tx pool mutex").push(client);
        }
    }
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct Event {
    address: String,
    external_name: String,
    payload_type: String,
    #[serde(deserialize_with = "nullable")]
    dedupe: Vec<Key>,
    #[serde(deserialize_with = "nullable")]
    subscriptions: Vec<Subscription>,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
struct Subscription {
    address: String,
    service: String,
    operation: String,
    execution: String,
    policy: Policy,
}

pub(crate) fn verify_events(events: &[Event], workers: &[Service]) -> Result<()> {
    let mut addresses = BTreeSet::new();
    let mut external_names = BTreeSet::new();
    for event in events {
        ensure!(
            !event.address.is_empty() && addresses.insert(event.address.as_str()),
            "duplicate or empty event address in bundle"
        );
        ensure!(
            !event.external_name.is_empty() && external_names.insert(event.external_name.as_str()),
            "duplicate or empty event external_name in bundle"
        );
        for subscription in &event.subscriptions {
            let valid = workers
                .iter()
                .find(|worker| worker.address == subscription.service)
                .and_then(|worker| worker.durable.get(&subscription.execution))
                .is_some_and(|plan| plan.operation == subscription.operation);
            ensure!(
                valid,
                "event subscription {} does not match a declared durable execution",
                subscription.address
            );
        }
    }
    for worker in workers {
        for (publisher, address) in &worker.publishers {
            ensure!(
                addresses.contains(address.as_str()),
                "service {} publisher {} references unknown event {}",
                worker.address,
                publisher,
                address
            );
        }
    }
    Ok(())
}

pub(crate) async fn emit(
    host: &Host,
    scope: &Scope,
    service: &Service,
    body: Incoming,
) -> Result<Reply, Fault> {
    let started = Instant::now();
    let bytes = transport::read(body, MAX_METADATA)
        .await
        .map_err(|_| Fault::invalid())?;
    let request: EventEmitCall<String> =
        serde_json::from_slice(&bytes).map_err(|_| Fault::invalid())?;
    let address = service
        .publishers
        .get(&request.publisher)
        .ok_or_else(Fault::forbidden)?;
    let event = host
        .bundle
        .host
        .events
        .iter()
        .find(|event| &event.address == address)
        .ok_or_else(|| Fault::internal("publisher event is absent from bundle"))?;
    let call = scope.call.child_span()?;
    let result = async {
        let payload = event.admit(&host.bundle.types, &call, request.payload.get().as_bytes())?;
        let receipt = event
            .persist(host, service, &call, &payload)
            .await
            .map_err(|error| {
                if error.is::<DuplicateExecution>() {
                    Fault::new(409, "dispatch.rejected", "could not enqueue execution")
                } else {
                    Fault::internal(error)
                }
            })?;
        if !receipt.deduplicated && receipt.deliveries > 0 {
            host.engine.wake_consumers();
        }
        Ok::<_, Fault>(json_reply(StatusCode::OK, &json!({"output": receipt})))
    }
    .await;
    host.telemetry.span(
        &call,
        service,
        address,
        started.elapsed(),
        result.as_ref().err().map_or("", |fault| fault.code),
    );
    result
}

fn event_dedupe_key(payload: &Value, keys: &[Key]) -> Result<String> {
    let raw = dispatch_key(payload, keys)?;
    if raw.is_empty() {
        return Ok(String::new());
    }
    Ok(format!("sha256:{}", hex(&Sha256::digest(raw.as_bytes()))))
}

impl Event {
    fn admit(&self, types: &values::Types, call: &Call, raw: &[u8]) -> Result<Value, Fault> {
        if call.event_depth >= MAX_EVENT_DEPTH {
            return Err(Fault::new(
                409,
                "failed_precondition",
                "event cascade depth limit exceeded",
            ));
        }
        let payload = values::parse(raw)
            .and_then(|value| values::normalize(types, &self.payload_type, value))
            .map_err(|_| Fault::invalid())?;
        for subscription in &self.subscriptions {
            Host::authorize(call, &subscription.policy, &payload)?;
        }
        Ok(payload)
    }

    async fn persist(
        &self,
        host: &Host,
        service: &Service,
        call: &Call,
        payload: &Value,
    ) -> Result<EventReceipt> {
        let key = event_dedupe_key(payload, &self.dedupe)?;
        let id = random_hex::<16>()?;
        let input = values::encode(payload).into_bytes();
        let caller = call.principal_id();
        let mut memo = dispatch_memo(call);
        let mut conn = host.engine.tx_pool.checkout().await?;
        let tx = conn.client_mut().transaction().await?;
        let inserted = tx.query_opt(
            "INSERT INTO scenery.domain_events(id, event_name, event_address, publisher_service, dedupe_key, payload_blob, metadata_json, created_by)
             VALUES($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8)
             ON CONFLICT (event_name, dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING RETURNING id",
            &[&id, &self.external_name, &self.address, &service.address, &key, &input, &memo, &caller],
        ).await?;
        if inserted.is_none() {
            // This statement takes a new read-committed snapshot after the
            // conflicting insert commits, including for concurrent emitters.
            let row = tx.query_one(
                "SELECT id, delivery_count FROM scenery.domain_events WHERE event_name = $1 AND dedupe_key = $2",
                &[&self.external_name, &key],
            ).await?;
            tx.commit().await?;
            conn.release();
            return Ok(EventReceipt {
                event_id: row.get(0),
                event: self.external_name.clone(),
                deliveries: u32::try_from(row.get::<_, i32>(1))?,
                deduplicated: true,
            });
        }
        memo["event_id"] = json!(id);
        memo["event_name"] = json!(self.external_name);
        memo["event_depth"] = json!(call.event_depth + 1);
        let mut deliveries = 0i32;
        for subscription in &self.subscriptions {
            let plan = host
                .service(&subscription.service)
                .and_then(|owner| owner.durable.get(&subscription.execution))
                .filter(|plan| plan.operation == subscription.operation)
                .context("subscriber execution is absent from bundle")?;
            memo["caller_binding"] = json!(subscription.address);
            let key = format!("event:{id}:{}", subscription.address);
            let (_, inserted) = enqueue_job(&tx, plan, payload, &memo, Some(key)).await?;
            deliveries += i32::from(inserted);
        }
        tx.execute(
            "UPDATE scenery.domain_events SET delivery_count = $2 WHERE id = $1",
            &[&id, &deliveries],
        )
        .await?;
        tx.commit().await?;
        conn.release();
        Ok(EventReceipt {
            event_id: id,
            event: self.external_name.clone(),
            deliveries: u32::try_from(deliveries)?,
            deduplicated: false,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::auth::Claims;
    use crate::host::ScopeGuard;

    fn sample_event(rules: Value) -> Event {
        serde_json::from_value(json!({
            "Address": "example/event/accepted",
            "ExternalName": "orders.accepted",
            "PayloadType": "example/record/payload",
            "Dedupe": [{"Name": "amount", "Type": "int64"}],
            "Subscriptions": [
                {"Address": "orders/subscription/receive", "Service": "orders/service/orders",
                 "Operation": "orders/operation/receive", "Execution": "orders/execution/receive",
                 "Policy": {
                    "BindingAddress":"orders/subscription/receive",
                    "AuthorizationStrategy":"deny_unless_allowed",
                    "AuthorizationRules": rules,
                    "TotalInvocationTimeoutNanos":0, "MaxRequestHeaderBytes":0,
                    "CORS":"none", "AllowedOrigins":[], "TransportStatuses":{}
                 }}
            ]
        }))
        .unwrap()
    }

    #[tokio::test]
    async fn admission_normalizes_payload_before_policy_and_keys() {
        let mut event = sample_event(json!([
            {"Name":"permit", "Effect":"allow", "Expression":"true"},
            {"Name":"deny_test", "Effect":"deny", "Expression":"input.id == \"denied\""}
        ]));
        let constraints = json!({"Pattern": "", "Format": "", "UniqueItems": false});
        let types = serde_json::from_value(json!({
            "example/record/payload": {
                "Kind": "record", "UnknownFields": "", "Open": false,
                "Values": null, "Discriminator": "", "Variants": null,
                "Fields": [
                    {"Name": "id", "Type": "string", "Constraints": constraints},
                    {"Name": "amount", "Type": "int64", "Constraints": constraints}
                ]
            }
        }))
        .unwrap();
        let guard = ScopeGuard::test();
        let mut call = guard.scope.call.clone();
        let payload = event
            .admit(
                &types,
                &call,
                br#"{"id":"accepted","amount":"9007199254740993"}"#,
            )
            .map_err(|fault| fault.code)
            .unwrap();
        let other = event
            .admit(
                &types,
                &call,
                br#"{"amount":"9007199254740993","id":"other"}"#,
            )
            .map_err(|fault| fault.code)
            .unwrap();
        let key = event_dedupe_key(&payload, &event.dedupe).unwrap();
        assert!(key.starts_with("sha256:") && key.len() == 71);
        assert_eq!(key, event_dedupe_key(&other, &event.dedupe).unwrap());
        let large_a = json!({"id": (0..8192).map(|i| (b'a' + ((i * 31 + 7) % 26) as u8) as char).collect::<String>()});
        let large_b = json!({"id": (0..8192).map(|i| (b'a' + ((i * 31 + 11) % 26) as u8) as char).collect::<String>()});
        let id_keys = vec![Key { name: "id".into(), ty: "string".into() }];
        let digest_a = event_dedupe_key(&large_a, &id_keys).unwrap();
        let digest_b = event_dedupe_key(&large_b, &id_keys).unwrap();
        assert!(digest_a.starts_with("sha256:") && digest_a.len() == 71);
        assert_ne!(digest_a, digest_b);
        assert_eq!(event_dedupe_key(&large_a, &[]).unwrap(), "");
        assert_eq!(payload["amount"], "9007199254740993");
        for (input, status) in [
            (&br#"{"id":"denied","amount":"9007199254740993"}"#[..], 403),
            (&br#"{"id":"allowed","amount":"invalid"}"#[..], 400),
        ] {
            assert_eq!(
                event.admit(&types, &call, input).unwrap_err().status,
                status
            );
        }
        event.subscriptions.clear();
        call.event_depth = MAX_EVENT_DEPTH;
        let fault = event
            .admit(
                &types,
                &call,
                br#"{"id":"accepted","amount":"9007199254740993"}"#,
            )
            .unwrap_err();
        assert_eq!(fault.status, StatusCode::CONFLICT);
        assert_eq!(fault.code, "failed_precondition");
    }

    #[tokio::test]
    async fn admission_enforces_principal_tenant_and_ignores_sync_stack_depth() {
        let event = sample_event(json!([
            {"Name":"tenant", "Effect":"allow", "Expression":"principal.authenticated && context.tenant_id == \"tenant-1\""}
        ]));
        let constraints = json!({"Pattern": "", "Format": "", "UniqueItems": false});
        let types = serde_json::from_value(json!({
            "example/record/payload": {
                "Kind": "record", "UnknownFields": "", "Open": false,
                "Values": null, "Discriminator": "", "Variants": null,
                "Fields": [
                    {"Name": "id", "Type": "string", "Constraints": constraints},
                    {"Name": "amount", "Type": "int64", "Constraints": constraints}
                ]
            }
        }))
        .unwrap();
        let guard = ScopeGuard::test();
        let mut call = guard.scope.call.clone();
        call.stack = (0..32).map(|i| format!("binding/{i}")).collect();
        let mut child = call
            .child_span()
            .map_err(|fault| fault.code)
            .expect("child_span ignores sync stack");
        assert_eq!(child.parent_span_id, call.span_id);
        assert_ne!(child.span_id, call.span_id);
        assert_eq!(
            event
                .admit(&types, &child, br#"{"id":"ok","amount":"1"}"#)
                .unwrap_err()
                .status,
            403
        );
        child.principal = Some(Claims {
            user: "user-1".into(),
            tenant: "tenant-1".into(),
            ..Claims::default()
        });
        assert!(
            event
                .admit(&types, &child, br#"{"id":"ok","amount":"1"}"#)
                .is_ok()
        );
    }

    #[test]
    fn verify_events_rejects_broken_bundle_references() {
        let mut worker: Service = serde_json::from_value(json!({
            "Address": "orders/service/orders",
            "Module": "orders",
            "Name": "orders",
            "Process": "worker-1",
            "Runtime": "go",
            "Operations": {},
            "Clients": {},
            "Publishers": {"accepted": "example/event/accepted"},
            "Bindings": [],
            "Durable": {
                "orders/execution/receive": {
                    "Operation": "orders/operation/receive",
                    "Registration": {
                        "Address": "orders/execution/receive", "EngineAddress": "app/execution_engine/jobs",
                        "ExternalName": "orders.receive", "Service": "orders", "Revision": 1,
                        "DefaultTimeout": 1000, "DefaultLease": 1000, "MaxAttempts": 1,
                        "RetryInitial": 100, "RetryMax": 1000, "RetryBackoff": 2.0, "RetryJitter": 0.1,
                        "SuccessRetention": 1000, "FailureRetention": 1000, "MaxConcurrency": 1,
                        "DeduplicationRetention": 1000, "DeduplicationConflict": "return_existing"
                    },
                    "Dedupe": [],
                    "Concurrency": []
                }
            },
            "Schedules": [],
            "Stores": {}
        }))
        .unwrap();
        let event = sample_event(json!([]));
        verify_events(std::slice::from_ref(&event), std::slice::from_ref(&worker))
            .expect("valid bundle events");

        let duplicate = sample_event(json!([]));
        assert!(verify_events(&[event, duplicate], std::slice::from_ref(&worker)).is_err());

        worker
            .publishers
            .insert("dangling".into(), "example/event/missing".into());
        assert!(verify_events(&[sample_event(json!([]))], &[worker]).is_err());
    }
}

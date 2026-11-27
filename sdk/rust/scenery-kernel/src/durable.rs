use std::collections::{BTreeMap, BTreeSet};
use std::sync::Arc;
use std::time::Duration;

use anyhow::{Context, Result, ensure};
use hyper::body::Incoming;
use hyper::{HeaderMap, Request, StatusCode};
use serde::Deserialize;
use serde_json::{Value, json};
use time::OffsetDateTime;
use tokio::sync::{Mutex, Notify};
use tokio::task::JoinSet;
use tokio::time::{Instant, interval, sleep};
use tokio_postgres::{Client, GenericClient, NoTls, Row};
use tokio_util::sync::CancellationToken;

use crate::abi::Outcome;
use crate::auth::Claims;
use crate::host::{Call, Fault, Host, Reply, json_reply};
use crate::plan::{Durable, Key, Registration, Schedule, Service};
use crate::process::{DRAIN_TIMEOUT, random_hex};
use crate::{transport, values};

const SCHEMA: &str = include_str!("durable_schema.sql");
const SCHEMA_VERSION: i32 = 4;
const SCHEMA_NAME: &str = "domain_events";
const SCHEMA_CHECKSUM: &str = "durable-postgres-v4";

pub(crate) struct Engine {
    client: Mutex<Option<Arc<Client>>>,
    pub(crate) tx_pool: crate::events::TxPool,
    wake: Notify,
}

impl Engine {
    pub(crate) fn new() -> Self {
        Self {
            client: Mutex::new(None),
            tx_pool: crate::events::TxPool::new(),
            wake: Notify::new(),
        }
    }

    pub(crate) async fn connection(&self) -> Result<Arc<Client>> {
        let mut pool = self.client.lock().await;
        if let Some(client) = pool.as_ref().filter(|client| !client.is_closed()) {
            return Ok(client.clone());
        }
        let (client, _driver) = connect().await?;
        let client = Arc::new(client);
        *pool = Some(client.clone());
        Ok(client)
    }

    pub(crate) async fn prepare(&self, host: &Host) -> Result<()> {
        let registrations: Vec<_> = host
            .bundle
            .host
            .workers
            .iter()
            .flat_map(|service| service.durable.values())
            .map(|plan| &plan.registration)
            .collect();
        if registrations.is_empty() && host.bundle.host.events.is_empty() {
            return Ok(());
        }
        // Reconciliation needs a transaction of its own, which the shared
        // pipelined connection cannot provide.
        let (mut client, _driver) = connect().await?;
        reconcile(&mut client, &registrations).await
    }

    pub(crate) async fn run(host: Arc<Host>, stopping: CancellationToken) {
        let mut tasks = JoinSet::new();
        let mut retention_services = BTreeSet::new();
        for service in &host.bundle.host.workers {
            for plan in service.durable.values() {
                retention_services.insert(plan.registration.service.clone());
            }
        }
        if !retention_services.is_empty() {
            let host = host.clone();
            let stop = stopping.clone();
            tasks.spawn(async move {
                let mut tick = interval(Duration::from_secs(60));
                tick.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
                loop {
                    tokio::select! {
                        biased;
                        () = stop.cancelled() => break,
                        _ = tick.tick() => {
                            if let Err(error) = host.engine.purge(&retention_services).await {
                                eprintln!("durable retention: {error:#}");
                                host.record_runtime_fault("durable_retention", "", Host::classify_runtime_error(&error)).await;
                            }
                        }
                    }
                }
            });
        }
        for (index, service) in host.bundle.host.workers.iter().enumerate() {
            for (address, plan) in &service.durable {
                for _ in 0..plan.registration.max_concurrency.max(1) {
                    let host = host.clone();
                    let stop = stopping.clone();
                    let address = address.clone();
                    tasks.spawn(async move {
                        let service = &host.bundle.host.workers[index];
                        consume(&host, service, &service.durable[&address], &stop).await;
                    });
                }
            }
        }
        while tasks.join_next().await.is_some() {}
    }

    async fn purge(&self, services: &BTreeSet<String>) -> Result<()> {
        let services: Vec<_> = services.iter().collect();
        self.connection()
            .await?
            .execute(
                "DELETE FROM scenery.durable_jobs
             WHERE service = ANY($1::text[]) AND completed_at IS NOT NULL AND (
                (state = 'succeeded' AND success_retention_ms > 0 AND
                 completed_at <= now() - (success_retention_ms * interval '1 millisecond'))
                OR (state IN ('failed','canceled') AND failure_retention_ms > 0 AND
                    completed_at <= now() - (failure_retention_ms * interval '1 millisecond'))
             )",
                &[&services],
            )
            .await?;
        Ok(())
    }

    pub(crate) fn wake_consumers(&self) {
        self.wake.notify_waiters();
    }

    pub(crate) async fn enqueue(&self, call: &Call, plan: &Durable, input: &Value) -> Result<Job> {
        self.enqueue_with_replay(call, plan, input, false).await
    }

    async fn enqueue_with_replay(
        &self,
        call: &Call,
        plan: &Durable,
        input: &Value,
        replay: bool,
    ) -> Result<Job> {
        let client = self.connection().await?;
        let mut memo = dispatch_memo(call);
        if replay {
            memo["replay"] = json!(true);
        }
        let (job, inserted) = enqueue_job(&*client, plan, input, &memo, None).await?;
        if inserted {
            self.wake_consumers();
        }
        Ok(job)
    }

    async fn observe(&self, service: &str, mut job: Job, deadline: Instant) -> Result<(Job, bool)> {
        let client = self.connection().await?;
        while matches!(job.state.as_str(), "queued" | "running") {
            let next = tokio::time::timeout_at(deadline, async {
                sleep(Duration::from_millis(50)).await;
                client
                    .query_one(
                        "SELECT id,state,attempt,result_blob,error_blob,input_blob,memo_json
                     FROM scenery.durable_jobs WHERE service=$1 AND id=$2",
                        &[&service, &job.id],
                    )
                    .await
            })
            .await;
            match next {
                Ok(row) => job = Job::from_row(&row?),
                Err(_) => return Ok((job, true)),
            }
        }
        Ok((job, false))
    }

    pub(crate) async fn dispatch(
        &self,
        call: &Call,
        plan: &Durable,
        delivery: &str,
        input: &Value,
    ) -> Result<Outcome, Fault> {
        let reg = &plan.registration;
        let job = self
            .enqueue(call, plan, input)
            .await
            .map_err(|_| Fault::new(409, "dispatch.rejected", "could not enqueue execution"))?;
        if delivery == "wait" {
            let (job, timed_out) = self
                .observe(&reg.service, job, call.deadline)
                .await
                .map_err(Fault::internal)?;
            if timed_out {
                return Err(Fault::wait_timeout());
            }
            if job.state != "succeeded" {
                return Err(Fault::internal("durable execution failed"));
            }
            let raw = job
                .output
                .context("durable output is missing")
                .map_err(Fault::internal)?;
            let outcome = values::parse(&raw).map_err(Fault::internal)?;
            let kind = outcome["kind"]
                .as_str()
                .context("missing result kind")
                .map_err(Fault::internal)?;
            let name = outcome["name"]
                .as_str()
                .context("missing result name")
                .map_err(Fault::internal)?;
            let payload = outcome[if kind == "result" { "value" } else { "problem" }].clone();
            return Ok(Outcome {
                kind: kind.into(),
                name: name.into(),
                payload,
                stream: None,
                events: None,
                worker: None,
            });
        }
        Ok(Outcome {
            kind: "result".into(),
            name: "dispatch.enqueued".into(),
            stream: None,
            events: None,
            worker: None,
            payload: json!({
                "durable_identity": format!("{}::{}", reg.engine_address, reg.address),
                "execution_id": job.id, "accepted_revision": reg.revision.to_string(),
            }),
        })
    }

    pub(crate) async fn dev_run(&self, host: &Arc<Host>, request: Request<Incoming>) -> Reply {
        self.run_development(host, request)
            .await
            .unwrap_or_else(|fault| fault.reply(host, None))
    }

    async fn run_development(
        &self,
        host: &Arc<Host>,
        request: Request<Incoming>,
    ) -> Result<Reply, Fault> {
        let headers = request.headers().clone();
        let raw = transport::read(request.into_body(), scenery_wire::MAX_METADATA)
            .await
            .map_err(|_| Fault::invalid())?;
        let command: DevRequest = serde_json::from_slice(&raw).map_err(|_| Fault::invalid())?;
        if !(1..=3_600_000_000_000).contains(&command.timeout_ns) {
            return Err(Fault::invalid());
        }
        let mut targets = Vec::new();
        for service in &host.bundle.host.workers {
            for plan in service.durable.values() {
                let reg = &plan.registration;
                if command.target == reg.address
                    || command.target == reg.external_name
                    || command.target == format!("{}.{}", service.module, tail(&reg.address))
                    || command.target == format!("{}.{}", service.module, tail(&plan.operation))
                {
                    targets.push((service, Some(plan), None));
                }
            }
            for schedule in &service.schedules {
                if command.target == schedule.address
                    || command.target == tail(&schedule.address)
                    || command.target == format!("{}.{}", service.module, tail(&schedule.address))
                {
                    targets.push((
                        service,
                        service.durable.get(&schedule.execution),
                        Some(schedule),
                    ));
                }
            }
        }
        let [(service, plan, schedule)] = targets.as_slice() else {
            return Err(Fault::invalid());
        };
        let generation = host
            .generation()
            .ok_or_else(|| Fault::unavailable(scenery_wire::Delivery::NotSent))?;
        let address = schedule
            .map(|schedule| schedule.address.as_str())
            .or_else(|| plan.map(|plan| plan.registration.address.as_str()))
            .ok_or_else(Fault::invalid)?;
        let mut call = Call::new(
            generation,
            &HeaderMap::new(),
            "DURABLE",
            address,
            Duration::from_nanos(command.timeout_ns),
        )
        .map_err(Fault::internal)?;
        if schedule.is_none() && headers.contains_key("authorization") {
            host.authenticate(&mut call, &headers, true).await?;
        }
        let operation = schedule
            .map(|schedule| &schedule.operation)
            .or_else(|| plan.map(|plan| &plan.operation))
            .ok_or_else(Fault::invalid)?;
        let input = command
            .input
            .or_else(|| schedule.map(|schedule| schedule.input.clone()))
            .unwrap_or_else(|| json!({}));
        let input = values::normalize(
            &host.bundle.types,
            &service.operations[operation].input,
            input,
        )
        .map_err(|_| Fault::invalid())?;
        if let Some(schedule) = schedule {
            set_workload(&mut call, schedule);
            Host::authorize(&call, &schedule.policy, &input)?;
        }
        let Some(plan) = plan else {
            let execution_id = format!("run_{}", random_hex::<16>().map_err(Fault::internal)?);
            call.method = "SCHEDULE".into();
            call.execution_id = Some(execution_id.clone());
            call.cron_idempotency_key = Some(execution_id);
            let mut reply = json!({"job_id": call.execution_id, "target": address, "module": service.module,
                "execution_kind": "schedule", "attempts": 1, "input": input, "trace_id": call.trace_id, "timed_out": false});
            if !command.wait {
                reply["state"] = json!("queued");
                reply["attempts"] = json!(0);
                let host = host.clone();
                let service_address = service.address.clone();
                let operation = operation.clone();
                tokio::spawn(async move {
                    let Some(service) = host
                        .bundle
                        .host
                        .workers
                        .iter()
                        .find(|service| service.address == service_address)
                    else {
                        return;
                    };
                    tokio::select! {
                        () = host.stop.cancelled() => {}
                        output = host.invoke(&call, service, &operation, None, "direct", &input) => {
                            match output {
                                Ok(output) if !output.is_streamed() => {}
                                Err(fault) => { host.telemetry.fault_failure(&fault, "scheduled execution failed"); }
                                _ => { host.telemetry.failure("scheduled execution failed"); }
                            }
                        }
                    }
                });
                return Ok(json_reply(StatusCode::OK, &reply));
            }
            let output = host
                .invoke(&call, service, operation, None, "direct", &input)
                .await;
            match output {
                Ok(output) if !output.is_streamed() => {
                    reply["state"] = json!("succeeded");
                    reply["output"] = output.json();
                }
                other => {
                    reply["state"] = json!("failed");
                    let report = match other {
                        Err(fault) => host
                            .telemetry
                            .fault_failure(&fault, "scheduled execution failed"),
                        _ => host.telemetry.failure("scheduled execution failed"),
                    };
                    reply["error"] = json!({"code": "SCN9000", "message": "execution failed", "report_token": report});
                }
            }
            return Ok(json_reply(StatusCode::OK, &reply));
        };
        if schedule.is_some() {
            call.method = "SCHEDULE".into();
            call.cron_idempotency_key = Some(format!(
                "run_{}",
                random_hex::<16>().map_err(Fault::internal)?
            ));
        }
        let mut job = self
            .enqueue_with_replay(&call, plan, &input, command.replay)
            .await
            .map_err(Fault::internal)?;
        let mut timed_out = false;
        if command.wait {
            (job, timed_out) = self
                .observe(&plan.registration.service, job, call.deadline)
                .await
                .map_err(Fault::internal)?;
        }
        let mut reply = json!({
            "job_id": job.id, "target": address, "module": service.module,
            "execution_kind": if schedule.is_some() { "schedule" } else { "durable" }, "state": job.state, "attempts": job.attempt,
            "input": values::parse(&job.input).unwrap_or(input), "trace_id": job.memo["trace_id"],
            "timed_out": timed_out,
        });
        if let Some(output) = job.output {
            reply["output"] = values::parse(&output).map_err(Fault::internal)?;
        }
        if timed_out {
            reply["error"] =
                json!({"code": "SCN8003", "message": "timed out waiting for execution"});
        } else if job.state == "failed" {
            let failure = job
                .error
                .as_deref()
                .and_then(|bytes| values::parse(bytes).ok());
            reply["error"] = failure
                .unwrap_or_else(|| json!({"code": "SCN9000", "message": "execution failed"}));
        } else if job.state == "canceled" {
            reply["error"] =
                json!({"code": "SCN8003", "message": "durable execution was canceled"});
        }
        Ok(json_reply(StatusCode::OK, &reply))
    }
}

pub(crate) fn set_workload(call: &mut Call, schedule: &Schedule) {
    call.schedule_target = Some(schedule.address.clone());
    call.workload = Some(json!({
        "workload": "scheduler", "issuer": "std.identity_issuer.runtime",
        "principal_type": "std.type.workload_principal", "workload_identity": "std.workload_identity.scheduler",
        "authorization": schedule.authorization, "pipeline": schedule.pipeline,
    }));
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct DevRequest {
    target: String,
    input: Option<Value>,
    wait: bool,
    #[serde(default)]
    replay: bool,
    timeout_ns: u64,
}

pub(crate) struct Job {
    id: String,
    state: String,
    attempt: i32,
    output: Option<Vec<u8>>,
    error: Option<Vec<u8>>,
    input: Vec<u8>,
    memo: Value,
}

impl Job {
    fn from_row(row: &Row) -> Self {
        Self {
            id: row.get(0),
            state: row.get(1),
            attempt: row.get(2),
            output: row.get(3),
            error: row.get(4),
            input: row.get(5),
            memo: row.get(6),
        }
    }
}

pub(crate) async fn connect() -> Result<(Client, tokio::task::JoinHandle<()>)> {
    let address = crate::plan::resolve_app_database_url()?;
    let mut url = url::Url::parse(&address).context("invalid database URL")?;
    let parameters: Vec<_> = url
        .query_pairs()
        .filter_map(|(key, value)| {
            if key == "search_path" {
                return None;
            }
            let value = if key == "sslmode" && matches!(value.as_ref(), "verify-ca" | "verify-full")
            {
                "require".into()
            } else {
                value.into_owned()
            };
            Some((key.into_owned(), value))
        })
        .collect();
    url.set_query(None);
    if !parameters.is_empty() {
        url.query_pairs_mut().extend_pairs(parameters);
    }
    let mut config: tokio_postgres::Config = url
        .as_str()
        .parse()
        .context("invalid database configuration")?;
    if config.get_connect_timeout().is_none() {
        config.connect_timeout(Duration::from_secs(5));
    }
    if config.get_ssl_mode() == tokio_postgres::config::SslMode::Disable {
        let (client, connection) = config
            .connect(NoTls)
            .await
            .context("connect application database")?;
        return Ok((
            client,
            tokio::spawn(async move {
                if let Err(error) = connection.await {
                    eprintln!("application database connection closed: {error}");
                }
            }),
        ));
    }
    let tls = tokio_postgres_rustls::MakeRustlsConnect::with_native_certs()
        .map(|(connector, _)| connector)
        .unwrap_or_else(|_| tokio_postgres_rustls::MakeRustlsConnect::with_webpki_roots());
    let (client, connection) = config
        .connect(tls)
        .await
        .context("connect application database")?;
    let driver = tokio::spawn(async move {
        if let Err(error) = connection.await {
            eprintln!("application database connection closed: {error}");
        }
    });
    Ok((client, driver))
}

fn ms(nanoseconds: i64) -> i64 {
    nanoseconds / 1_000_000
}

fn tail(address: &str) -> &str {
    address.rsplit('/').next().unwrap_or(address)
}

/// Builds the deduplication or concurrency key of `input` as Go's
/// `contract.EncodeContractCompositeKey` does. Each component is written in
/// exact canonical JSON, so a float the wire spells `1e+06` is keyed as
/// `1000000` and a job enqueued by either host deduplicates against the
/// other's.
pub(crate) fn dispatch_key(input: &Value, fields: &[Key]) -> Result<String> {
    if fields.is_empty() {
        return Ok(String::new());
    }
    let mut encoded = String::from("[");
    for (index, field) in fields.iter().enumerate() {
        let component = match input.get(&field.name) {
            None if field.ty.starts_with("optional(") => {
                json!({"state": "absent", "type": field.ty})
            }
            Some(Value::Null) if field.ty.starts_with("nullable(") => {
                json!({"state": "null", "type": field.ty})
            }
            Some(value) => json!({"state": "value", "type": field.ty, "value": value}),
            _ => anyhow::bail!("required execution key is absent"),
        };
        if index > 0 {
            encoded.push(',');
        }
        encoded.push_str(&values::encode_exact(&component)?);
    }
    encoded.push(']');
    Ok(format!(
        "scenery-key-v1.{}",
        values::base64url(encoded.as_bytes())
    ))
}

pub(crate) fn dispatch_memo(call: &Call) -> Value {
    json!({
        "trace_id": call.trace_id,
        "traceparent": format!("00-{}-{}-01", call.trace_id, call.span_id),
        "caller_binding": call.caller,
        "principal": call.principal_id(),
        "workload": call.workload,
        "schedule_target": call.schedule_target,
        "tenant_id": call.principal.as_ref().map(|claims| &claims.tenant),
        "cron_idempotency_key": call.cron_idempotency_key,
        "event_depth": call.event_depth,
    })
}

#[derive(Debug)]
pub(crate) struct DuplicateExecution;

impl std::fmt::Display for DuplicateExecution {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("duplicate durable execution")
    }
}

impl std::error::Error for DuplicateExecution {}

pub(crate) async fn enqueue_job(
    client: &impl GenericClient,
    plan: &Durable,
    input: &Value,
    memo: &Value,
    fallback_key: Option<String>,
) -> Result<(Job, bool)> {
    let reg = &plan.registration;
    let dedupe = if memo["replay"].as_bool() == Some(true) {
        String::new()
    } else if plan.dedupe.is_empty() {
        fallback_key.unwrap_or_default()
    } else {
        dispatch_key(input, &plan.dedupe)?
    };
    let concurrency = dispatch_key(input, &plan.concurrency)?;
    let id = random_hex::<16>()?;
    let input = values::encode(input).into_bytes();
    let caller = memo["principal"].as_str();
    if !dedupe.is_empty() {
        client.execute(
            "UPDATE scenery.durable_jobs SET dedupe_key = NULL, dedupe_expires_at = NULL, updated_at = now()
             WHERE service = $1 AND task_name = $2 AND dedupe_key = $3 AND dedupe_expires_at <= now()",
            &[&reg.service, &reg.external_name, &dedupe],
        ).await?;
    }
    let row = client.query_one(
        "WITH inserted AS (
            INSERT INTO scenery.durable_jobs (
                service, id, task_name, task_version, state, dedupe_key, dedupe_expires_at,
                concurrency_key, input_blob, memo_json, max_attempts, success_retention_ms,
                failure_retention_ms, created_by
            ) VALUES ($1,$2,$3,$4,'queued',NULLIF($5,''),
                CASE WHEN $5 = '' THEN NULL ELSE now() + ($6::bigint * interval '1 millisecond') END,
                NULLIF($7,''),$8,$9,$10,$11,$12,$13)
            ON CONFLICT (service, task_name, dedupe_key) WHERE dedupe_key IS NOT NULL
            DO UPDATE SET dedupe_key = excluded.dedupe_key
            RETURNING id, state, attempt, result_blob, error_blob, input_blob, memo_json
         ), recorded AS (
            INSERT INTO scenery.durable_job_events (service,job_id,event_type,payload_blob)
            SELECT $1,id,'job.created','{}'::bytea FROM inserted WHERE id = $2
         ) SELECT * FROM inserted",
        &[&reg.service, &id, &reg.external_name, &(reg.revision as i32), &dedupe,
            &ms(reg.deduplication_retention), &concurrency, &input, &memo, &reg.max_attempts,
            &ms(reg.success_retention), &ms(reg.failure_retention), &caller],
    ).await?;
    let job = Job::from_row(&row);
    if job.id != id && reg.deduplication_conflict == "reject" {
        return Err(DuplicateExecution.into());
    }
    let inserted = job.id == id;
    Ok((job, inserted))
}

/// Installs the durable and schedule tables. Once the current schema version is
/// recorded in `scenery.durable_schema_migrations`, startup skips DDL so live
/// consumers and schedulers never contend with `ALTER TABLE` or index locks.
pub(crate) async fn install_schema(client: &mut Client) -> Result<()> {
    let tx = client
        .transaction()
        .await
        .context("initialize durable schema")?;
    tx.execute(
        "SELECT pg_advisory_xact_lock(hashtextextended('scenery.durable.store', 0))",
        &[],
    )
    .await
    .context("initialize durable schema")?;
    let ledger_exists: bool = tx
        .query_one(
            "SELECT to_regclass('scenery.durable_schema_migrations') IS NOT NULL",
            &[],
        )
        .await
        .context("initialize durable schema")?
        .get(0);
    if ledger_exists {
        let installed: bool = tx
            .query_one(
                "SELECT EXISTS (
                    SELECT 1 FROM scenery.durable_schema_migrations
                    WHERE version = $1 AND checksum = $2
                )",
                &[&SCHEMA_VERSION, &SCHEMA_CHECKSUM],
            )
            .await
            .context("initialize durable schema")?
            .get(0);
        if installed {
            tx.commit().await.context("initialize durable schema")?;
            return Ok(());
        }
    }
    tx.batch_execute(SCHEMA)
        .await
        .context("initialize durable schema")?;
    tx.execute(
        "INSERT INTO scenery.durable_schema_migrations(version,name,checksum)
         VALUES($1,$2,$3) ON CONFLICT(version) DO NOTHING",
        &[&SCHEMA_VERSION, &SCHEMA_NAME, &SCHEMA_CHECKSUM],
    )
    .await
    .context("initialize durable schema")?;
    tx.commit().await.context("initialize durable schema")?;
    Ok(())
}

/// Brings the stored task declarations in line with this deployment, as the
/// Go store's `ReconcileTasks` does: it refuses to strand active jobs, then
/// disables what the services no longer declare and registers the rest.
async fn reconcile(client: &mut Client, registrations: &[&Registration]) -> Result<()> {
    install_schema(client).await?;
    let mut declared = BTreeMap::new();
    for reg in registrations {
        let key = (reg.service.as_str(), reg.external_name.as_str());
        ensure!(
            declared
                .insert(key, reg.revision)
                .is_none_or(|previous| previous == reg.revision),
            "durable store: task {:?} has conflicting revisions",
            reg.external_name
        );
    }
    let services: Vec<&str> = declared
        .keys()
        .map(|(service, _)| *service)
        .collect::<BTreeSet<_>>()
        .into_iter()
        .collect();
    let tx = client.transaction().await?;
    tx.execute(
        "SELECT pg_advisory_xact_lock(hashtextextended('scenery.durable.store', 0))",
        &[],
    )
    .await?;
    let active: Vec<ActiveRevision> = tx
        .query(
            "SELECT service, task_name, task_version, count(*) FROM scenery.durable_jobs
             WHERE service = ANY($1::text[]) AND state IN ('queued','running')
             GROUP BY service, task_name, task_version",
            &[&services],
        )
        .await
        .context("inspect active task revisions")?
        .iter()
        .map(|row| (row.get(0), row.get(1), row.get(2), row.get(3)))
        .collect();
    migration_required(&declared, &active)?;
    tx.execute(
        "UPDATE scenery.durable_tasks SET enabled = false, updated_at = now()
         WHERE service = ANY($1::text[])",
        &[&services],
    )
    .await
    .context("disable previous task declarations")?;
    for reg in registrations {
        register(&tx, reg).await?;
    }
    tx.commit().await?;
    Ok(())
}

/// One `(service, task, revision, jobs)` group of queued or running jobs.
type ActiveRevision = (String, String, i32, i64);

/// Refuses to start while queued or running jobs belong to a revision this
/// deployment does not provide. A job is only ever leased at the revision
/// that enqueued it, so registering past it would strand it silently.
fn migration_required(
    declared: &BTreeMap<(&str, &str), u32>,
    active: &[ActiveRevision],
) -> Result<()> {
    for (service, name, version, jobs) in active {
        let wanted = declared
            .get(&(service.as_str(), name.as_str()))
            .copied()
            .unwrap_or(0);
        ensure!(
            i64::from(wanted) == i64::from(*version),
            "durable store: migration_required: task {name:?} has {jobs} active jobs at revision {version} but runtime provides revision {wanted}"
        );
    }
    Ok(())
}

async fn register(client: &impl GenericClient, reg: &Registration) -> Result<()> {
    client.execute(
        "INSERT INTO scenery.durable_tasks (
            service,name,version,handler_ref,default_timeout_ms,default_lease_ms,max_attempts,
            max_concurrency,deduplication_retention_ms,deduplication_conflict,retry_initial_ms,
            retry_max_ms,retry_backoff,retry_jitter,success_retention_ms,failure_retention_ms
         ) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
         ON CONFLICT (service,name) DO UPDATE SET
            version=excluded.version, handler_ref=excluded.handler_ref,
            default_timeout_ms=excluded.default_timeout_ms, default_lease_ms=excluded.default_lease_ms,
            max_attempts=excluded.max_attempts, max_concurrency=excluded.max_concurrency,
            deduplication_retention_ms=excluded.deduplication_retention_ms,
            deduplication_conflict=excluded.deduplication_conflict, retry_initial_ms=excluded.retry_initial_ms,
            retry_max_ms=excluded.retry_max_ms, retry_backoff=excluded.retry_backoff, retry_jitter=excluded.retry_jitter,
            success_retention_ms=excluded.success_retention_ms, failure_retention_ms=excluded.failure_retention_ms,
            enabled=true,updated_at=now()",
        &[&reg.service, &reg.external_name, &(reg.revision as i32), &reg.address,
            &effective_timeout_ms(reg),
            &effective_lease_ms(reg),
            &reg.max_attempts, &reg.max_concurrency, &ms(reg.deduplication_retention),
            &reg.deduplication_conflict, &(ms(reg.retry_initial) as i32), &(ms(reg.retry_max) as i32),
            &reg.retry_backoff, &reg.retry_jitter, &ms(reg.success_retention), &ms(reg.failure_retention)],
    ).await?;
    Ok(())
}

async fn consume(host: &Host, service: &Service, plan: &Durable, stop: &CancellationToken) {
    let mut idle = interval(Duration::from_millis(100));
    idle.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    loop {
        tokio::select! {
            biased;
            () = stop.cancelled() => break,
            _ = idle.tick() => {}
            () = host.engine.wake.notified() => {}
        }
        if let Err(error) = execute_next(host, service, plan, stop).await {
            eprintln!(
                "durable engine {}: {error:#}",
                plan.registration.external_name
            );
            host.record_runtime_fault(
                "durable_engine",
                &plan.registration.external_name,
                Host::classify_runtime_error(&error),
            )
            .await;
            tokio::select! {
                biased;
                () = stop.cancelled() => break,
                () = sleep(Duration::from_secs(1)) => {}
            }
        }
    }
}

pub(super) fn effective_timeout_ms(reg: &Registration) -> i32 {
    if reg.default_timeout == 0 {
        60_000
    } else {
        reg.default_timeout.div_ceil(1_000_000).min(i32::MAX as u64) as i32
    }
}

pub(super) fn effective_lease_ms(reg: &Registration) -> i32 {
    let timeout_ms = effective_timeout_ms(reg);
    let lease_ms = if reg.default_lease == 0 {
        60_000
    } else {
        reg.default_lease.div_ceil(1_000_000).min(i32::MAX as u64) as i32
    };
    lease_ms.min(timeout_ms)
}

pub(super) fn effective_timeout(reg: &Registration) -> Duration {
    Duration::from_millis(effective_timeout_ms(reg) as u64)
}

pub(super) fn effective_lease(reg: &Registration) -> Duration {
    Duration::from_millis(effective_lease_ms(reg) as u64)
}

async fn execute_next(
    host: &Host,
    service: &Service,
    plan: &Durable,
    stop: &CancellationToken,
) -> Result<bool> {
    if stop.is_cancelled() {
        return Ok(false);
    }
    let Some(generation) = host.generation() else {
        return Ok(false);
    };
    let client = host.engine.connection().await?;
    let reg = &plan.registration;
    let lease_id = random_hex::<16>()?;
    let owner = std::process::id().to_string();
    client.execute(
        "WITH expired AS (
            UPDATE scenery.durable_jobs SET state=CASE WHEN attempt < max_attempts THEN 'queued' ELSE 'failed' END,
                run_after=now(), error_codec='text', error_blob='durable lease or timeout expired'::bytea,
                lease_id=NULL, lease_owner=NULL, lease_token_hash=NULL, lease_until=NULL, timeout_at=NULL,
                completed_at=CASE WHEN attempt < max_attempts THEN NULL ELSE now() END, updated_at=now()
            WHERE service=$1 AND task_name=$2 AND state='running' AND (lease_until <= now() OR timeout_at <= now())
            RETURNING service,id,attempt
         ) INSERT INTO scenery.durable_job_events (service,job_id,attempt,event_type)
           SELECT service,id,attempt,'job.lease_expired' FROM expired",
        &[&reg.service, &reg.external_name],
    ).await?;
    if stop.is_cancelled() {
        return Ok(false);
    }
    let row = client.query_opt(
        "WITH candidate AS (
            SELECT j.id, t.default_lease_ms, t.default_timeout_ms FROM scenery.durable_jobs j
            JOIN scenery.durable_tasks t ON t.service=j.service AND t.name=j.task_name AND t.version=j.task_version
            WHERE j.service=$1 AND j.task_name=$2 AND j.task_version=$5 AND t.enabled AND j.state='queued'
            AND j.run_after <= now() AND j.attempt < j.max_attempts
            AND (t.max_concurrency <= 0 OR (
                SELECT count(*) FROM scenery.durable_jobs r
                WHERE r.service=j.service AND r.task_name=j.task_name AND r.state='running'
                  AND COALESCE(r.concurrency_key,'')=COALESCE(j.concurrency_key,'')
            ) < t.max_concurrency)
            ORDER BY j.priority DESC,j.created_at,j.id LIMIT 1 FOR UPDATE OF t,j SKIP LOCKED
         ), leased AS (
            UPDATE scenery.durable_jobs j SET state='running',attempt=j.attempt+1,lease_id=$3,lease_owner=$4,
                lease_until=now() + (c.default_lease_ms * interval '1 millisecond'),
                timeout_at=now() + (c.default_timeout_ms * interval '1 millisecond'),updated_at=now()
            FROM candidate c WHERE j.service=$1 AND j.id=c.id
            RETURNING j.id,j.state,j.attempt,j.result_blob,j.error_blob,j.input_blob,j.memo_json
         ), event AS (
            INSERT INTO scenery.durable_job_events(service,job_id,attempt,event_type,payload_blob)
            SELECT $1,id,attempt,'job.leased','{}'::bytea FROM leased
         ) SELECT * FROM leased",
        &[&reg.service, &reg.external_name, &lease_id, &owner, &(reg.revision as i32)],
    ).await?;
    let Some(row) = row else { return Ok(false) };
    let job = Job::from_row(&row);
    let mut headers = HeaderMap::new();
    if let Some(parent) = job.memo["traceparent"].as_str() {
        crate::host::header(&mut headers, "traceparent", parent);
    }
    let mut call = Call::new(
        generation,
        &headers,
        "DURABLE",
        &reg.external_name,
        effective_timeout(reg),
    )?;
    call.invocation_id.clone_from(&job.id);
    call.execution_id = Some(job.id.clone());
    call.event_depth = job.memo["event_depth"]
        .as_u64()
        .map_or(0, |depth| u32::try_from(depth).unwrap_or(u32::MAX));
    call.caller = job.memo["caller_binding"]
        .as_str()
        .unwrap_or_default()
        .into();
    call.cron_idempotency_key = job.memo["cron_idempotency_key"]
        .as_str()
        .filter(|v| !v.is_empty())
        .map(str::to_owned);
    call.schedule_target = job.memo["schedule_target"]
        .as_str()
        .filter(|v| !v.is_empty())
        .map(str::to_owned);
    if job.memo["workload"].is_object()
        && job.memo["principal"] == "workload:std.workload_identity.scheduler"
    {
        call.workload = Some(job.memo["workload"].clone());
    }
    if let Some(user) = job.memo["principal"].as_str().filter(|v| !v.is_empty()) {
        call.principal = Some(Claims {
            user: user.into(),
            tenant: job.memo["tenant_id"].as_str().unwrap_or_default().into(),
            ..Claims::default()
        });
    }
    let execute = async {
        let input = values::parse(&job.input).map_err(Fault::internal)?;
        let operation = &service.operations[&plan.operation];
        let input = values::normalize(&host.bundle.types, &operation.input, input)
            .map_err(Fault::internal)?;
        let outcome = host
            .invoke(&call, service, &plan.operation, None, "durable", &input)
            .await?;
        if outcome.is_streamed() {
            return Err(Fault::internal("execution returned a stream"));
        }
        Ok(outcome)
    };
    tokio::pin!(execute);
    let lease = effective_lease(reg);
    let lease_ms = i64::from(effective_lease_ms(reg));
    let mut heartbeat = interval(Duration::from_nanos(
        u64::try_from(lease.as_nanos() / 3)
            .unwrap_or(u64::MAX)
            .max(1_000_000),
    ));
    heartbeat.tick().await;
    // A stopping host stops claiming work, but a running execution keeps its
    // lease for the drain window: most handlers finish within it.
    let drained = async {
        stop.cancelled().await;
        sleep(DRAIN_TIMEOUT).await;
    };
    tokio::pin!(drained);
    let outcome = loop {
        tokio::select! {
            result = &mut execute => break result,
            () = &mut drained => {
                call.cancel.cancel();
                release(&client, reg, &job.id, &lease_id).await?;
                return Ok(true);
            }
            _ = heartbeat.tick() => {
                let updated = client.execute(
                    "UPDATE scenery.durable_jobs SET lease_until=LEAST(timeout_at,now() + ($4::bigint * interval '1 millisecond'))
                     WHERE service=$1 AND id=$2 AND lease_id=$3 AND state='running'
                       AND lease_until > now() AND timeout_at > now()",
                    &[&reg.service, &job.id, &lease_id, &lease_ms],
                ).await?;
                if updated == 0 {
                    call.cancel.cancel();
                    return Ok(true);
                }
            }
        }
    };
    let (output, error) = match outcome {
        Ok(value) => (Some(values::encode(&value.json()).into_bytes()), None),
        Err(fault) => {
            let report = host.telemetry.fault_failure(&fault, fault.code);
            (None, Some(values::encode(&json!({"code": "SCN9000", "message": "execution failed", "report_token": report})).into_bytes()))
        }
    };
    let delay = retry_delay(reg, job.attempt);
    client.execute(
        "WITH completed AS (
            UPDATE scenery.durable_jobs SET
              state=CASE WHEN $4::bytea IS NOT NULL THEN 'succeeded'
                         WHEN attempt < max_attempts THEN 'queued' ELSE 'failed' END,
              result_codec=CASE WHEN $4 IS NOT NULL THEN 'json' ELSE NULL END, result_blob=$4,
              error_codec=CASE WHEN $5::bytea IS NOT NULL THEN 'json' ELSE NULL END, error_blob=$5,
              lease_id=NULL,lease_owner=NULL,lease_token_hash=NULL,lease_until=NULL,timeout_at=NULL,
              run_after=now() + ($6::bigint * interval '1 millisecond'),
              completed_at=CASE WHEN $4 IS NOT NULL OR attempt >= max_attempts THEN now() ELSE NULL END,
              updated_at=now()
            WHERE service=$1 AND id=$2 AND lease_id=$3 AND state='running' AND lease_until > now()
            RETURNING service,id,attempt,state
         ) INSERT INTO scenery.durable_job_events(service,job_id,attempt,event_type,payload_blob)
           SELECT service,id,attempt,CASE WHEN state='queued' THEN 'job.retry_scheduled' ELSE 'job.' || state END,COALESCE($4,$5)
           FROM completed",
        &[&reg.service, &job.id, &lease_id, &output, &error, &delay],
    ).await?;
    Ok(true)
}

/// Hands a leased job back when the host stops before its handler finished.
/// The attempt is returned with the lease: a deployment is not a failure,
/// and the job is claimable again at once instead of after its lease.
async fn release(client: &Client, reg: &Registration, job: &str, lease: &str) -> Result<()> {
    client.execute(
        "WITH released AS (
            UPDATE scenery.durable_jobs SET state='queued', attempt=GREATEST(attempt-1,0), run_after=now(),
                lease_id=NULL, lease_owner=NULL, lease_token_hash=NULL, lease_until=NULL, timeout_at=NULL,
                updated_at=now()
            WHERE service=$1 AND id=$2 AND lease_id=$3 AND state='running'
            RETURNING service,id,attempt
         ) INSERT INTO scenery.durable_job_events (service,job_id,attempt,event_type)
           SELECT service,id,attempt,'job.lease_released' FROM released",
        &[&reg.service, &job, &lease],
    ).await?;
    Ok(())
}

fn retry_delay(reg: &Registration, attempt: i32) -> i64 {
    let delay = ms(reg.retry_initial) as f64
        * f64::from(reg.retry_backoff.max(1.0)).powi(attempt.saturating_sub(1));
    let bound = ms(reg.retry_max).max(ms(reg.retry_initial)) as f64;
    let noise = OffsetDateTime::now_utc().nanosecond() as f64 / 1e9;
    (delay.min(bound) * (1.0 + (2.0 * noise - 1.0) * f64::from(reg.retry_jitter)))
        .min(bound)
        .max(0.0) as i64
}

#[cfg(test)]
#[path = "durable_test.rs"]
mod tests;

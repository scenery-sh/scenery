use std::collections::VecDeque;
use std::sync::Arc;
use std::time::Duration;

use hyper::HeaderMap;
use time::OffsetDateTime;
use tokio::task::JoinSet;
use tokio::time::{Instant, sleep_until};
use tokio_util::sync::CancellationToken;

use crate::durable::set_workload;
use crate::host::{Call, Fault, Host};
use crate::plan::{Schedule, Service};
use crate::process::random_hex;
use crate::values;

pub(crate) async fn run(host: Arc<Host>, stop: CancellationToken) {
    let mut schedules = JoinSet::new();
    for (owner, service) in host.bundle.host.workers.iter().enumerate() {
        for (index, schedule) in service.schedules.iter().enumerate() {
            if schedule.every == 0 {
                continue;
            }
            let stop = stop.child_token();
            let host = host.clone();
            schedules.spawn(async move { tick(host, stop, owner, index).await });
        }
    }
    while schedules.join_next().await.is_some() {}
}

async fn tick(host: Arc<Host>, stop: CancellationToken, owner: usize, index: usize) {
    let schedule = &host.bundle.host.workers[owner].schedules[index];
    let cadence = Duration::from_nanos(schedule.every);
    let clock = OffsetDateTime::now_utc().unix_timestamp_nanos();
    let period = i128::from(schedule.every);
    let initial = (period - clock.rem_euclid(period)) as u64;
    let mut next = Instant::now() + Duration::from_nanos(initial);
    let mut active = JoinSet::new();
    let mut queue = VecDeque::new();
    loop {
        let run = tokio::select! {
            () = stop.cancelled() => break,
            _ = active.join_next(), if !active.is_empty() => {
                if active.is_empty() {
                    while queue.front().is_some_and(|scheduled: &Instant| {
                        schedule.catchup_maximum_age > 0 && scheduled.elapsed() > Duration::from_nanos(schedule.catchup_maximum_age)
                    }) {
                        queue.pop_front();
                    }
                    queue.pop_front()
                } else { None }
            }
            () = sleep_until(next) => {
                let current = next;
                next += cadence;
                match (schedule.overlap.as_str(), active.is_empty()) {
                    ("allow", _) | (_, true) => Some(current),
                    ("replace", false) => { active.abort_all(); Some(current) }
                    ("queue", false) => { queue.push_back(current); None }
                    _ => None,
                }
            }
        };
        if run.is_some() {
            let host = host.clone();
            active.spawn(async move {
                let service = &host.bundle.host.workers[owner];
                let schedule = &service.schedules[index];
                if let Err(error) = invoke(&host, service, schedule).await {
                    host.telemetry.failure(error.code);
                }
            });
        }
    }
    active.shutdown().await;
}

async fn invoke(host: &Host, service: &Service, schedule: &Schedule) -> Result<(), Fault> {
    let generation = host
        .generation()
        .ok_or_else(|| Fault::unavailable(scenery_wire::Delivery::NotSent))?;
    let timeout = if schedule.timeout == 0 {
        Duration::from_secs(30)
    } else {
        Duration::from_nanos(schedule.timeout)
    };
    let mut call = Call::new(
        generation,
        &HeaderMap::new(),
        "SCHEDULE",
        &schedule.address,
        timeout,
    )
    .map_err(Fault::internal)?;
    set_workload(&mut call, schedule);
    let execution_id = random_hex::<16>().map_err(Fault::internal)?;
    call.execution_id = Some(execution_id.clone());
    call.cron_idempotency_key = Some(execution_id);
    let operation = &service.operations[&schedule.operation];
    let input = values::normalize(&host.bundle.types, &operation.input, schedule.input.clone())
        .map_err(Fault::internal)?;
    Host::authorize(&call, &schedule.policy, &input)?;
    if let Some(plan) = service.durable.get(&schedule.execution) {
        host.engine
            .enqueue(&call, plan, &input)
            .await
            .map_err(Fault::internal)?;
    } else {
        let outcome = host
            .invoke(&call, service, &schedule.operation, None, "direct", &input)
            .await?;
        if outcome.stream.is_some() {
            return Err(Fault::internal("scheduled operation returned a stream"));
        }
    }
    Ok(())
}

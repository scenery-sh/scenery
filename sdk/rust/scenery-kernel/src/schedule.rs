use std::collections::VecDeque;
use std::sync::Arc;
use std::time::Duration;

use hyper::HeaderMap;
use tokio::task::JoinSet;
use tokio::time::{Instant, interval, sleep_until};
use tokio_util::sync::CancellationToken;

use crate::durable::set_workload;
use crate::host::{Call, Fault, Host};
use crate::plan::{Schedule, Service};
use crate::process::random_hex;
use crate::schedule_slots::{self, Key, LEASE, Run, Slots};
use crate::values;

/// How often a replica deletes old slot claims.
const PRUNE_INTERVAL: Duration = Duration::from_secs(600);

/// Runs every declared schedule until `stop`. With a database, replicas
/// claim each slot in PostgreSQL and run it once between them; without
/// one, this host is the only scheduler and keeps its state in memory.
pub(crate) async fn run(host: Arc<Host>, stop: CancellationToken) {
    let mut schedules = JoinSet::new();
    let declared: Vec<_> = host
        .bundle
        .host
        .workers
        .iter()
        .enumerate()
        .flat_map(|(owner, service)| {
            service
                .schedules
                .iter()
                .enumerate()
                .filter(|(_, schedule)| schedule.every > 0)
                .map(move |(index, _)| (owner, index))
        })
        .collect();
    if declared.is_empty() {
        return;
    }
    let slots = coordinated().map(Arc::new);
    if let Some(slots) = slots.clone() {
        let services = declared
            .iter()
            .map(|&(owner, index)| key(&host, owner, index).service)
            .collect::<std::collections::BTreeSet<_>>()
            .into_iter()
            .collect::<Vec<_>>();
        let (host, stop) = (host.clone(), stop.clone());
        schedules.spawn(async move {
            let mut tick = interval(PRUNE_INTERVAL);
            tick.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
            loop {
                tokio::select! {
                    () = stop.cancelled() => break,
                    _ = tick.tick() => {
                        let pruned = async {
                            slots.ensure_schema().await?;
                            let client = host.engine.connection().await?;
                            schedule_slots::prune(&client, &services).await
                        };
                        tokio::select! {
                            () = stop.cancelled() => break,
                            result = tokio::time::timeout(Duration::from_secs(10), pruned) => match result {
                                Ok(Ok(_)) => {}
                                Ok(Err(error)) => {
                                    eprintln!("schedule retention: {error:#}");
                                    host.record_runtime_fault("schedule_retention", "", Host::classify_runtime_error(&error)).await;
                                }
                                Err(_) => {
                                    eprintln!("schedule retention timed out");
                                    host.record_runtime_fault("schedule_retention", "", "timeout").await;
                                }
                            }
                        }
                    }
                }
            }
        });
    }
    for (owner, index) in declared {
        let stop = stop.child_token();
        let host = host.clone();
        let slots = slots.clone();
        schedules.spawn(async move { tick(host, stop, owner, index, slots).await });
    }
    while schedules.join_next().await.is_some() {}
    if let Some(slots) = slots {
        let released = async {
            let client = host.engine.connection().await?;
            slots.release(&client).await
        };
        match tokio::time::timeout(Duration::from_secs(2), released).await {
            Ok(Ok(())) => {}
            Ok(Err(error)) => eprintln!("schedule leases were not released: {error:#}"),
            Err(_) => eprintln!("schedule leases were not released in time"),
        }
    }
}

/// The coordinator of a host with an application database.
fn coordinated() -> Option<Slots> {
    std::env::var_os("DATABASE_URL")
        .filter(|address| !address.is_empty())
        .map(|_| {
            let owner = random_hex::<16>().unwrap_or_else(|_| std::process::id().to_string());
            Slots::new(owner, LEASE)
        })
}

fn key(host: &Host, owner: usize, index: usize) -> Key {
    let service = &host.bundle.host.workers[owner];
    Key {
        service: format!("{}:{}", host.bundle.host.name, service.address),
        schedule: service.schedules[index].address.clone(),
    }
}

/// The first slot boundary after `now`: the next UTC instant, in
/// nanoseconds, that `period` divides. Every replica computes the same one.
pub(crate) fn next_slot(now: i128, period: i128) -> i128 {
    now - now.rem_euclid(period) + period
}

/// What a single scheduler does when a slot arrives while `active` runs.
#[derive(Debug, PartialEq)]
enum Overlap {
    Run,
    Replace,
    Queue,
    Skip,
}

fn local_overlap(policy: &str, active: bool) -> Overlap {
    match (policy, active) {
        ("allow", _) | (_, false) => Overlap::Run,
        ("replace", true) => Overlap::Replace,
        ("queue", true) => Overlap::Queue,
        _ => Overlap::Skip,
    }
}

async fn tick(
    host: Arc<Host>,
    stop: CancellationToken,
    owner: usize,
    index: usize,
    slots: Option<Arc<Slots>>,
) {
    let schedule = &host.bundle.host.workers[owner].schedules[index];
    let cadence = Duration::from_nanos(schedule.every);
    let period = i128::from(schedule.every);
    let clock = schedule_slots::now();
    let mut slot = next_slot(clock, period);
    let mut next = Instant::now() + Duration::from_nanos((slot - clock) as u64);
    let key = Arc::new(key(&host, owner, index));
    let mut active = JoinSet::new();
    let mut queue = VecDeque::new();
    loop {
        let start = tokio::select! {
            () = stop.cancelled() => break,
            _ = active.join_next(), if !active.is_empty() => {
                if active.is_empty() && slots.is_none() {
                    while queue.front().is_some_and(|scheduled: &Instant| {
                        schedule.catchup_maximum_age > 0 && scheduled.elapsed() > Duration::from_nanos(schedule.catchup_maximum_age)
                    }) {
                        queue.pop_front();
                    }
                    queue.pop_front().map(|_| None)
                } else { None }
            }
            () = sleep_until(next) => {
                let (current, at) = (next, slot);
                next += cadence;
                slot += period;
                match &slots {
                    None => match local_overlap(&schedule.overlap, !active.is_empty()) {
                        Overlap::Run => Some(None),
                        Overlap::Replace => { active.abort_all(); Some(None) }
                        Overlap::Queue => { queue.push_back(current); None }
                        Overlap::Skip => None,
                    },
                    Some(slots) => {
                        let claimed = tokio::select! {
                            () = stop.cancelled() => break,
                            result = claim(&host, slots, &key, at, schedule) => result,
                        };
                        match claimed {
                            Ok(Some(run)) => {
                                if schedule.overlap == "replace" {
                                    active.abort_all();
                                }
                                Some(Some(run))
                            }
                            Ok(None) => None,
                            Err(error) => {
                                // Without the claim no replica may assume the
                                // slot is free; it is skipped, never run twice.
                                eprintln!("schedule {} could not claim its slot: {error:#}", schedule.address);
                                host.record_runtime_fault("schedule_claim", &schedule.address, Host::classify_runtime_error(&error)).await;
                                None
                            }
                        }
                    }
                }
            }
        };
        let Some(run) = start else {
            continue;
        };
        let host = host.clone();
        match (run, &slots) {
            (Some(run), Some(slots)) => {
                let (slots, key) = (slots.clone(), key.clone());
                active.spawn(
                    async move { coordinated_run(host, slots, key, owner, index, run).await },
                );
            }
            _ => {
                active.spawn(async move {
                    let service = &host.bundle.host.workers[owner];
                    if let Err(error) = invoke(&host, service, &service.schedules[index]).await {
                        host.telemetry.fault_failure(&error, error.code);
                    }
                });
            }
        }
    }
    active.shutdown().await;
}

async fn claim(
    host: &Host,
    slots: &Slots,
    key: &Key,
    at: i128,
    schedule: &Schedule,
) -> anyhow::Result<Option<Run>> {
    let catchup = Duration::from_nanos(schedule.catchup_maximum_age);
    let claim = async {
        slots.ensure_schema().await?;
        let client = host.engine.connection().await?;
        slots
            .claim(
                &client,
                key,
                schedule_slots::micros(at),
                &schedule.overlap,
                catchup,
            )
            .await
    };
    tokio::time::timeout(Duration::from_secs(10), claim)
        .await
        .map_err(|_| anyhow::anyhow!("claim timed out"))?
}

/// Runs a claimed slot and, for a `queue` schedule, every slot that waited
/// behind it. A leased run renews its lease while it runs and stops before its
/// last confirmed lease expires or when another replica replaces it.
async fn coordinated_run(
    host: Arc<Host>,
    slots: Arc<Slots>,
    key: Arc<Key>,
    owner: usize,
    index: usize,
    run: Run,
) {
    let service = &host.bundle.host.workers[owner];
    let schedule = &service.schedules[index];
    let mut next = Some(run);
    while let Some(run) = next.take() {
        let invocation = invoke(&host, service, schedule);
        tokio::pin!(invocation);
        let mut lease_deadline = Instant::now() + slots.lease();
        let mut renewal = interval(slots.heartbeat());
        renewal.tick().await;
        let result = loop {
            tokio::select! {
                result = &mut invocation => break Some(result),
                () = sleep_until(lease_deadline), if run.leased => {
                    eprintln!("schedule {} lease expired before renewal succeeded; cancelling run", schedule.address);
                    break None;
                }
                _ = renewal.tick(), if run.leased => {
                    let started = Instant::now();
                    let renewed = async {
                        let client = host.engine.connection().await?;
                        slots.renew(&client, &key, &run).await
                    };
                    match tokio::time::timeout(Duration::from_secs(5), renewed).await {
                        Ok(Ok(true)) => lease_deadline = started + slots.lease(),
                        // Replaced or taken over: dropping the invocation
                        // cancels it, as a local `replace` does.
                        Ok(Ok(false)) => break None,
                        Ok(Err(error)) => eprintln!("schedule {} could not renew its lease: {error:#}", schedule.address),
                        Err(_) => eprintln!("schedule {} lease renewal timed out", schedule.address),
                    }
                }
            }
        };
        if let Some(Err(error)) = result {
            host.telemetry.fault_failure(&error, error.code);
        }
        let finished = async {
            let client = host.engine.connection().await?;
            slots.finish(&client, &key, &run).await?;
            if schedule.overlap == "queue" {
                let catchup = Duration::from_nanos(schedule.catchup_maximum_age);
                return slots.promote(&client, &key, catchup).await;
            }
            Ok(None)
        };
        match tokio::time::timeout(Duration::from_secs(5), finished).await {
            Ok(Ok(promoted)) => next = promoted,
            Ok(Err(error)) => eprintln!(
                "schedule {} could not record its run: {error:#}",
                schedule.address
            ),
            Err(_) => eprintln!("schedule {} finish timed out", schedule.address),
        }
    }
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
        if outcome.is_streamed() {
            return Err(Fault::internal("scheduled operation returned a stream"));
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn slots_are_period_aligned_utc_boundaries() {
        let minute = 60_000_000_000_i128;
        // 2026-11-26T10:15:42.5Z falls in the slot that starts at 10:16.
        let now = 1_795_688_142_500_000_000_i128;
        let slot = next_slot(now, minute);
        assert_eq!(slot % minute, 0);
        assert_eq!(slot - now, 17_500_000_000);
        // Exactly on a boundary, the next slot is a full period away.
        assert_eq!(next_slot(slot, minute), slot + minute);
        // Every replica's clock inside the same period agrees.
        assert_eq!(next_slot(slot - 1, minute), slot);
        assert_eq!(next_slot(slot - minute + 1, minute), slot);
        assert_eq!(schedule_slots::micros(slot), (slot / 1_000) as i64);
        assert_eq!(next_slot(-1, 10), 0);
    }

    #[test]
    fn local_overlap_follows_the_declared_policy() {
        for policy in ["allow", "skip", "replace", "queue"] {
            assert_eq!(local_overlap(policy, false), Overlap::Run, "{policy}");
        }
        assert_eq!(local_overlap("allow", true), Overlap::Run);
        assert_eq!(local_overlap("skip", true), Overlap::Skip);
        assert_eq!(local_overlap("replace", true), Overlap::Replace);
        assert_eq!(local_overlap("queue", true), Overlap::Queue);
        assert_eq!(local_overlap("", true), Overlap::Skip);
    }
}

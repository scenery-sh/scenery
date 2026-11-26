//! PostgreSQL coordination of schedule slots across host replicas.
//!
//! Every replica computes the same slots: the UTC instants a schedule's
//! period divides. The replica that inserts a slot's row runs it; the
//! others see the row and skip. A slot that runs under the `skip`,
//! `replace`, or `queue` overlap policy holds a lease that its runner
//! renews, so a crashed replica's slot expires instead of blocking the
//! schedule, and a unique index lets at most one leased run exist per
//! schedule.

use std::time::Duration;

use anyhow::Result;
use time::OffsetDateTime;
use tokio::sync::OnceCell;
use tokio_postgres::Client;
use tokio_postgres::error::SqlState;

/// How long a run's lease lasts without renewal.
pub(crate) const LEASE: Duration = Duration::from_secs(30);

/// Identifies one schedule of one application in the shared table.
pub(crate) struct Key {
    pub service: String,
    pub schedule: String,
}

/// A slot this replica must run now. A leased run renews its lease and
/// stops if another replica replaced it.
#[derive(Clone, Copy, Debug, PartialEq)]
pub(crate) struct Run {
    pub slot: i64,
    pub leased: bool,
}

/// One replica's claims. `owner` is unique per host process.
pub(crate) struct Slots {
    owner: String,
    lease: Duration,
    installed: OnceCell<()>,
}

const SLOT: &str = "'epoch'::timestamptz + $3::bigint * interval '1 microsecond'";

impl Slots {
    pub(crate) fn new(owner: String, lease: Duration) -> Self {
        Self {
            owner,
            lease,
            installed: OnceCell::new(),
        }
    }

    /// The interval at which a leased run renews its lease.
    pub(crate) fn heartbeat(&self) -> Duration {
        self.lease / 3
    }

    fn lease_ms(&self) -> i64 {
        i64::try_from(self.lease.as_millis()).unwrap_or(i64::MAX)
    }

    /// Installs the schedule tables once per process, retrying on the next
    /// claim after a failure.
    pub(crate) async fn ensure_schema(&self) -> Result<()> {
        self.installed
            .get_or_try_init(|| async {
                let (client, _driver) = crate::durable::connect().await?;
                crate::durable::install_schema(&client).await
            })
            .await?;
        Ok(())
    }

    /// Claims `slot` (UTC microseconds) under the schedule's `overlap`
    /// policy and returns the run this replica must start, if any. With
    /// `queue`, that is the oldest waiting slot no older than `catchup`
    /// (zero keeps every slot), which need not be `slot` itself.
    pub(crate) async fn claim(
        &self,
        client: &Client,
        key: &Key,
        slot: i64,
        overlap: &str,
        catchup: Duration,
    ) -> Result<Option<Run>> {
        self.expire(client, key).await?;
        let state = match overlap {
            "allow" => "claimed",
            "queue" => "queued",
            _ => "running",
        };
        if overlap == "replace" {
            client
                .execute(
                    &format!(
                        "UPDATE scenery.schedule_slots SET state = 'replaced', lease_until = NULL, updated_at = now()
                         WHERE service = $1 AND schedule = $2 AND state = 'running' AND slot_at < {SLOT}"
                    ),
                    &[&key.service, &key.schedule, &slot],
                )
                .await?;
        }
        let inserted = client
            .query_opt(
                &format!(
                    "INSERT INTO scenery.schedule_slots (service, schedule, slot_at, state, owner, lease_until)
                     VALUES ($1, $2, {SLOT}, $4::text, $5,
                         CASE WHEN $4::text = 'running' THEN now() + $6::bigint * interval '1 millisecond' END)
                     ON CONFLICT DO NOTHING RETURNING 1"
                ),
                &[&key.service, &key.schedule, &slot, &state, &self.owner, &self.lease_ms()],
            )
            .await?
            .is_some();
        match (inserted, state) {
            (false, _) => Ok(None),
            (true, "queued") => self.promote(client, key, catchup).await,
            (true, state) => Ok(Some(Run {
                slot,
                leased: state == "running",
            })),
        }
    }

    /// Starts the oldest waiting slot of a `queue` schedule when no run of
    /// it holds a lease.
    pub(crate) async fn promote(
        &self,
        client: &Client,
        key: &Key,
        catchup: Duration,
    ) -> Result<Option<Run>> {
        self.expire(client, key).await?;
        let catchup = i64::try_from(catchup.as_millis()).unwrap_or(i64::MAX);
        client
            .execute(
                "UPDATE scenery.schedule_slots SET state = 'expired', updated_at = now()
                 WHERE service = $1 AND schedule = $2 AND state = 'queued' AND $3::bigint > 0
                   AND slot_at < now() - $3::bigint * interval '1 millisecond'",
                &[&key.service, &key.schedule, &catchup],
            )
            .await?;
        let promoted = client
            .query_opt(
                "UPDATE scenery.schedule_slots
                 SET state = 'running', owner = $3, lease_until = now() + $4::bigint * interval '1 millisecond',
                     updated_at = now()
                 WHERE (service, schedule, slot_at) = (
                     SELECT service, schedule, slot_at FROM scenery.schedule_slots
                     WHERE service = $1 AND schedule = $2 AND state = 'queued'
                     ORDER BY slot_at LIMIT 1 FOR UPDATE SKIP LOCKED)
                 RETURNING (extract(epoch FROM slot_at) * 1000000)::bigint",
                &[&key.service, &key.schedule, &self.owner, &self.lease_ms()],
            )
            .await;
        match promoted {
            Ok(row) => Ok(row.map(|row| Run {
                slot: row.get(0),
                leased: true,
            })),
            // Another run holds the schedule's lease.
            Err(error) if error.code() == Some(&SqlState::UNIQUE_VIOLATION) => Ok(None),
            Err(error) => Err(error.into()),
        }
    }

    /// Extends a leased run. `false` means the run lost its lease: another
    /// replica replaced it, or it expired and was taken over.
    pub(crate) async fn renew(&self, client: &Client, key: &Key, run: &Run) -> Result<bool> {
        Ok(client
            .query_opt(
                &format!(
                    "UPDATE scenery.schedule_slots
                     SET lease_until = now() + $5::bigint * interval '1 millisecond', updated_at = now()
                     WHERE service = $1 AND schedule = $2 AND slot_at = {SLOT}
                       AND state = 'running' AND owner = $4 RETURNING 1"
                ),
                &[&key.service, &key.schedule, &run.slot, &self.owner, &self.lease_ms()],
            )
            .await?
            .is_some())
    }

    /// Records a finished run and releases its lease.
    pub(crate) async fn finish(&self, client: &Client, key: &Key, run: &Run) -> Result<()> {
        client
            .execute(
                &format!(
                    "UPDATE scenery.schedule_slots SET state = 'done', lease_until = NULL, updated_at = now()
                     WHERE service = $1 AND schedule = $2 AND slot_at = {SLOT}
                       AND owner = $4 AND state IN ('running', 'claimed')"
                ),
                &[&key.service, &key.schedule, &run.slot, &self.owner],
            )
            .await?;
        Ok(())
    }

    /// Ends every lease this replica holds, so a stopping host's schedules
    /// pass to the other replicas at once instead of when the leases expire.
    pub(crate) async fn release(&self, client: &Client) -> Result<()> {
        client
            .execute(
                "UPDATE scenery.schedule_slots SET state = 'expired', lease_until = NULL, updated_at = now()
                 WHERE owner = $1 AND state = 'running'",
                &[&self.owner],
            )
            .await?;
        Ok(())
    }

    /// Ends the leases of crashed replicas.
    async fn expire(&self, client: &Client, key: &Key) -> Result<()> {
        client
            .execute(
                "UPDATE scenery.schedule_slots SET state = 'expired', updated_at = now()
                 WHERE service = $1 AND schedule = $2 AND state = 'running' AND lease_until < now()",
                &[&key.service, &key.schedule],
            )
            .await?;
        Ok(())
    }
}

/// Deletes the slots of `services` older than a day, keeping only a run
/// whose lease is still valid. No replica claims a slot that old again.
pub(crate) async fn prune(client: &Client, services: &[String]) -> Result<u64> {
    Ok(client
        .execute(
            "DELETE FROM scenery.schedule_slots
             WHERE service = ANY($1::text[]) AND slot_at < now() - interval '1 day'
               AND (state <> 'running' OR lease_until < now())",
            &[&services],
        )
        .await?)
}

/// The slot of `at` in UTC microseconds, as the table stores it.
pub(crate) fn micros(at: i128) -> i64 {
    i64::try_from(at.div_euclid(1_000)).unwrap_or(i64::MAX)
}

/// The current UTC time in nanoseconds.
pub(crate) fn now() -> i128 {
    OffsetDateTime::now_utc().unix_timestamp_nanos()
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeMap;
    use std::sync::Arc;

    use super::*;
    use crate::durable::{connect, install_schema};

    async fn client() -> Result<Client> {
        let (client, _driver) = connect().await?;
        Ok(client)
    }

    fn key(schedule: &str) -> Key {
        Key {
            service: "slots-probe:billing/service/billing".into(),
            schedule: format!("billing/schedule/{schedule}"),
        }
    }

    /// Two schedulers with their own connections race for the same slots,
    /// as two replicas behind a load balancer do.
    #[tokio::test]
    #[ignore = "requires a local PostgreSQL database in DATABASE_URL"]
    async fn competing_replicas_run_each_slot_exactly_once() -> Result<()> {
        let setup = client().await?;
        install_schema(&setup).await?;
        setup
            .execute(
                "DELETE FROM scenery.schedule_slots WHERE service = $1",
                &[&key("").service],
            )
            .await?;
        let replicas: Vec<_> = ["replica-a", "replica-b"]
            .into_iter()
            .map(|owner| Arc::new(Slots::new(owner.into(), LEASE)))
            .collect();
        let base = micros(now());
        for overlap in ["allow", "skip", "replace", "queue"] {
            let mut tasks = Vec::new();
            for slots in &replicas {
                let slots = slots.clone();
                tasks.push(tokio::spawn(async move {
                    let client = client().await?;
                    let key = key(overlap);
                    let mut runs = Vec::new();
                    for index in 0..60 {
                        let slot = base + index * 1_000_000;
                        let Some(mut run) = slots
                            .claim(&client, &key, slot, overlap, Duration::ZERO)
                            .await?
                        else {
                            continue;
                        };
                        // A queue's finishing run starts the slot waiting
                        // behind it, which the other replica may have queued.
                        loop {
                            runs.push(run.slot);
                            slots.finish(&client, &key, &run).await?;
                            if overlap != "queue" {
                                break;
                            }
                            match slots.promote(&client, &key, Duration::ZERO).await? {
                                Some(next) => run = next,
                                None => break,
                            }
                        }
                    }
                    anyhow::Ok(runs)
                }));
            }
            let mut counts = BTreeMap::<i64, usize>::new();
            for task in tasks {
                for slot in task.await?? {
                    *counts.entry(slot).or_default() += 1;
                }
            }
            assert!(
                counts.values().all(|count| *count == 1),
                "{overlap}: a slot ran twice: {counts:?}"
            );
            if overlap != "queue" {
                assert_eq!(counts.len(), 60, "{overlap}: every slot ran");
            }
            // Drain what a queue left behind once both replicas are done.
            if overlap == "queue" {
                let key = key(overlap);
                while let Some(run) = replicas[0].promote(&setup, &key, Duration::ZERO).await? {
                    assert!(
                        counts.insert(run.slot, 1).is_none(),
                        "queued slot ran twice"
                    );
                    replicas[0].finish(&setup, &key, &run).await?;
                }
                assert_eq!(counts.len(), 60, "every queued slot ran once");
            }
        }
        Ok(())
    }

    #[tokio::test]
    #[ignore = "requires a local PostgreSQL database in DATABASE_URL"]
    async fn overlap_policies_and_leases_hold_across_replicas() -> Result<()> {
        let client = client().await?;
        install_schema(&client).await?;
        let service = "slots-policy:jobs/service/jobs";
        client
            .execute(
                "DELETE FROM scenery.schedule_slots WHERE service = $1",
                &[&service],
            )
            .await?;
        let key = |schedule: &str| Key {
            service: service.into(),
            schedule: schedule.into(),
        };
        let a = Slots::new("a".into(), Duration::from_millis(300));
        let b = Slots::new("b".into(), Duration::from_millis(300));
        let zero = Duration::ZERO;

        // skip: a running slot makes the next one skip on every replica.
        let skip = key("skip");
        let first = a.claim(&client, &skip, 1, "skip", zero).await?.unwrap();
        assert!(first.leased);
        assert_eq!(b.claim(&client, &skip, 2, "skip", zero).await?, None);
        assert!(a.renew(&client, &skip, &first).await?);
        a.finish(&client, &skip, &first).await?;
        assert!(b.claim(&client, &skip, 3, "skip", zero).await?.is_some());

        // A crashed replica's lease expires instead of wedging the schedule.
        let crashed = key("crashed");
        let stale = a.claim(&client, &crashed, 1, "skip", zero).await?.unwrap();
        assert_eq!(b.claim(&client, &crashed, 2, "skip", zero).await?, None);
        tokio::time::sleep(Duration::from_millis(400)).await;
        assert!(b.claim(&client, &crashed, 3, "skip", zero).await?.is_some());
        assert!(
            !a.renew(&client, &crashed, &stale).await?,
            "the expired run lost its lease"
        );

        // replace: the newer slot takes the lease from the running one.
        let replace = key("replace");
        let old = a
            .claim(&client, &replace, 1, "replace", zero)
            .await?
            .unwrap();
        let new = b
            .claim(&client, &replace, 2, "replace", zero)
            .await?
            .unwrap();
        assert!(!a.renew(&client, &replace, &old).await?);
        assert!(b.renew(&client, &replace, &new).await?);
        // A stopping replica hands its leases over at once.
        b.release(&client).await?;
        assert!(!b.renew(&client, &replace, &new).await?);
        assert!(
            a.claim(&client, &replace, 3, "replace", zero)
                .await?
                .is_some()
        );

        // queue: later slots wait in order and the finishing run starts them.
        let queue = key("queue");
        let running = a.claim(&client, &queue, 1, "queue", zero).await?.unwrap();
        assert_eq!(b.claim(&client, &queue, 2, "queue", zero).await?, None);
        assert_eq!(b.claim(&client, &queue, 3, "queue", zero).await?, None);
        assert_eq!(b.promote(&client, &queue, zero).await?, None);
        a.finish(&client, &queue, &running).await?;
        let next = a.promote(&client, &queue, zero).await?.unwrap();
        assert_eq!(next.slot, 2);
        a.finish(&client, &queue, &next).await?;
        // A waiting slot older than the catch-up age is dropped.
        tokio::time::sleep(Duration::from_millis(50)).await;
        assert_eq!(
            b.promote(&client, &queue, Duration::from_millis(1)).await?,
            None
        );

        // allow: every slot runs once, alongside a running one.
        let allow = key("allow");
        assert!(a.claim(&client, &allow, 1, "allow", zero).await?.is_some());
        assert!(b.claim(&client, &allow, 2, "allow", zero).await?.is_some());
        assert_eq!(a.claim(&client, &allow, 2, "allow", zero).await?, None);

        // Old slots are pruned; a valid lease is kept.
        client
            .execute(
                "UPDATE scenery.schedule_slots
                 SET slot_at = slot_at - interval '2 days', lease_until = now() - interval '1 second'
                 WHERE service = $1",
                &[&service],
            )
            .await?;
        let held = a
            .claim(&client, &key("held"), micros(now()), "skip", zero)
            .await?
            .unwrap();
        client
            .execute(
                "UPDATE scenery.schedule_slots SET slot_at = slot_at - interval '2 days', lease_until = now() + interval '1 hour'
                 WHERE service = $1 AND schedule = 'held'",
                &[&service],
            )
            .await?;
        assert!(prune(&client, &[service.into()]).await? > 0);
        let left: i64 = client
            .query_one(
                "SELECT count(*) FROM scenery.schedule_slots WHERE service = $1",
                &[&service],
            )
            .await?
            .get(0);
        assert_eq!(left, 1, "only the leased run remains");
        assert!(held.leased);
        Ok(())
    }
}

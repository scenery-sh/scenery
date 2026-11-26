//! A bounded pool of application database sessions for request-scoped work.

use std::ops::{Deref, DerefMut};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use anyhow::{Context, Result};
use tokio::sync::{OwnedSemaphorePermit, Semaphore};
use tokio::task::JoinHandle;
use tokio_postgres::Client;

/// The Go runtime's ceiling for the same routes (`SetMaxOpenConns(10)`).
const SESSIONS: usize = 10;
const ACQUIRE_TIMEOUT: Duration = Duration::from_secs(5);

type Idle = Arc<Mutex<Vec<(Client, JoinHandle<()>)>>>;

/// Hands out at most `SESSIONS` database sessions at a time and keeps the
/// idle ones open, so an unauthenticated request can neither open a backend
/// of its own nor exhaust the database's connection limit.
pub(crate) struct Pool {
    permits: Arc<Semaphore>,
    idle: Idle,
}

impl Pool {
    pub(crate) fn new() -> Self {
        Self {
            permits: Arc::new(Semaphore::new(SESSIONS)),
            idle: Idle::default(),
        }
    }

    /// Waits briefly for a free session. Saturation is an error for the
    /// caller to report as unavailable, never an unbounded queue.
    pub(crate) async fn acquire(&self) -> Result<Session> {
        let permit = tokio::time::timeout(ACQUIRE_TIMEOUT, self.permits.clone().acquire_owned())
            .await
            .context("application database sessions are exhausted")??;
        let reused = loop {
            match self.idle.lock().expect("session pool").pop() {
                Some((client, driver)) if !client.is_closed() => break Some((client, driver)),
                Some(_) => {}
                None => break None,
            }
        };
        let (client, driver) = match reused {
            Some(session) => session,
            None => crate::durable::connect().await?,
        };
        Ok(Session {
            session: Some((client, driver)),
            idle: self.idle.clone(),
            reusable: true,
            _permit: permit,
        })
    }

    #[cfg(test)]
    fn idle(&self) -> usize {
        self.idle.lock().expect("session pool").len()
    }
}

/// One checked-out session. Dropping it returns the session to the pool,
/// unless it still holds state that only closing the session releases.
pub(crate) struct Session {
    session: Option<(Client, JoinHandle<()>)>,
    idle: Idle,
    reusable: bool,
    _permit: OwnedSemaphorePermit,
}

impl Session {
    /// Marks session-level state, such as an advisory lock. A request that
    /// is dropped while holding it closes the session to release it.
    pub(crate) fn hold_session_state(&mut self) {
        self.reusable = false;
    }

    /// Releases every session-level lock so the session can be reused. A
    /// session that fails to release them is closed instead.
    pub(crate) async fn release_session_state(&mut self) -> Result<()> {
        if !self.reusable {
            self.execute("SELECT pg_advisory_unlock_all()", &[]).await?;
            self.reusable = true;
        }
        Ok(())
    }
}

impl Deref for Session {
    type Target = Client;

    fn deref(&self) -> &Client {
        &self.session.as_ref().expect("session is checked out").0
    }
}

impl DerefMut for Session {
    fn deref_mut(&mut self) -> &mut Client {
        &mut self.session.as_mut().expect("session is checked out").0
    }
}

impl Drop for Session {
    fn drop(&mut self) {
        let Some((client, driver)) = self.session.take() else {
            return;
        };
        if self.reusable && !client.is_closed() {
            self.idle
                .lock()
                .expect("session pool")
                .push((client, driver));
        } else {
            // Ending the connection is what releases its session state.
            driver.abort();
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Runs against `DATABASE_URL`, like the browser authentication probe.
    #[tokio::test]
    #[ignore = "requires a local PostgreSQL database in DATABASE_URL"]
    async fn sessions_are_bounded_reused_and_closed_while_holding_locks() -> Result<()> {
        let pool = Pool::new();
        async fn backend(session: &Session) -> Result<i32> {
            let row = session.query_one("SELECT pg_backend_pid()", &[]).await?;
            Ok(row.get(0))
        }
        let first = pool.acquire().await?;
        let pid = backend(&first).await?;
        drop(first);
        assert_eq!(pool.idle(), 1);
        let mut again = pool.acquire().await?;
        assert_eq!(backend(&again).await?, pid, "an idle session is reused");

        // A session dropped while it holds an advisory lock is closed, so
        // the lock cannot leak to the next request.
        again
            .execute(
                "SELECT pg_advisory_lock(hashtextextended('pool-test', 0))",
                &[],
            )
            .await?;
        again.hold_session_state();
        drop(again);
        assert_eq!(pool.idle(), 0);
        let mut next = pool.acquire().await?;
        assert_ne!(backend(&next).await?, pid);
        let free: bool = next
            .query_one(
                "SELECT pg_try_advisory_lock(hashtextextended('pool-test', 0))",
                &[],
            )
            .await?
            .get(0);
        assert!(free, "the dropped session released its lock");
        next.hold_session_state();
        next.release_session_state().await?;
        drop(next);
        assert_eq!(pool.idle(), 1);

        // The pool never opens more than its ceiling.
        let mut held = Vec::new();
        for _ in 0..SESSIONS {
            held.push(pool.acquire().await?);
        }
        assert!(
            tokio::time::timeout(Duration::from_millis(100), pool.acquire())
                .await
                .is_err(),
            "an eleventh session must wait"
        );
        Ok(())
    }
}

use super::*;

#[test]
fn active_jobs_of_another_revision_require_a_migration() {
    let declared = BTreeMap::from([(("orders", "ship"), 2), (("orders", "bill"), 1)]);
    let active =
        |name: &str, revision: i32| vec![("orders".to_owned(), name.to_owned(), revision, 3)];
    assert!(migration_required(&declared, &[]).is_ok());
    assert!(migration_required(&declared, &active("ship", 2)).is_ok());
    assert!(migration_required(&declared, &active("bill", 1)).is_ok());
    // A bumped revision with jobs still queued at the previous one.
    let error = migration_required(&declared, &active("ship", 1)).unwrap_err();
    assert_eq!(
        error.to_string(),
        "durable store: migration_required: task \"ship\" has 3 active jobs at revision 1 but runtime provides revision 2"
    );
    // A removed execution, and the same name in another service.
    assert!(migration_required(&declared, &active("refund", 1)).is_err());
    let other = vec![("billing".to_owned(), "ship".to_owned(), 2, 1)];
    assert!(migration_required(&declared, &other).is_err());
}

fn registration(name: &str, revision: u32) -> Registration {
    serde_json::from_value(json!({
        "Address": format!("orders/execution/{name}"), "EngineAddress": "orders/engine/main",
        "ExternalName": name, "Service": "orders-probe", "Revision": revision,
        "DefaultTimeout": 60_000_000_000_u64, "DefaultLease": 30_000_000_000_u64,
        "MaxAttempts": 3, "RetryInitial": 1_000_000_000, "RetryMax": 2_000_000_000,
        "RetryBackoff": 2.0, "RetryJitter": 0.0, "SuccessRetention": 0, "FailureRetention": 0,
        "MaxConcurrency": 1, "DeduplicationRetention": 0, "DeduplicationConflict": "return_existing",
    }))
    .unwrap()
}

/// Runs the statements this module adds against a real server.
#[tokio::test]
#[ignore = "requires a local PostgreSQL database in DATABASE_URL"]
async fn postgres_guards_revisions_and_releases_leases() -> Result<()> {
    let (mut client, _driver) = connect().await?;
    let first = registration("ship", 1);
    reconcile(&mut client, &[&first]).await?;
    client
        .execute(
            "DELETE FROM scenery.durable_jobs WHERE service='orders-probe'",
            &[],
        )
        .await?;
    client.execute(
        "INSERT INTO scenery.durable_jobs (service,id,task_name,task_version,state,input_blob,memo_json,max_attempts)
         VALUES ('orders-probe','job-1','ship',1,'queued','{}'::bytea,'{}'::jsonb,3)", &[]).await?;

    // The same revision reconciles again; a bumped one must not strand
    // the queued job, and neither may dropping the execution.
    reconcile(&mut client, &[&first]).await?;
    let bumped = registration("ship", 2);
    let error = reconcile(&mut client, &[&bumped]).await.unwrap_err();
    assert!(
        error.to_string().contains("migration_required"),
        "{error:#}"
    );
    let other = registration("bill", 1);
    assert!(reconcile(&mut client, &[&other]).await.is_err());
    let enabled: bool = client
        .query_one(
            "SELECT enabled FROM scenery.durable_tasks WHERE service='orders-probe' AND name='ship'",
            &[],
        )
        .await?
        .get(0);
    assert!(enabled, "a refused reconciliation changes nothing");

    // A lease handed back at shutdown returns the job and its attempt.
    client.execute(
        "UPDATE scenery.durable_jobs SET state='running', attempt=1, lease_id='lease-1',
            lease_owner='host', lease_until=now()+interval '1 minute', timeout_at=now()+interval '1 minute'
         WHERE service='orders-probe' AND id='job-1'", &[]).await?;
    release(&client, &first, "job-1", "another-lease").await?;
    async fn state(client: &Client) -> Result<(String, i32, bool)> {
        let row = client
            .query_one(
                "SELECT state, attempt, lease_id IS NULL FROM scenery.durable_jobs
                 WHERE service='orders-probe' AND id='job-1'",
                &[],
            )
            .await?;
        Ok((row.get(0), row.get(1), row.get(2)))
    }
    assert_eq!(state(&client).await?, ("running".into(), 1, false));
    release(&client, &first, "job-1", "lease-1").await?;
    assert_eq!(state(&client).await?, ("queued".into(), 0, true));
    let released: i64 = client
        .query_one(
            "SELECT count(*) FROM scenery.durable_job_events
             WHERE service='orders-probe' AND job_id='job-1' AND event_type='job.lease_released'",
            &[],
        )
        .await?
        .get(0);
    assert_eq!(released, 1);

    // Once the job is gone the revision may move on, and the task the
    // deployment stopped declaring is disabled.
    client
        .execute(
            "DELETE FROM scenery.durable_jobs WHERE service='orders-probe'",
            &[],
        )
        .await?;
    reconcile(&mut client, &[&other]).await?;
    let rows = client
        .query(
            "SELECT name, enabled FROM scenery.durable_tasks WHERE service='orders-probe' ORDER BY name",
            &[],
        )
        .await?;
    let tasks: Vec<(String, bool)> = rows.iter().map(|row| (row.get(0), row.get(1))).collect();
    assert_eq!(tasks, [("bill".into(), true), ("ship".into(), false)]);
    client
        .execute(
            "DELETE FROM scenery.durable_tasks WHERE service='orders-probe'",
            &[],
        )
        .await?;
    Ok(())
}

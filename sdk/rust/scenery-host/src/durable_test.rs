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

/// Jobs already in a database carry the keys Go's
/// `contract.EncodeContractCompositeKey` built, which the shared corpus pins.
#[test]
fn dispatch_keys_match_the_go_corpus() {
    use base64::Engine;
    use base64::engine::general_purpose::URL_SAFE_NO_PAD;

    #[derive(Deserialize)]
    struct Case {
        name: String,
        fields: Vec<Key>,
        input: String,
        #[serde(default)]
        canonical: String,
        #[serde(default)]
        reject: bool,
    }
    let cases: Vec<Case> = serde_json::from_str(include_str!(
        "../../../../internal/contract/testdata/worker-keys-v1.json"
    ))
    .unwrap();
    assert!(cases.len() >= 15);
    for case in cases {
        let input = values::parse(case.input.as_bytes()).unwrap();
        let key = dispatch_key(&input, &case.fields);
        if case.reject {
            assert!(key.is_err(), "accepted {}", case.name);
            continue;
        }
        let expected = format!("scenery-key-v1.{}", URL_SAFE_NO_PAD.encode(&case.canonical));
        assert_eq!(key.unwrap(), expected, "{}", case.name);
    }
    // The exponent form Go's wire gives a float is keyed as its exact decimal.
    let amount: Vec<Key> =
        serde_json::from_value(json!([{"Name":"amount","Type":"float64"}])).unwrap();
    assert_eq!(
        dispatch_key(&values::parse(br#"{"amount":1e+06}"#).unwrap(), &amount).unwrap(),
        "scenery-key-v1.W3sic3RhdGUiOiJ2YWx1ZSIsInR5cGUiOiJmbG9hdDY0IiwidmFsdWUiOjEwMDAwMDB9XQ"
    );
    assert_eq!(dispatch_key(&json!({}), &[]).unwrap(), "");
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

#[tokio::test]
async fn execute_next_returns_immediately_when_stop_is_cancelled() -> Result<()> {
    let (_sender, receiver) = tokio::sync::watch::channel(None);
    let bundle: crate::plan::Bundle = serde_json::from_value(json!({
        "version": 1, "contract_revision": "contract", "programs": [],
        "reports": {"schema_revision": "sha256:fixture", "producer_version": "test"},
        "host": {"Name": "test", "Workers": []}, "types": {},
        "host_config_keys": [],
    }))?;
    let host = Host::new(
        bundle,
        Value::Null,
        Default::default(),
        "control".into(),
        None,
        (
            CancellationToken::new(),
            crate::assistant::Gateway::new(None, None, &[])?,
        ),
        receiver,
    )?;
    let service: Service = serde_json::from_value(json!({
        "Address": "orders/service/orders", "Module": "orders", "Name": "orders",
        "Process": "worker-orders", "Runtime": "go",
        "Operations": {}, "Clients": {}, "Bindings": [], "Durable": {},
        "Schedules": [], "Stores": {},
    }))?;
    let plan: Durable = serde_json::from_value(json!({
        "Operation": "orders/operation/ship",
        "Registration": {
            "Address": "orders/execution/ship", "EngineAddress": "orders/engine/main",
            "ExternalName": "ship", "Service": "orders", "Revision": 1,
            "DefaultTimeout": 0, "DefaultLease": 30_000_000_000_u64,
            "MaxAttempts": 3, "RetryInitial": 1_000_000_000, "RetryMax": 2_000_000_000,
            "RetryBackoff": 2.0, "RetryJitter": 0.0, "SuccessRetention": 0, "FailureRetention": 0,
            "MaxConcurrency": 1, "DeduplicationRetention": 0, "DeduplicationConflict": "return_existing"
        },
        "Dedupe": [], "Concurrency": []
    }))?;
    let stop = CancellationToken::new();
    stop.cancel();
    // Even without DATABASE_URL or a published generation, a cancelled stop
    // token must exit before touching Postgres.
    assert!(!execute_next(&host, &service, &plan, &stop).await?);
    Ok(())
}

#[test]
fn effective_timeout_and_lease_normalize_zero_and_clamp_lease_to_timeout() {
    let make = |timeout: u64, lease: u64| Registration {
        address: "orders/execution/ship".into(),
        engine_address: "orders/engine/main".into(),
        external_name: "ship".into(),
        service: "orders".into(),
        revision: 1,
        default_timeout: timeout,
        default_lease: lease,
        max_attempts: 3,
        retry_initial: 1_000_000_000,
        retry_max: 2_000_000_000,
        retry_backoff: 2.0,
        retry_jitter: 0.0,
        success_retention: 0,
        failure_retention: 0,
        max_concurrency: 1,
        deduplication_retention: 0,
        deduplication_conflict: "return_existing".into(),
    };

    let zero_both = make(0, 0);
    assert_eq!(effective_timeout(&zero_both), Duration::from_secs(60));
    assert_eq!(effective_lease(&zero_both), Duration::from_secs(60));

    let zero_timeout_positive_lease = make(0, 5_000_000_000);
    assert_eq!(
        effective_timeout(&zero_timeout_positive_lease),
        Duration::from_secs(60)
    );
    assert_eq!(
        effective_lease(&zero_timeout_positive_lease),
        Duration::from_secs(5)
    );

    let lease_exceeding_timeout = make(10_000_000_000, 90_000_000_000);
    assert_eq!(
        effective_timeout(&lease_exceeding_timeout),
        Duration::from_secs(10)
    );
    assert_eq!(
        effective_lease(&lease_exceeding_timeout),
        Duration::from_secs(10)
    );

    let sub_ms_lease = make(1_000_000_000, 500_000);
    assert_eq!(effective_lease_ms(&sub_ms_lease), 1);
    assert_eq!(effective_lease(&sub_ms_lease), Duration::from_millis(1));

    let sub_ms_timeout = make(500_000, 0);
    assert_eq!(effective_timeout_ms(&sub_ms_timeout), 1);
    assert_eq!(effective_lease_ms(&sub_ms_timeout), 1);
    assert_eq!(effective_timeout(&sub_ms_timeout), Duration::from_millis(1));
    assert_eq!(effective_lease(&sub_ms_timeout), Duration::from_millis(1));
}

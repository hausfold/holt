// tests/fake-holt.sh stands in for the real binary so tests don't need a Go
// build of holt itself — it's a fixture, not a spec of holt's behavior.
// Shared verbatim with sdk/ts, sdk/python, sdk/swift and sdk/go's fixture of
// the same name; keep them in sync if the wire protocol changes.

use std::pin::Pin;

use futures_core::Stream;
use futures_util::StreamExt;
use holt::{
    landed_verdict, lane_state, watch_kind, HoltClient, HoltError, LeaseOptions, WatchEvent,
    WatchLine,
};

fn new_client() -> HoltClient {
    HoltClient {
        bin: Some("./tests/fake-holt.sh".to_string()),
        ..Default::default()
    }
}

#[tokio::test]
async fn list_reports_lanes_and_nullable_discipline() {
    let client = new_client();
    let envelope = client.list().await.expect("list");

    assert_eq!(envelope.schema, 1);
    assert_eq!(envelope.lanes.len(), 2);

    let sparkle = &envelope.lanes[0];
    assert_eq!(sparkle.state, lane_state::LIVE);
    assert_eq!(sparkle.occupied, Some(true));
    assert_eq!(sparkle.dirty, Some(false));

    let frost = &envelope.lanes[1];
    assert_eq!(
        frost.occupied, None,
        "not determined, must not collapse to false"
    );
    assert_eq!(frost.dirty, None);
    assert_eq!(frost.landed.verdict, landed_verdict::CONTAINED);
}

#[tokio::test]
async fn watch_yields_hello_sync_ready_then_stops_on_drop() {
    let client = new_client();
    let mut stream = Box::pin(client.watch());

    let mut kinds = Vec::new();
    while let Some(line) = stream.next().await {
        let line = line.expect("watch line");
        kinds.push(line.kind.clone());
        if line.kind == watch_kind::CREATED {
            break;
        }
    }

    assert_eq!(
        kinds,
        vec![
            watch_kind::HELLO,
            watch_kind::SYNC,
            watch_kind::READY,
            watch_kind::CREATED
        ]
    );
    // Dropping `stream` here kills the underlying `fake-holt.sh watch` loop
    // (kill_on_drop) — if it didn't, this test process would hang at exit.
}

#[tokio::test]
async fn watch_lane_filters_to_one_lane() {
    let client = new_client();
    // The item type is `WatchEvent`, not `WatchLine` — a lane-scoped stream
    // has no `hello` to carry, so it doesn't hand out a type that could be
    // one. The annotation is half the assertion: it stops compiling if
    // `watch_lane` ever widens back.
    let mut stream: Pin<Box<dyn Stream<Item = Result<WatchEvent, HoltError>> + Send>> =
        Box::pin(client.watch_lane("/repo/.holt/haus/fresh"));

    let event = stream.next().await.expect("one event").expect("ok");
    assert_eq!(event.kind, watch_kind::CREATED);
    assert_eq!(
        event.lane.as_ref().map(|l| l.path.as_str()),
        Some("/repo/.holt/haus/fresh")
    );
}

#[test]
fn into_event_narrows_everything_but_hello() {
    let hello = WatchLine {
        kind: watch_kind::HELLO.to_string(),
        seq: 0,
        holt: Some("0.1.0".to_string()),
        schema: Some(1),
        capabilities: Some(vec!["registry".to_string()]),
        ts: None,
        source: None,
        lane: None,
        message: None,
    };
    assert!(hello.into_event().is_none(), "the header is not an event");

    let line = WatchLine {
        kind: watch_kind::WARNING.to_string(),
        seq: 7,
        holt: None,
        schema: None,
        capabilities: None,
        ts: Some("2026-08-08T12:00:00Z".to_string()),
        source: Some("registry".to_string()),
        lane: None,
        message: Some("a lane went missing".to_string()),
    };
    let event = line.clone().into_event().expect("warning is an event");
    assert_eq!(event.kind, watch_kind::WARNING);
    assert_eq!(event.seq, 7);
    assert_eq!(event.ts, line.ts);
    assert_eq!(event.source, line.source);
    assert_eq!(event.message, line.message);
}

#[tokio::test]
async fn child_returns_only_the_new_path() {
    let client = new_client();
    let dir = client.child("/repo/other", None).await.expect("child");
    assert_eq!(dir, "/repo/.holt/other/new-lane");
}

#[tokio::test]
async fn resume_captured_stdout_never_execs() {
    let client = new_client();
    let out = client.resume("sparkle").await.expect("resume");
    assert!(out.contains("claude --resume"), "out = {out:?}");
}

#[tokio::test]
async fn lease_release_calls_heartbeat_release() {
    let client = new_client();
    let mut lease = client.lease(
        "/repo/.holt/haus/sparkle",
        LeaseOptions {
            pid: Some(12345),
            ..Default::default()
        },
    );
    // fake-holt's heartbeat branch accepts --release silently.
    lease.release().await.expect("release");
    // Idempotent.
    lease.release().await.expect("release again");
}

use std::process::Stdio;

use async_stream::stream;
use futures_core::Stream;
use tokio::io::{AsyncBufReadExt, AsyncReadExt, BufReader};
use tokio::process::Command;

use crate::client::HoltClient;
use crate::errors::HoltError;
use crate::types::{exit_code, watch_kind, WatchEvent, WatchLine};

/// `holt watch --json` as a stream of typed lines. One object per NDJSON
/// line on stdout, in order: `hello`, a `sync` burst for every lane already
/// alive, `ready`, then live changes for as long as the process runs
/// (SPEC.md §14.3 step 2).
///
/// The child process is killed when the stream is dropped — there is no
/// other way to stop it short: `watch` has no built-in end condition, by
/// design (SPEC.md §14).
pub(crate) fn watch_all(
    client: HoltClient,
) -> impl Stream<Item = Result<WatchLine, HoltError>> + Send + 'static {
    stream! {
        let bin = client.bin.clone().unwrap_or_else(|| "holt".to_string());
        let command_repr = vec![bin.clone(), "watch".to_string(), "--json".to_string()];

        let mut cmd = Command::new(&bin);
        cmd.arg("watch").arg("--json");
        if let Some(cwd) = &client.cwd {
            cmd.current_dir(cwd);
        }
        cmd.envs(&client.env);
        cmd.stdin(Stdio::null());
        cmd.stdout(Stdio::piped());
        cmd.stderr(Stdio::piped());
        cmd.kill_on_drop(true);

        let mut child = match cmd.spawn() {
            Ok(child) => child,
            Err(e) => {
                yield Err(HoltError::new(exit_code::USAGE, e.to_string(), command_repr));
                return;
            }
        };

        let stdout = child.stdout.take().expect("stdout was piped");
        let mut stderr = child.stderr.take().expect("stderr was piped");
        let (stderr_tx, stderr_rx) = tokio::sync::oneshot::channel();
        tokio::spawn(async move {
            let mut buf = String::new();
            let _ = stderr.read_to_string(&mut buf).await;
            let _ = stderr_tx.send(buf);
        });

        let mut lines = BufReader::new(stdout).lines();
        loop {
            match lines.next_line().await {
                Ok(Some(line)) => {
                    let trimmed = line.trim();
                    if trimmed.is_empty() {
                        continue;
                    }
                    match serde_json::from_str::<WatchLine>(trimmed) {
                        Ok(parsed) => yield Ok(parsed),
                        Err(e) => {
                            yield Err(HoltError::new(exit_code::USAGE, e.to_string(), command_repr.clone()));
                        }
                    }
                }
                Ok(None) => break,
                Err(e) => {
                    yield Err(HoltError::new(exit_code::USAGE, e.to_string(), command_repr.clone()));
                    break;
                }
            }
        }

        match child.wait().await {
            Ok(status) if status.success() => {}
            Ok(status) => {
                let stderr = stderr_rx.await.unwrap_or_default();
                let code = status.code().unwrap_or(exit_code::USAGE);
                yield Err(HoltError::new(code, stderr, command_repr));
            }
            Err(e) => {
                yield Err(HoltError::new(exit_code::USAGE, e.to_string(), command_repr));
            }
        }
    }
}

/// [`watch_all`], filtered to events about one lane (`event.lane.path`) and
/// stripped of `hello`/`ready` framing — the shape an embedder holding one
/// session per lane usually wants: "tell me when THIS lane's state
/// changes." Compare full paths, not names: names aren't unique across
/// repos, but a checkout path is the registry's own primary key (SPEC.md
/// §2.1).
///
/// Yields [`WatchEvent`], not [`WatchLine`]: `hello` is filtered out here,
/// so the header-only fields can't be populated and shouldn't be in the
/// type. Same contract as `watchLane` in the TS/Python/Swift SDKs.
pub(crate) fn watch_lane(
    client: HoltClient,
    path: String,
) -> impl Stream<Item = Result<WatchEvent, HoltError>> + Send + 'static {
    stream! {
        for await line in watch_all(client) {
            match line {
                Ok(line) => {
                    if line.kind == watch_kind::READY {
                        continue;
                    }
                    let Some(event) = line.into_event() else {
                        continue; // hello: framing, not an event
                    };
                    if event.lane.as_ref().is_some_and(|l| l.path == path) {
                        yield Ok(event);
                    }
                }
                Err(e) => yield Err(e),
            }
        }
    }
}

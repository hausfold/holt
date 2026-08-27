use std::time::Duration;

use tokio::sync::oneshot;
use tokio::task::JoinHandle;

use crate::client::ScruffClient;
use crate::errors::ScruffError;

/// Comfortably under the 90s TTL (`internal/occupancy.TTL`) that applies
/// when there's no pid to watch.
const DEFAULT_REFRESH_INTERVAL: Duration = Duration::from_secs(60);

/// Configures [`ScruffClient::lease`](crate::ScruffClient::lease).
#[derive(Debug, Clone, Copy, Default)]
pub struct LeaseOptions {
    /// When `Some`, ties the lease to a real local process: the kernel
    /// drops it the instant that pid dies, so no refresh loop runs at all
    /// and `refresh_interval` is ignored.
    pub pid: Option<u32>,
    /// Overrides the refresh cadence used when `pid` is `None`. `None`
    /// means 60s, comfortably under the 90s TTL.
    pub refresh_interval: Option<Duration>,
}

/// Holds an occupancy lease for as long as it's open, refreshing it on a
/// background task on an interval comfortably under the 90s TTL. This is
/// the primitive an embedder's "session" (a connection, not a cwd —
/// SPEC.md §14.2) should hold from connect to disconnect:
///
/// ```no_run
/// # async fn go(client: scruff::ScruffClient, lane_dir: &str) -> Result<(), scruff::ScruffError> {
/// let mut lease = client.lease(lane_dir, scruff::LeaseOptions::default());
/// // ... serve the session ...
/// lease.release().await?;
/// # Ok(())
/// # }
/// ```
///
/// A lease can only **save** a lane from `reap`, never condemn one —
/// "nobody leased it" isn't proof nobody's there (SPEC.md §14.2).
///
/// The first heartbeat fires in the background rather than being awaited by
/// [`ScruffClient::lease`](crate::ScruffClient::lease) itself — that method
/// isn't async, so a failure to take the lease surfaces on the next
/// refresh/release call rather than at construction. Call
/// `client.heartbeat(...)` yourself first if you need the initial take to
/// be synchronous.
pub struct Lease {
    client: ScruffClient,
    path: String,
    released: bool,
    cancel_tx: Option<oneshot::Sender<()>>,
    handle: Option<JoinHandle<()>>,
}

impl Lease {
    pub(crate) fn new(client: ScruffClient, path: String, options: LeaseOptions) -> Self {
        let (cancel_tx, cancel_rx) = oneshot::channel();
        let handle = spawn_refresh_loop(client.clone(), path.clone(), options, cancel_rx);
        Self {
            client,
            path,
            released: false,
            cancel_tx: Some(cancel_tx),
            handle: Some(handle),
        }
    }

    /// Drops the lease and stops refreshing it. Safe to call more than
    /// once.
    pub async fn release(&mut self) -> Result<(), ScruffError> {
        if self.released {
            return Ok(());
        }
        self.released = true;
        if let Some(tx) = self.cancel_tx.take() {
            let _ = tx.send(());
        }
        if let Some(handle) = self.handle.take() {
            let _ = handle.await;
        }
        self.client.release_heartbeat(Some(&self.path)).await
    }
}

impl Drop for Lease {
    fn drop(&mut self) {
        // Best-effort: stop the background refresh loop if the caller
        // dropped the handle without calling `release()`. This does NOT
        // call `release_heartbeat` (Drop can't be async) — the lease
        // simply stops renewing and expires on its own TTL.
        if !self.released {
            if let Some(tx) = self.cancel_tx.take() {
                let _ = tx.send(());
            }
        }
    }
}

fn spawn_refresh_loop(
    client: ScruffClient,
    path: String,
    options: LeaseOptions,
    mut cancel_rx: oneshot::Receiver<()>,
) -> JoinHandle<()> {
    tokio::spawn(async move {
        if let Some(pid) = options.pid {
            // The OS drops this the instant `pid` dies — no refresh loop
            // needed. Still wait on cancellation so `release()` can join
            // this task deterministically.
            let _ = client.heartbeat(Some(&path), Some(pid)).await;
            let _ = cancel_rx.await;
            return;
        }

        let interval = options.refresh_interval.unwrap_or(DEFAULT_REFRESH_INTERVAL);
        let _ = client.heartbeat(Some(&path), None).await; // take it now
        let mut ticker = tokio::time::interval(interval);
        ticker.tick().await; // the first tick fires immediately; skip it, we already took the lease above
        loop {
            tokio::select! {
                _ = ticker.tick() => {
                    // Best-effort refresh; a miss self-heals on the next tick.
                    let _ = client.heartbeat(Some(&path), None).await;
                }
                _ = &mut cancel_rx => break,
            }
        }
    })
}

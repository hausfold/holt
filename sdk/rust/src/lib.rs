//! A thin Rust client over the `holt` binary — the worktree-lifecycle
//! substrate for parallel coding agents. holt stays a binary; this crate
//! shells out to it (`tokio::process` + `--json`, `watch --json` for a live
//! NDJSON stream) rather than talking to a daemon, because there isn't one
//! (SPEC.md §14.1).
//!
//! ```no_run
//! use futures_util::StreamExt;
//!
//! #[tokio::main]
//! async fn main() -> Result<(), holt::HoltError> {
//!     let client = holt::HoltClient::default();
//!
//!     let envelope = client.list().await?;
//!     for lane in &envelope.lanes {
//!         // occupied/dirty are `Option<bool>` — `None` means "not
//!         // determined", never coerce it to `false` (SPEC.md §2.2's
//!         // whole nullable-discipline point).
//!         println!("{} {} {:?}", lane.name, lane.state, lane.occupied);
//!     }
//!
//!     let mut lines = Box::pin(client.watch());
//!     while let Some(line) = lines.next().await {
//!         if let Ok(line) = line {
//!             if line.kind == holt::watch_kind::CREATED {
//!                 println!("new lane: {:?}", line.lane.map(|l| l.name));
//!             }
//!         }
//!     }
//!     Ok(())
//! }
//! ```

mod client;
mod errors;
mod exec;
mod lease;
mod types;
mod watch;

pub use client::HoltClient;
pub use errors::HoltError;
pub use lease::{Lease, LeaseOptions};
pub use types::{
    exit_code, landed_verdict, landed_via, lane_state, watch_kind, Envelope, Landed, Lane,
    PostMergeAhead, WatchEvent, WatchLine,
};

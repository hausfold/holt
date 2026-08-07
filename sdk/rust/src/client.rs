use std::collections::HashMap;
use std::path::PathBuf;

use futures_core::Stream;

use crate::errors::HoltError;
use crate::exec::{run, run_interactive, run_json};
use crate::lease::{Lease, LeaseOptions};
use crate::types::{Envelope, WatchLine};
use crate::watch;

/// A thin client over the `holt` binary. Every method shells out — there is
/// no daemon, no port, no socket (SPEC.md §14.1) — so [`HoltClient::default`]
/// is a complete, usable client:
///
/// ```no_run
/// # async fn go() -> Result<(), holt::HoltError> {
/// let client = holt::HoltClient::default();
/// let envelope = client.list().await?;
/// # let _ = envelope;
/// # Ok(())
/// # }
/// ```
///
/// Two methods (`new_interactive`, `resume_interactive`) inherit the calling
/// process's stdio and can hand off the terminal to a coding agent; every
/// other method captures output and returns. Mixing them up matters — see
/// each method's doc comment. `HoltClient` holds nothing but the options
/// below, so it's cheap to clone or construct as often as you like; every
/// call is a fresh subprocess, so a shared `&HoltClient` is safe to use
/// concurrently.
#[derive(Debug, Clone, Default)]
pub struct HoltClient {
    /// Path to the holt binary, or a bare name resolved on `PATH`. `None`
    /// means `"holt"`.
    pub bin: Option<String>,
    /// Working directory every command runs from — most of holt's commands
    /// are cwd-sensitive (`new`, `park`, a bare `holt <name>`). `None` means
    /// this process's own cwd.
    pub cwd: Option<PathBuf>,
    /// Extra environment variables, merged over (and overriding) the
    /// current process's environment — useful for `HOLT_AGENT`,
    /// `HOLT_OCCUPANCY=lease`.
    pub env: HashMap<String, String>,
}

impl HoltClient {
    /// Equivalent to [`HoltClient::default`].
    pub fn new() -> Self {
        Self::default()
    }

    /// `holt --json` / `holt list --json` — byte-identical (SPEC.md §2.2).
    /// The full snapshot: every live/parked lane, across every repo holt
    /// knows about. Poll this for landedness and PR state; use
    /// [`HoltClient::watch`] for everything else, since it's push rather
    /// than poll.
    pub async fn list(&self) -> Result<Envelope, HoltError> {
        run_json(self, &["--json"]).await
    }

    /// `holt watch --json` as a stream of typed lines — a `hello`, then a
    /// `sync` burst for every lane already alive, `ready`, then live
    /// changes for as long as you keep polling it. Drop the stream to kill
    /// the underlying process.
    ///
    /// This is the primitive `onOpen`/`onParked`/… callback-style APIs are
    /// built from (SPEC.md §14.2) — see [`HoltClient::watch_lane`] for a
    /// version scoped to one lane's `path`.
    ///
    /// ```no_run
    /// use futures_util::StreamExt;
    ///
    /// # async fn go(client: holt::HoltClient) {
    /// let mut lines = Box::pin(client.watch());
    /// while let Some(line) = lines.next().await {
    ///     if let Ok(line) = line {
    ///         if line.kind == holt::watch_kind::CREATED {
    ///             println!("new lane: {:?}", line.lane.map(|l| l.name));
    ///         }
    ///     }
    /// }
    /// # }
    /// ```
    pub fn watch(&self) -> impl Stream<Item = Result<WatchLine, HoltError>> + Send + 'static {
        watch::watch_all(self.clone())
    }

    /// [`HoltClient::watch`], filtered to events about one lane
    /// (`lane.path`) and stripped of `hello`/`ready` framing — the shape an
    /// embedder holding one session per lane usually wants: "tell me when
    /// THIS lane's state changes." Compare full paths, not names: names
    /// aren't unique across repos, but a checkout path is the registry's
    /// own primary key (SPEC.md §2.1).
    pub fn watch_lane(
        &self,
        path: impl Into<String>,
    ) -> impl Stream<Item = Result<WatchLine, HoltError>> + Send + 'static {
        watch::watch_lane(self.clone(), path.into())
    }

    /// `holt child <repo> [name]` — a lane on ANOTHER repo, registered as a
    /// child of `cwd`. Prints only the new checkout's path on stdout
    /// (SPEC.md §2.3's "only the path" discipline extends here too) and
    /// never execs a client, which is what makes it the right primitive for
    /// an orchestrator: create the lane, then run your OWN agent process
    /// against the path it returns.
    pub async fn child(&self, repo_path: &str, name: Option<&str>) -> Result<String, HoltError> {
        let mut args = vec!["child", repo_path];
        if let Some(name) = name {
            args.push(name);
        }
        let result = run(self, &args).await?;
        Ok(result.stdout.trim().to_string())
    }

    /// `holt spawn <repo> <name> [agent]` — a named lane for a caller with
    /// no pane of its own (a scheduler, a web backend). Like
    /// [`HoltClient::child`], only ever creates the lane and prints its
    /// path; never execs.
    pub async fn spawn(
        &self,
        repo_path: &str,
        name: &str,
        agent: Option<&str>,
    ) -> Result<String, HoltError> {
        let mut args = vec!["spawn", repo_path, name];
        if let Some(agent) = agent {
            args.push(agent);
        }
        let result = run(self, &args).await?;
        Ok(result.stdout.trim().to_string())
    }

    /// `holt <name>` / `holt resume <name>` with stdout captured rather
    /// than a terminal — which means the Go binary's own TTY check
    /// (`ui.IsTTY`) sees a pipe and, by design, never execs a client. It
    /// rebuilds the checkout if needed and returns the human-readable
    /// result: either confirmation it's ready, or the exact command to
    /// reopen the agent's chat by hand. Safe to call from a server process.
    /// For a TUI that wants to actually hand off the terminal, use
    /// [`HoltClient::resume_interactive`] instead.
    pub async fn resume(&self, name: &str) -> Result<String, HoltError> {
        let result = run(self, &["resume", name]).await?;
        Ok(result.stdout)
    }

    /// `holt park [label]` — commits the working tree as one `wip:` commit
    /// on the current branch. Never touches the shared stash stack
    /// (README's "park, not git stash" section) — this is the one safe way
    /// for concurrent lanes to set work aside.
    pub async fn park(&self, label: Option<&str>) -> Result<(), HoltError> {
        let mut args = vec!["park"];
        if let Some(label) = label {
            args.push(label);
        }
        run(self, &args).await?;
        Ok(())
    }

    /// `holt unpark` — reverses the most recent [`HoltClient::park`],
    /// putting its changes back uncommitted. Returns a [`HoltError`] with
    /// [`HoltError::refused`] true if that commit is already pushed (holt
    /// will not rewrite published history) or HEAD isn't a parked commit.
    pub async fn unpark(&self) -> Result<(), HoltError> {
        run(self, &["unpark"]).await?;
        Ok(())
    }

    /// `holt reap` — sweeps every LANDED lane nobody is standing in
    /// (occupied, per [`HoltClient::heartbeat`]/`lsof`, always wins). Never
    /// removes the checkout holt is being run from, and never removes a
    /// stray.
    pub async fn reap(&self) -> Result<(), HoltError> {
        run(self, &["reap"]).await?;
        Ok(())
    }

    /// `holt reship [name]` — pushes a branch that outran its
    /// already-merged PR, and opens the follow-up. Returns a [`HoltError`]
    /// with [`HoltError::degraded`] true if `gh` itself is unavailable.
    pub async fn reship(&self, name: Option<&str>) -> Result<(), HoltError> {
        let mut args = vec!["reship"];
        if let Some(name) = name {
            args.push(name);
        }
        run(self, &args).await?;
        Ok(())
    }

    /// `holt heartbeat [path] [--pid N]` — takes or refreshes the occupancy
    /// lease on a checkout (SPEC.md §9.1, §14.2). This is the seam built
    /// for exactly this SDK: a program embedding holt has no pane and no
    /// shell cwd'd anywhere, so the lease is the only way [`HoltClient::reap`]
    /// learns a checkout is in use. A lease can only SAVE a lane from the
    /// sweep, never condemn one — see [`HoltClient::lease`] for a
    /// self-refreshing wrapper instead of calling this on a timer yourself.
    /// `path: None` uses `cwd`; `pid: None` omits `--pid`.
    pub async fn heartbeat(&self, path: Option<&str>, pid: Option<u32>) -> Result<(), HoltError> {
        let mut args = vec!["heartbeat"];
        if let Some(path) = path {
            args.push(path);
        }
        let pid_str;
        if let Some(pid) = pid {
            pid_str = pid.to_string();
            args.push("--pid");
            args.push(&pid_str);
        }
        run(self, &args).await?;
        Ok(())
    }

    /// Drops the lease taken by [`HoltClient::heartbeat`].
    pub async fn release_heartbeat(&self, path: Option<&str>) -> Result<(), HoltError> {
        let mut args = vec!["heartbeat"];
        if let Some(path) = path {
            args.push(path);
        }
        args.push("--release");
        run(self, &args).await?;
        Ok(())
    }

    /// Holds an occupancy lease for as long as the returned handle is open,
    /// refreshing it on a background task on an interval comfortably under
    /// the 90s TTL (`internal/occupancy.TTL`) that applies when there's no
    /// pid to watch. This is the primitive an embedder's "session" (a
    /// connection, not a cwd — SPEC.md §14.2) should hold from connect to
    /// disconnect:
    ///
    /// ```no_run
    /// # async fn go(client: holt::HoltClient, lane_dir: &str) -> Result<(), holt::HoltError> {
    /// let mut lease = client.lease(lane_dir, holt::LeaseOptions::default());
    /// // ... serve the session ...
    /// lease.release().await?;
    /// # Ok(())
    /// # }
    /// ```
    ///
    /// Pass `LeaseOptions { pid: Some(pid), .. }` instead when the lease
    /// should track a real local process — the kernel then releases it the
    /// instant that pid dies, with no refresh loop needed at all, and
    /// `refresh_interval` is ignored.
    pub fn lease(&self, path: impl Into<String>, options: LeaseOptions) -> Lease {
        Lease::new(self.clone(), path.into(), options)
    }

    /// `holt new [name] [agent]` with stdio INHERITED from the calling
    /// process. holt execs the configured agent client unconditionally
    /// here (unlike `resume`, `new` doesn't check for a TTY) — appropriate
    /// for a real terminal app (a TUI) that wants to hand off the screen
    /// and get control back when the agent session ends, and WRONG for a
    /// server: it will block until the agent process exits, with your
    /// stdio attached to whatever the agent expects.
    pub async fn new_interactive(
        &self,
        name: Option<&str>,
        agent: Option<&str>,
    ) -> Result<(), HoltError> {
        let mut args = vec!["new"];
        if let Some(name) = name {
            args.push(name);
        }
        if let Some(agent) = agent {
            args.push(agent);
        }
        run_interactive(self, &args).await
    }

    /// `holt resume <name>` / `holt <name>` with stdio INHERITED, so a real
    /// terminal's TTY check passes and holt hands off the screen to the
    /// agent client. Same caveat as [`HoltClient::new_interactive`]: blocks
    /// until that session ends.
    pub async fn resume_interactive(&self, name: &str) -> Result<(), HoltError> {
        run_interactive(self, &["resume", name]).await
    }
}

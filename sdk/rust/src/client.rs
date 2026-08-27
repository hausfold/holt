use std::collections::HashMap;
use std::path::PathBuf;

use futures_core::Stream;

use crate::errors::ScruffError;
use crate::exec::{run, run_interactive, run_json};
use crate::lease::{Lease, LeaseOptions};
use crate::types::{Envelope, WatchEvent, WatchLine};
use crate::watch;

/// A thin client over the `scruff` binary. Every method shells out — there is
/// no daemon, no port, no socket (SPEC.md §14.1) — so [`ScruffClient::default`]
/// is a complete, usable client:
///
/// ```no_run
/// # async fn go() -> Result<(), scruff::ScruffError> {
/// let client = scruff::ScruffClient::default();
/// let envelope = client.list().await?;
/// # let _ = envelope;
/// # Ok(())
/// # }
/// ```
///
/// Two methods (`new_interactive`, `resume_interactive`) inherit the calling
/// process's stdio and can hand off the terminal to a coding agent; every
/// other method captures output and returns. Mixing them up matters — see
/// each method's doc comment. `ScruffClient` holds nothing but the options
/// below, so it's cheap to clone or construct as often as you like; every
/// call is a fresh subprocess, so a shared `&ScruffClient` is safe to use
/// concurrently.
#[derive(Debug, Clone, Default)]
pub struct ScruffClient {
    /// Path to the scruff binary, or a bare name resolved on `PATH`. `None`
    /// means `"scruff"`.
    pub bin: Option<String>,
    /// Working directory every command runs from — most of scruff's commands
    /// are cwd-sensitive (`new`, `park`, a bare `scruff <name>`). `None` means
    /// this process's own cwd.
    pub cwd: Option<PathBuf>,
    /// Extra environment variables, merged over (and overriding) the
    /// current process's environment — useful for `SCRUFF_AGENT`,
    /// `SCRUFF_OCCUPANCY=lease`.
    pub env: HashMap<String, String>,
}

impl ScruffClient {
    /// Equivalent to [`ScruffClient::default`].
    pub fn new() -> Self {
        Self::default()
    }

    /// `scruff --json` / `scruff list --json` — byte-identical (SPEC.md §2.2).
    /// The full snapshot: every live/parked lane, across every repo scruff
    /// knows about. Poll this for landedness and PR state; use
    /// [`ScruffClient::watch`] for everything else, since it's push rather
    /// than poll.
    pub async fn list(&self) -> Result<Envelope, ScruffError> {
        run_json(self, &["--json"]).await
    }

    /// `scruff watch --json` as a stream of typed lines — a `hello`, then a
    /// `sync` burst for every lane already alive, `ready`, then live
    /// changes for as long as you keep polling it. Drop the stream to kill
    /// the underlying process.
    ///
    /// This is the primitive `onOpen`/`onParked`/… callback-style APIs are
    /// built from (SPEC.md §14.2) — see [`ScruffClient::watch_lane`] for a
    /// version scoped to one lane's `path`.
    ///
    /// ```no_run
    /// use futures_util::StreamExt;
    ///
    /// # async fn go(client: scruff::ScruffClient) {
    /// let mut lines = Box::pin(client.watch());
    /// while let Some(line) = lines.next().await {
    ///     if let Ok(line) = line {
    ///         if line.kind == scruff::watch_kind::CREATED {
    ///             println!("new lane: {:?}", line.lane.map(|l| l.name));
    ///         }
    ///     }
    /// }
    /// # }
    /// ```
    pub fn watch(&self) -> impl Stream<Item = Result<WatchLine, ScruffError>> + Send + 'static {
        watch::watch_all(self.clone())
    }

    /// [`ScruffClient::watch`], filtered to events about one lane
    /// (`lane.path`) and stripped of `hello`/`ready` framing — the shape an
    /// embedder holding one session per lane usually wants: "tell me when
    /// THIS lane's state changes." Compare full paths, not names: names
    /// aren't unique across repos, but a checkout path is the registry's
    /// own primary key (SPEC.md §2.1).
    ///
    /// Yields [`WatchEvent`], not [`WatchLine`]: this stream has already
    /// dropped the `hello` header, so the header-only fields can't be
    /// populated and aren't in the type. Same contract as `watchLane` in
    /// the TS/Python/Swift SDKs.
    pub fn watch_lane(
        &self,
        path: impl Into<String>,
    ) -> impl Stream<Item = Result<WatchEvent, ScruffError>> + Send + 'static {
        watch::watch_lane(self.clone(), path.into())
    }

    /// `scruff child <repo> [name]` — a lane on ANOTHER repo, registered as a
    /// child of `cwd`. Prints only the new checkout's path on stdout
    /// (SPEC.md §2.3's "only the path" discipline extends here too) and
    /// never execs a client, which is what makes it the right primitive for
    /// an orchestrator: create the lane, then run your OWN agent process
    /// against the path it returns.
    pub async fn child(&self, repo_path: &str, name: Option<&str>) -> Result<String, ScruffError> {
        let mut args = vec!["child", repo_path];
        if let Some(name) = name {
            args.push(name);
        }
        let result = run(self, &args).await?;
        Ok(result.stdout.trim().to_string())
    }

    /// `scruff spawn <repo> <name> [agent]` — a named lane for a caller with
    /// no pane of its own (a scheduler, a web backend). Like
    /// [`ScruffClient::child`], only ever creates the lane and prints its
    /// path; never execs.
    pub async fn spawn(
        &self,
        repo_path: &str,
        name: &str,
        agent: Option<&str>,
    ) -> Result<String, ScruffError> {
        let mut args = vec!["spawn", repo_path, name];
        if let Some(agent) = agent {
            args.push(agent);
        }
        let result = run(self, &args).await?;
        Ok(result.stdout.trim().to_string())
    }

    /// `scruff <name>` / `scruff resume <name>` with stdout captured rather
    /// than a terminal — which means the Go binary's own TTY check
    /// (`ui.IsTTY`) sees a pipe and, by design, never execs a client. It
    /// rebuilds the checkout if needed and returns the human-readable
    /// result: either confirmation it's ready, or the exact command to
    /// reopen the agent's chat by hand. Safe to call from a server process.
    /// For a TUI that wants to actually hand off the terminal, use
    /// [`ScruffClient::resume_interactive`] instead.
    pub async fn resume(&self, name: &str) -> Result<String, ScruffError> {
        let result = run(self, &["resume", name]).await?;
        Ok(result.stdout)
    }

    /// `scruff park [label]` — commits the working tree as one `wip:` commit
    /// on the current branch. Never touches the shared stash stack
    /// (README's "park, not git stash" section) — this is the one safe way
    /// for concurrent lanes to set work aside.
    pub async fn park(&self, label: Option<&str>) -> Result<(), ScruffError> {
        let mut args = vec!["park"];
        if let Some(label) = label {
            args.push(label);
        }
        run(self, &args).await?;
        Ok(())
    }

    /// `scruff unpark` — reverses the most recent [`ScruffClient::park`],
    /// putting its changes back uncommitted. Returns a [`ScruffError`] with
    /// [`ScruffError::refused`] true if that commit is already pushed (scruff
    /// will not rewrite published history) or HEAD isn't a parked commit.
    pub async fn unpark(&self) -> Result<(), ScruffError> {
        run(self, &["unpark"]).await?;
        Ok(())
    }

    /// `scruff reap` — sweeps every LANDED lane nobody is standing in
    /// (occupied, per [`ScruffClient::heartbeat`]/`lsof`, always wins). Never
    /// removes the checkout scruff is being run from, and never removes a
    /// stray.
    pub async fn reap(&self) -> Result<(), ScruffError> {
        run(self, &["reap"]).await?;
        Ok(())
    }

    /// `scruff reship [name]` — pushes a branch that outran its
    /// already-merged PR, and opens the follow-up. Returns a [`ScruffError`]
    /// with [`ScruffError::degraded`] true if `gh` itself is unavailable.
    pub async fn reship(&self, name: Option<&str>) -> Result<(), ScruffError> {
        let mut args = vec!["reship"];
        if let Some(name) = name {
            args.push(name);
        }
        run(self, &args).await?;
        Ok(())
    }

    /// `scruff heartbeat [path] [--pid N]` — takes or refreshes the occupancy
    /// lease on a checkout (SPEC.md §9.1, §14.2). This is the seam built
    /// for exactly this SDK: a program embedding scruff has no pane and no
    /// shell cwd'd anywhere, so the lease is the only way [`ScruffClient::reap`]
    /// learns a checkout is in use. A lease can only SAVE a lane from the
    /// sweep, never condemn one — see [`ScruffClient::lease`] for a
    /// self-refreshing wrapper instead of calling this on a timer yourself.
    /// `path: None` uses `cwd`; `pid: None` omits `--pid`.
    pub async fn heartbeat(&self, path: Option<&str>, pid: Option<u32>) -> Result<(), ScruffError> {
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

    /// Drops the lease taken by [`ScruffClient::heartbeat`].
    pub async fn release_heartbeat(&self, path: Option<&str>) -> Result<(), ScruffError> {
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
    /// # async fn go(client: scruff::ScruffClient, lane_dir: &str) -> Result<(), scruff::ScruffError> {
    /// let mut lease = client.lease(lane_dir, scruff::LeaseOptions::default());
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

    /// `scruff new [name] --open [agent]` with stdio INHERITED from the calling
    /// process. scruff execs the configured agent client unconditionally
    /// here (unlike `resume`, `new` doesn't check for a TTY) — appropriate
    /// for a real terminal app (a TUI) that wants to hand off the screen
    /// and get control back when the agent session ends, and WRONG for a
    /// server: it will block until the agent process exits, with your
    /// stdio attached to whatever the agent expects.
    pub async fn new_interactive(
        &self,
        name: Option<&str>,
        agent: Option<&str>,
    ) -> Result<(), ScruffError> {
        let mut args = vec!["new"];
        if let Some(name) = name {
            args.push(name);
        }
        // --open is explicit: bare `scruff new` only prints the lane's path.
        args.push("--open");
        if let Some(agent) = agent {
            args.push(agent);
        }
        run_interactive(self, &args).await
    }

    /// `scruff resume <name>` / `scruff <name>` with stdio INHERITED, so a real
    /// terminal's TTY check passes and scruff hands off the screen to the
    /// agent client. Same caveat as [`ScruffClient::new_interactive`]: blocks
    /// until that session ends.
    pub async fn resume_interactive(&self, name: &str) -> Result<(), ScruffError> {
        run_interactive(self, &["resume", name]).await
    }
}

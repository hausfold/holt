import Foundation

/// Options for constructing a `HoltClient`.
public struct HoltClientOptions: Sendable {
    /// Path to the holt binary, or a bare name resolved on `PATH`. Defaults
    /// to `"holt"`.
    public var bin: String?
    /// Working directory every command runs from — most of holt's commands
    /// are cwd-sensitive (`new`, `park`, a bare `holt <name>`). Defaults to
    /// the SDK process's own cwd.
    public var cwd: String?
    /// Extra environment variables, merged over the current process's env.
    /// Useful for `HOLT_AGENT`, `HOLT_OCCUPANCY=lease`.
    public var env: [String: String?]?

    public init(bin: String? = nil, cwd: String? = nil, env: [String: String?]? = nil) {
        self.bin = bin
        self.cwd = cwd
        self.env = env
    }
}

/// A thin client over the `holt` binary. Every method shells out — there
/// is no daemon, no port, no socket (SPEC.md §14.1) — so this is a value
/// type holding nothing but the options each call needs, cheap to
/// construct as often as you like.
///
/// Two methods (`newInteractive`, `resumeInteractive`) inherit the calling
/// process's stdio and can hand off the terminal to a coding agent; every
/// other method captures output and returns. Mixing them up matters: see
/// each method's doc comment.
public struct HoltClient: Sendable {
    let opts: RunOptions

    public init(options: HoltClientOptions = HoltClientOptions()) {
        self.opts = RunOptions(bin: options.bin, cwd: options.cwd, env: options.env)
    }

    /// `holt --json` / `holt list --json` — byte-identical (SPEC.md §2.2).
    /// The full snapshot: every live/parked lane, across every repo holt
    /// knows about. Poll this for landedness and PR state; use `watch()`
    /// for everything else, since it's push rather than poll.
    public func list() async throws -> HoltEnvelope {
        try await runJSON(["--json"], options: opts)
    }

    /// `holt watch --json` as an `AsyncThrowingStream` of typed lines — a
    /// `.hello`, then a `.sync` burst for every lane already alive,
    /// `.ready`, then live changes for as long as you keep iterating. Stop
    /// iterating (`break`, or let the `for try await` loop's task be
    /// cancelled) to kill the underlying process.
    ///
    /// This is the primitive onOpen/onParked/…-style callback APIs are
    /// built from (SPEC.md §14.2) — see the free function `watchLane` for
    /// a version scoped to one lane's `path`.
    ///
    /// ```swift
    /// for try await line in holt.watch() {
    ///     if case .event(let event) = line, event.kind == .created {
    ///         print("new lane:", event.lane?.name ?? "?")
    ///     }
    /// }
    /// ```
    public func watch() -> AsyncThrowingStream<WatchLine, Error> {
        watchAll(options: opts)
    }

    /// `holt child <repo> [name]` — a lane on ANOTHER repo, registered as a
    /// child of `cwd`. Prints only the new checkout's path on stdout
    /// (SPEC.md §2.3's "only the path" discipline extends here too) and
    /// never execs a client, which is what makes it the right primitive
    /// for an orchestrator: create the lane, then run your OWN agent
    /// process against the path it returns.
    public func child(_ repoPath: String, name: String? = nil) async throws -> String {
        var args = ["child", repoPath]
        if let name { args.append(name) }
        let result = try await run(args, options: opts)
        return result.stdout.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// `holt spawn <repo> <name> [agent]` — a named lane for a caller with
    /// no pane of its own (a scheduler, a web backend). Like `child`, only
    /// ever creates the lane and prints its path; never execs.
    public func spawn(_ repoPath: String, name: String, agent: String? = nil) async throws -> String {
        var args = ["spawn", repoPath, name]
        if let agent { args.append(agent) }
        let result = try await run(args, options: opts)
        return result.stdout.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// `holt <name>` / `holt resume <name>` with stdout captured rather
    /// than a terminal — which means the Go binary's own TTY check
    /// (`ui.IsTTY`) sees a pipe and, by design, never execs a client. It
    /// rebuilds the checkout if needed and returns the human-readable
    /// result: either confirmation it's ready, or the exact command to
    /// reopen the agent's chat by hand. Safe to call from a server
    /// process. For a TUI that wants to actually hand off the terminal,
    /// use `resumeInteractive` instead.
    public func resume(_ name: String) async throws -> String {
        try await run(["resume", name], options: opts).stdout
    }

    /// `holt park [label]` — commits the working tree as one `wip:` commit
    /// on the current branch. Never touches the shared stash stack
    /// (README's "park, not git stash" section) — this is the one safe
    /// way for concurrent lanes to set work aside.
    public func park(label: String? = nil) async throws {
        var args = ["park"]
        if let label { args.append(label) }
        _ = try await run(args, options: opts)
    }

    /// `holt unpark` — reverses the most recent `park`, putting its
    /// changes back uncommitted. Throws `HoltError` with `.refused == true`
    /// if that commit is already pushed (holt will not rewrite published
    /// history) or HEAD isn't a parked commit.
    public func unpark() async throws {
        _ = try await run(["unpark"], options: opts)
    }

    /// `holt reap` — sweeps every LANDED lane nobody is standing in
    /// (occupied, per `heartbeat`/`lsof`, always wins). Never removes the
    /// checkout holt is being run from, and never removes a stray.
    public func reap() async throws {
        _ = try await run(["reap"], options: opts)
    }

    /// `holt reship [name]` — pushes a branch that outran its already-
    /// merged PR, and opens the follow-up. Throws with `.degraded == true`
    /// if `gh` itself is unavailable.
    public func reship(_ name: String? = nil) async throws {
        var args = ["reship"]
        if let name { args.append(name) }
        _ = try await run(args, options: opts)
    }

    /// `holt heartbeat [path] [--pid N]` — takes or refreshes the
    /// occupancy lease on a checkout (SPEC.md §9.1, §14.2). This is the
    /// seam built for exactly this SDK: a program embedding holt has no
    /// pane and no shell cwd'd anywhere, so the lease is the only way
    /// `reap` learns a checkout is in use. A lease can only SAVE a lane
    /// from the sweep, never condemn one — see `lease` for a self-
    /// refreshing wrapper instead of calling this on a timer yourself.
    public func heartbeat(path: String? = nil, pid: Int32? = nil) async throws {
        var args = ["heartbeat"]
        if let path { args.append(path) }
        if let pid { args += ["--pid", String(pid)] }
        _ = try await run(args, options: opts)
    }

    /// Drops the lease taken by `heartbeat`.
    public func releaseHeartbeat(path: String? = nil) async throws {
        var args = ["heartbeat"]
        if let path { args.append(path) }
        args.append("--release")
        _ = try await run(args, options: opts)
    }

    /// Takes an occupancy lease and holds it for as long as the returned
    /// `Lease` is alive, refreshing it on an interval comfortably under
    /// the 90s TTL (`internal/occupancy.TTL`) that applies when there's no
    /// pid to watch. This is the primitive an embedder's "session" (a
    /// connection, not a cwd — SPEC.md §14.2) should hold from connect to
    /// disconnect:
    ///
    /// ```swift
    /// let lease = try await holt.lease(path: laneDir)
    /// // ... serve the session ...
    /// await lease.release()
    /// ```
    ///
    /// Pass `pid:` instead when the lease should track a real local
    /// process — the kernel then releases it the instant that pid dies,
    /// with no refresh loop needed at all, and `refreshInterval` is
    /// ignored.
    ///
    /// This is a throwing `async` factory rather than a plain
    /// initializer (unlike the TS SDK's constructor-based `lease()`):
    /// Swift can await the first heartbeat before returning, so a failure
    /// to take the lease is raised here rather than surfacing on the next
    /// refresh or release call — same tradeoff the Python SDK made.
    public func lease(path: String, pid: Int32? = nil, refreshInterval: Duration = .seconds(60)) async throws -> Lease {
        try await heartbeat(path: path, pid: pid)
        return Lease(client: self, path: path, pid: pid, refreshInterval: refreshInterval)
    }

    /// `holt new [name] [agent]` with stdio INHERITED from the calling
    /// process. holt execs the configured agent client unconditionally
    /// here (unlike `resume`, `new` doesn't check for a TTY) — appropriate
    /// for a real terminal app (a TUI) that wants to hand off the screen
    /// and get control back when the agent session ends, and WRONG for a
    /// server: it will block until the agent process exits, with your
    /// stdio attached to whatever the agent expects.
    public func newInteractive(_ name: String? = nil, agent: String? = nil) async throws {
        var args = ["new"]
        if let name { args.append(name) }
        if let agent { args.append(agent) }
        try await runInteractive(args, options: opts)
    }

    /// `holt resume <name>` / `holt <name>` with stdio INHERITED, so a
    /// real terminal's TTY check passes and holt hands off the screen to
    /// the agent client. Same caveat as `newInteractive`: blocks until
    /// that session ends.
    public func resumeInteractive(_ name: String) async throws {
        try await runInteractive(["resume", name], options: opts)
    }
}

/// Same shape as `run`, but with stdio inherited from the calling process
/// instead of captured — see `HoltClient.newInteractive`/
/// `resumeInteractive`.
private func runInteractive(_ args: [String], options: RunOptions) async throws {
    let bin = options.bin ?? "holt"
    let process = Process()
    process.executableURL = URL(fileURLWithPath: "/usr/bin/env")
    process.arguments = [bin] + args
    if let cwd = options.cwd {
        process.currentDirectoryURL = URL(fileURLWithPath: cwd)
    }
    if let env = mergedEnvironment(options.env) {
        process.environment = env
    }
    process.standardInput = FileHandle.standardInput
    process.standardOutput = FileHandle.standardOutput
    process.standardError = FileHandle.standardError

    let code: Int32 = try await withCheckedThrowingContinuation { continuation in
        process.terminationHandler = { p in
            continuation.resume(returning: p.terminationStatus)
        }
        do {
            try process.run()
        } catch {
            continuation.resume(throwing: error)
        }
    }
    if code != 0 {
        throw HoltError(code: code, stderr: "", command: [bin] + args)
    }
}

/// A held occupancy lease. See `HoltClient.lease`.
public actor Lease {
    private let client: HoltClient
    private let path: String
    private var released = false
    private var refreshTask: Task<Void, Never>?

    init(client: HoltClient, path: String, pid: Int32?, refreshInterval: Duration) {
        self.client = client
        self.path = path
        if pid == nil {
            // < the 90s TTL, with margin. Tracks no real process, so
            // nothing else will ever refresh this — the loop is the whole
            // point.
            refreshTask = Task {
                while !Task.isCancelled {
                    try? await Task.sleep(for: refreshInterval)
                    if Task.isCancelled { break }
                    // Best-effort refresh; a miss self-heals on the next
                    // tick.
                    try? await client.heartbeat(path: path)
                }
            }
        }
    }

    /// Drops the lease and stops refreshing it. Safe to call more than
    /// once.
    public func release() async {
        guard !released else { return }
        released = true
        refreshTask?.cancel()
        try? await client.releaseHeartbeat(path: path)
    }
}

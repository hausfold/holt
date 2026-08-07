import Foundation

/// Options threaded through to every subprocess call.
public struct RunOptions: Sendable {
    /// Path to the holt binary, or a bare name resolved on `PATH`. Defaults
    /// to `"holt"`.
    public var bin: String?
    /// Working directory every command runs from — most of holt's commands
    /// are cwd-sensitive (`new`, `park`, a bare `holt <name>`). Defaults to
    /// this process's own cwd.
    public var cwd: String?
    /// Extra environment variables, merged over the current process's env.
    /// Useful for `HOLT_AGENT`, `HOLT_OCCUPANCY=lease`. A value of `nil`
    /// unsets the key rather than passing the literal string "nil" to the
    /// child.
    public var env: [String: String?]?
    /// Piped to the child's stdin, then the stream is closed. Used by
    /// `holt hook create`/`remove`, which read JSON off stdin (SPEC.md
    /// §2.3).
    public var stdin: String?

    public init(bin: String? = nil, cwd: String? = nil, env: [String: String?]? = nil, stdin: String? = nil) {
        self.bin = bin
        self.cwd = cwd
        self.env = env
        self.stdin = stdin
    }
}

public struct RunResult: Sendable {
    public let stdout: String
    public let stderr: String
    public let code: Int32
}

/// `overrides`, merged over `ProcessInfo.processInfo.environment` — `nil`
/// values remove the key rather than passing it through.
func mergedEnvironment(_ overrides: [String: String?]?) -> [String: String]? {
    guard let overrides else { return nil }
    var merged = ProcessInfo.processInfo.environment
    for (key, value) in overrides {
        if let value {
            merged[key] = value
        } else {
            merged.removeValue(forKey: key)
        }
    }
    return merged
}

/// Runs one holt invocation to completion and collects its output. Every
/// non-`--json` holt command writes human text to stdout on success — this
/// is the primitive `list()`/`watch()` build their typed parsing on top of,
/// and the one lifecycle commands (`new`, `park`, `reap`, ...) use
/// directly, surfacing stdout as a plain string.
///
/// Throws `HoltError` on a non-zero exit, carrying holt's exit code
/// (SPEC.md §2.4) rather than collapsing every failure into one shape.
public func run(_ args: [String], options: RunOptions = RunOptions()) async throws -> RunResult {
    let bin = options.bin ?? "holt"

    let process = Process()
    // Launched through `/usr/bin/env` rather than resolving `bin` against
    // `PATH` ourselves — `Process.executableURL` needs an absolute path and
    // won't search `PATH` for a bare name the way `execvp` (and therefore
    // Node's `spawn`/Python's `create_subprocess_exec`) does. `env` gives
    // the same lookup for free on both macOS and Linux, and passes an
    // already-absolute `bin` straight through unchanged.
    process.executableURL = URL(fileURLWithPath: "/usr/bin/env")
    process.arguments = [bin] + args
    if let cwd = options.cwd {
        process.currentDirectoryURL = URL(fileURLWithPath: cwd)
    }
    if let env = mergedEnvironment(options.env) {
        process.environment = env
    }

    let stdinPipe = Pipe()
    let stdoutPipe = Pipe()
    let stderrPipe = Pipe()
    process.standardInput = stdinPipe
    process.standardOutput = stdoutPipe
    process.standardError = stderrPipe

    async let stdoutData = readAll(stdoutPipe.fileHandleForReading)
    async let stderrData = readAll(stderrPipe.fileHandleForReading)

    // `terminationHandler` must be assigned before `run()` is called —
    // assigning it afterward races the child process's own exit. Doing
    // both inside one continuation closure, synchronously, is what
    // guarantees the ordering.
    let code: Int32 = try await withCheckedThrowingContinuation { continuation in
        process.terminationHandler = { p in
            continuation.resume(returning: p.terminationStatus)
        }
        do {
            try process.run()
            if let stdin = options.stdin {
                stdinPipe.fileHandleForWriting.write(Data(stdin.utf8))
            }
            try? stdinPipe.fileHandleForWriting.close()
        } catch {
            continuation.resume(throwing: error)
        }
    }

    let stdout = String(data: await stdoutData, encoding: .utf8) ?? ""
    let stderr = String(data: await stderrData, encoding: .utf8) ?? ""

    if code != 0 {
        throw HoltError(code: code, stderr: stderr, command: [bin] + args)
    }
    return RunResult(stdout: stdout, stderr: stderr, code: code)
}

/// Same as `run`, but decodes stdout as JSON — for `--json` commands only.
/// holt's own contract (README, `internal/ui`) is "stdout carries the
/// payload, every diagnostic goes to stderr", so this never has to guess
/// which lines are data.
public func runJSON<T: Decodable>(_ args: [String], options: RunOptions = RunOptions()) async throws -> T {
    let result = try await run(args, options: options)
    return try JSONDecoder().decode(T.self, from: Data(result.stdout.utf8))
}

private func readAll(_ handle: FileHandle) async -> Data {
    await Task.detached {
        (try? handle.readToEnd()) ?? Data()
    }.value
}

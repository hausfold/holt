import Foundation

/// `holt watch --json` as an `AsyncThrowingStream` of typed lines. One
/// object per NDJSON line on stdout, in order: `.hello`, a `.event(.sync)`
/// burst for every lane already alive, `.event(.ready)`, then live changes
/// for as long as the stream is consumed (SPEC.md §14.3 step 2).
///
/// The child process is killed when you stop consuming — breaking out of a
/// `for try await`, or letting the stream's task get cancelled. There is
/// no other way to stop it short: `watch` has no built-in end condition,
/// by design (SPEC.md §14).
public func watchAll(options: RunOptions = RunOptions()) -> AsyncThrowingStream<WatchLine, Error> {
    AsyncThrowingStream { continuation in
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/env")
        process.arguments = [options.bin ?? "holt", "watch", "--json"]
        if let cwd = options.cwd {
            process.currentDirectoryURL = URL(fileURLWithPath: cwd)
        }
        if let env = mergedEnvironment(options.env) {
            process.environment = env
        }

        let stdoutPipe = Pipe()
        let stderrPipe = Pipe()
        process.standardInput = FileHandle.nullDevice
        process.standardOutput = stdoutPipe
        process.standardError = stderrPipe

        let task = Task {
            do {
                try process.run()
            } catch {
                continuation.finish(throwing: error)
                return
            }

            do {
                let decoder = JSONDecoder()
                for try await line in stdoutPipe.fileHandleForReading.bytes.lines {
                    if line.isEmpty { continue }
                    let parsed = try decoder.decode(WatchLine.self, from: Data(line.utf8))
                    continuation.yield(parsed)
                }
                continuation.finish()
            } catch {
                continuation.finish(throwing: error)
            }
        }

        continuation.onTermination = { _ in
            task.cancel()
            if process.isRunning {
                process.terminate()
            }
        }
    }
}

/// `watchAll`, filtered to events about one lane (`event.lane.path`) and
/// stripped of the `.hello`/`.ready` framing that names no lane — the shape
/// an embedder holding one session per lane usually wants: "tell me when
/// THIS lane's state changes."
///
/// A `.sync` event for the lane still passes through — it is NOT framing.
/// It's how a caller that started watching after the lane went live learns
/// the lane exists at all, so a `switch` over `event.kind` needs a `.sync`
/// case.
///
/// Compare full paths, not names: names aren't unique across repos, but a
/// checkout path is the registry's own primary key (SPEC.md §2.1).
public func watchLane(path: String, options: RunOptions = RunOptions()) -> AsyncThrowingStream<WatchEvent, Error> {
    AsyncThrowingStream { continuation in
        let task = Task {
            do {
                for try await line in watchAll(options: options) {
                    guard case .event(let event) = line else { continue }
                    if event.lane?.path == path {
                        continuation.yield(event)
                    }
                }
                continuation.finish()
            } catch {
                continuation.finish(throwing: error)
            }
        }
        continuation.onTermination = { _ in task.cancel() }
    }
}

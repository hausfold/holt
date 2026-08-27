import Foundation

/// Thrown by every SDK call that shells out and gets back a non-zero exit.
/// Carries scruff's actual exit code (SPEC.md §2.4) rather than collapsing it
/// to a generic failure — `.refused` is how a caller tells "scruff declined
/// to destroy something" from "you asked wrong" (`.usage`) or "registry
/// locked" (`.locked`), and each deserves different handling (retry,
/// surface to a human, or just don't retry).
public struct ScruffError: Error, CustomStringConvertible, Sendable {
    public let code: Int32
    public let stderr: String
    public let command: [String]

    public init(code: Int32, stderr: String, command: [String]) {
        self.code = code
        self.stderr = stderr
        self.command = command
    }

    private var label: String {
        switch ScruffExitCode(rawValue: code) {
        case .usage: return "usage"
        case .refused: return "refused"
        case .degraded: return "degraded"
        case .conflict: return "conflict"
        case .locked: return "locked"
        default: return "exit \(code)"
        }
    }

    public var description: String {
        let suffix = stderr.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            ? ""
            : " — \(stderr.trimmingCharacters(in: .whitespacesAndNewlines))"
        return "scruff \(command.joined(separator: " ")): \(label)\(suffix)"
    }

    /// `true` when scruff declined for safety (occupied, dirty, or not
    /// provably landed) rather than because the call itself was wrong.
    public var refused: Bool { code == ScruffExitCode.refused.rawValue }

    /// `true` when the operation completed but a signal was unavailable
    /// (forge down, no `lsof`) — check `warnings` on the envelope for why.
    public var degraded: Bool { code == ScruffExitCode.degraded.rawValue }
}

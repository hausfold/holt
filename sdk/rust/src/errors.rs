use std::fmt;

use crate::types::exit_code;

/// Returned by every SDK call that shells out and gets back a non-zero exit.
/// Carries scruff's actual exit code (SPEC.md §2.4) rather than collapsing
/// every failure into one shape — [`ScruffError::refused`] is how a caller
/// tells "scruff declined to destroy something" from "you asked wrong"
/// (`exit_code::USAGE`) or "registry locked" (`exit_code::LOCKED`), and each
/// deserves different handling (retry, surface to a human, or just don't
/// retry).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ScruffError {
    pub code: i32,
    pub stderr: String,
    pub command: Vec<String>,
}

impl ScruffError {
    pub(crate) fn new(code: i32, stderr: impl Into<String>, command: Vec<String>) -> Self {
        Self {
            code,
            stderr: stderr.into(),
            command,
        }
    }

    /// `true` when scruff declined for safety (occupied, dirty, or not
    /// provably landed) rather than because the call itself was wrong.
    pub fn refused(&self) -> bool {
        self.code == exit_code::REFUSED
    }

    /// `true` when the operation completed but a signal was unavailable
    /// (forge down, no `lsof`) — check an [`crate::Envelope`]'s `warnings`
    /// for why.
    pub fn degraded(&self) -> bool {
        self.code == exit_code::DEGRADED
    }
}

fn exit_label(code: i32) -> String {
    match code {
        exit_code::USAGE => "usage".into(),
        exit_code::REFUSED => "refused".into(),
        exit_code::DEGRADED => "degraded".into(),
        exit_code::CONFLICT => "conflict".into(),
        exit_code::LOCKED => "locked".into(),
        other => format!("exit {other}"),
    }
}

impl fmt::Display for ScruffError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let label = exit_label(self.code);
        write!(f, "scruff {}: {label}", self.command.join(" "))?;
        if !self.stderr.trim().is_empty() {
            write!(f, " — {}", self.stderr.trim())?;
        }
        Ok(())
    }
}

impl std::error::Error for ScruffError {}

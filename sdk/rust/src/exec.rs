use std::process::Stdio;

use serde::de::DeserializeOwned;
use tokio::process::Command;

use crate::client::ScruffClient;
use crate::errors::ScruffError;
use crate::types::exit_code;

#[derive(Debug)]
pub(crate) struct RunResult {
    pub stdout: String,
}

fn command(client: &ScruffClient, args: &[&str]) -> Command {
    let mut cmd = Command::new(client.bin.as_deref().unwrap_or("scruff"));
    cmd.args(args);
    if let Some(cwd) = &client.cwd {
        cmd.current_dir(cwd);
    }
    cmd.envs(&client.env);
    cmd
}

fn full_command(client: &ScruffClient, args: &[&str]) -> Vec<String> {
    let mut command = vec![client.bin.as_deref().unwrap_or("scruff").to_string()];
    command.extend(args.iter().map(|a| a.to_string()));
    command
}

/// Runs one scruff invocation to completion and collects its output. Every
/// non-`--json` scruff command writes human text to stdout on success — this
/// is the primitive `list()`/`watch()` build their typed parsing on top of,
/// and the one lifecycle commands (`park`, `reap`, ...) use directly,
/// surfacing stdout as a plain string.
///
/// Returns [`ScruffError`] on a non-zero exit, carrying scruff's exit code
/// (SPEC.md §2.4) rather than collapsing every failure into one shape.
pub(crate) async fn run(client: &ScruffClient, args: &[&str]) -> Result<RunResult, ScruffError> {
    let mut cmd = command(client, args);
    cmd.stdin(Stdio::null());
    cmd.stdout(Stdio::piped());
    cmd.stderr(Stdio::piped());

    let output = cmd.output().await.map_err(|e| {
        // A spawn failure (bad bin, not a git repo yet) has no real exit
        // code — Usage's bucket, same convention the Go SDK uses.
        ScruffError::new(exit_code::USAGE, e.to_string(), full_command(client, args))
    })?;

    let stdout = String::from_utf8_lossy(&output.stdout).into_owned();
    let stderr = String::from_utf8_lossy(&output.stderr).into_owned();

    if !output.status.success() {
        let code = output.status.code().unwrap_or(exit_code::USAGE);
        return Err(ScruffError::new(code, stderr, full_command(client, args)));
    }
    Ok(RunResult { stdout })
}

/// Same as [`run`], but parses stdout as JSON — for `--json` commands only.
/// scruff's own contract (README, `internal/ui`) is "stdout carries the
/// payload, every diagnostic goes to stderr", so this never has to guess
/// which lines are data.
pub(crate) async fn run_json<T: DeserializeOwned>(
    client: &ScruffClient,
    args: &[&str],
) -> Result<T, ScruffError> {
    let result = run(client, args).await?;
    serde_json::from_str(&result.stdout)
        .map_err(|e| ScruffError::new(exit_code::USAGE, e.to_string(), full_command(client, args)))
}

/// Runs scruff with stdio INHERITED from the calling process — used by
/// `new_interactive`/`resume_interactive`, where scruff is expected to exec
/// an agent client and take over the real terminal.
pub(crate) async fn run_interactive(client: &ScruffClient, args: &[&str]) -> Result<(), ScruffError> {
    let mut cmd = command(client, args);
    cmd.stdin(Stdio::inherit());
    cmd.stdout(Stdio::inherit());
    cmd.stderr(Stdio::inherit());

    let status = cmd
        .status()
        .await
        .map_err(|e| ScruffError::new(exit_code::USAGE, e.to_string(), full_command(client, args)))?;

    if !status.success() {
        let code = status.code().unwrap_or(exit_code::USAGE);
        return Err(ScruffError::new(code, "", full_command(client, args)));
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    // In-crate (not tests/client_test.rs) because it drives the fixture's
    // dedicated "reap-refused" verb directly through run() — no public
    // method has an argv shape that lands on that first arg. Mirrors
    // sdk/go's internal_test.go.
    #[tokio::test]
    async fn error_mapping_non_zero_exit_carries_the_real_code() {
        let client = ScruffClient {
            bin: Some("./tests/fake-scruff.sh".to_string()),
            ..Default::default()
        };
        let err = run(&client, &["reap-refused"])
            .await
            .expect_err("want error");

        assert_eq!(err.code, exit_code::REFUSED);
        assert!(err.refused());
        assert!(!err.degraded());
        assert!(err.stderr.contains("occupied"), "stderr = {:?}", err.stderr);
    }
}

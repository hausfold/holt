import { describe, expect, test } from "bun:test";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { HoltClient } from "../src/client.js";
import { HoltError } from "../src/errors.js";
import { run } from "../src/exec.js";

const bin = path.join(path.dirname(fileURLToPath(import.meta.url)), "fake-holt.sh");
const client = () => new HoltClient({ bin });

describe("list", () => {
  test("parses the --json envelope with nullable discipline intact", async () => {
    const envelope = await client().list();
    expect(envelope.schema).toBe(1);
    expect(envelope.lanes).toHaveLength(2);

    const sparkle = envelope.lanes[0]!;
    expect(sparkle.occupied).toBe(true); // true, not undefined-coerced
    expect(sparkle.dirty).toBe(false); // false, distinct from null

    const frost = envelope.lanes[1]!;
    expect(frost.occupied).toBeNull(); // null means "not determined"
    expect(frost.dirty).toBeNull();
    expect(frost.landed.verdict).toBe("contained");
  });
});

describe("watch", () => {
  test("yields hello, sync, ready, then live changes, and stops on break", async () => {
    const kinds: string[] = [];
    for await (const line of client().watch()) {
      kinds.push(line.kind);
      if (line.kind === "created") break;
    }
    expect(kinds).toEqual(["hello", "sync", "ready", "created"]);
  });

  test("watchLane filters to one lane's events only", async () => {
    const { watchLane } = await import("../src/watch.js");
    const seen: string[] = [];
    for await (const ev of watchLane("/repo/.holt/nebelhaus/fresh", { bin })) {
      seen.push(ev.kind);
      break;
    }
    expect(seen).toEqual(["created"]);
  });
});

describe("child", () => {
  test("returns only the new checkout path", async () => {
    const dir = await client().child("/repo/other");
    expect(dir).toBe("/repo/.holt/other/new-lane");
  });
});

describe("resume", () => {
  test("captured stdout never execs — returns the reopen instructions as text", async () => {
    const out = await client().resume("sparkle");
    expect(out).toContain("claude --resume");
  });
});

describe("error mapping", () => {
  test("non-zero exit throws HoltError carrying the real exit code", async () => {
    expect.assertions(4);
    try {
      await run(["reap-refused"], { bin });
    } catch (err) {
      expect(err).toBeInstanceOf(HoltError);
      const e = err as HoltError;
      expect(e.code).toBe(2);
      expect(e.refused).toBe(true);
      expect(e.stderr).toContain("occupied");
    }
  });
});

describe("heartbeat / lease", () => {
  test("lease.release() calls heartbeat --release", async () => {
    const c = client();
    const lease = c.lease("/repo/.holt/nebelhaus/sparkle", { pid: 12345 });
    await lease.release();
    // No throw: fake-holt's heartbeat branch accepts --release silently.
  });
});

export { ScruffClient, Lease, type ScruffClientOptions } from "./client.js";
export { ScruffError } from "./errors.js";
export { watchAll, watchLane } from "./watch.js";
export { run, runJSON, type RunOptions, type RunResult } from "./exec.js";
export {
  ScruffExitCode,
  isWatchHello,
  type ScruffEnvelope,
  type ScruffLane,
  type LaneState,
  type LandedInfo,
  type LandedVerdict,
  type LandedVia,
  type PostMergeAhead,
  type WatchEvent,
  type WatchEventKind,
  type WatchHello,
  type WatchLine,
} from "./types.js";

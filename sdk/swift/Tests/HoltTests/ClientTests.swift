import XCTest
@testable import Holt

/// `fake-holt.sh` stands in for the real binary so tests don't need a Go
/// build — it's a fixture, not a spec of holt's behavior, kept in sync by
/// hand with `sdk/ts/test/fake-holt.sh` / `sdk/python/tests/fake-holt.sh`.
final class ClientTests: XCTestCase {
    private var bin: String!

    override func setUpWithError() throws {
        bin = try XCTUnwrap(Bundle.module.url(forResource: "fake-holt", withExtension: "sh")).path
    }

    private func client() -> HoltClient {
        HoltClient(options: HoltClientOptions(bin: bin))
    }

    func testListParsesTheJSONEnvelopeWithNullableDisciplineIntact() async throws {
        let envelope = try await client().list()
        XCTAssertEqual(envelope.schema, 1)
        XCTAssertEqual(envelope.lanes.count, 2)

        let sparkle = envelope.lanes[0]
        XCTAssertEqual(sparkle.occupied, true) // true, not nil-coerced
        XCTAssertEqual(sparkle.dirty, false) // false, distinct from nil

        let frost = envelope.lanes[1]
        XCTAssertNil(frost.occupied) // nil means "not determined"
        XCTAssertNil(frost.dirty)
        XCTAssertEqual(frost.landed.verdict, .contained)
    }

    func testWatchYieldsHelloSyncReadyThenLiveChangesAndStopsOnBreak() async throws {
        var kinds: [String] = []
        for try await line in client().watch() {
            kinds.append(line.kind)
            if line.kind == "created" { break }
        }
        XCTAssertEqual(kinds, ["hello", "sync", "ready", "created"])
    }

    func testWatchLaneFiltersToOneLanesEventsOnly() async throws {
        var seen: [String] = []
        for try await event in watchLane(path: "/repo/.holt/nebelhaus/fresh", options: RunOptions(bin: bin)) {
            seen.append(event.kind.rawValue)
            break
        }
        XCTAssertEqual(seen, ["created"])
    }

    func testChildReturnsOnlyTheNewCheckoutPath() async throws {
        let dir = try await client().child("/repo/other")
        XCTAssertEqual(dir, "/repo/.holt/other/new-lane")
    }

    func testResumeCapturedStdoutNeverExecsReturnsTheReopenInstructionsAsText() async throws {
        let out = try await client().resume("sparkle")
        XCTAssertTrue(out.contains("claude --resume"))
    }

    func testNonZeroExitThrowsHoltErrorCarryingTheRealExitCode() async throws {
        do {
            _ = try await Holt.run(["reap-refused"], options: RunOptions(bin: bin))
            XCTFail("expected HoltError to be thrown")
        } catch let error as HoltError {
            XCTAssertEqual(error.code, 2)
            XCTAssertTrue(error.refused)
            XCTAssertTrue(error.stderr.contains("occupied"))
        }
    }

    func testLeaseReleaseCallsHeartbeatRelease() async throws {
        let lease = try await client().lease(path: "/repo/.holt/nebelhaus/sparkle", pid: 12345)
        await lease.release()
        // No throw: fake-holt's heartbeat branch accepts --release silently.
    }
}

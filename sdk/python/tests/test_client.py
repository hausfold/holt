import os
from contextlib import aclosing

import pytest

from holt import HoltClient, HoltClientOptions, HoltError
from holt.exec import RunOptions, run
from holt.watch import watch_lane

FAKE_HOLT = os.path.join(os.path.dirname(__file__), "fake-holt.sh")


def client() -> HoltClient:
    return HoltClient(HoltClientOptions(bin=FAKE_HOLT))


async def test_list_parses_the_json_envelope_with_nullable_discipline_intact() -> None:
    envelope = await client().list()
    assert envelope.schema == 1
    assert len(envelope.lanes) == 2

    sparkle = envelope.lanes[0]
    assert sparkle.occupied is True  # True, not falsy-coerced
    assert sparkle.dirty is False  # False, distinct from None

    frost = envelope.lanes[1]
    assert frost.occupied is None  # None means "not determined"
    assert frost.dirty is None
    assert frost.landed.verdict == "contained"


async def test_watch_yields_hello_sync_ready_then_live_changes_and_stops_on_break() -> None:
    kinds = []
    async with aclosing(client().watch()) as stream:
        async for line in stream:
            kinds.append(line.kind)
            if line.kind == "created":
                break
    assert kinds == ["hello", "sync", "ready", "created"]


async def test_watch_lane_filters_to_one_lanes_events_only() -> None:
    seen = []
    async with aclosing(watch_lane("/repo/.holt/nebelhaus/fresh", RunOptions(bin=FAKE_HOLT))) as stream:
        async for ev in stream:
            seen.append(ev.kind)
            break
    assert seen == ["created"]


async def test_client_watch_lane_filters_the_same_way_on_its_own_options() -> None:
    seen = []
    async with aclosing(client().watch_lane("/repo/.holt/nebelhaus/fresh")) as stream:
        async for ev in stream:
            seen.append(ev.kind)
            break
    assert seen == ["created"]


# `sync` names a lane, so it is data, not framing — it's the only way a caller
# that attached AFTER the lane went live learns the lane exists. Pinned because
# three docstrings used to claim the opposite.
async def test_watch_lane_passes_a_lanes_sync_through() -> None:
    seen = []
    async with aclosing(client().watch_lane("/repo/.holt/nebelhaus/sparkle")) as stream:
        async for ev in stream:
            seen.append(ev.kind)
            break
    assert seen == ["sync"]


async def test_child_returns_only_the_new_checkout_path() -> None:
    directory = await client().child("/repo/other")
    assert directory == "/repo/.holt/other/new-lane"


async def test_resume_captured_stdout_never_execs() -> None:
    out = await client().resume("sparkle")
    assert "claude --resume" in out


async def test_error_mapping_nonzero_exit_raises_holt_error_carrying_the_real_exit_code() -> None:
    with pytest.raises(HoltError) as exc_info:
        await run(["reap-refused"], RunOptions(bin=FAKE_HOLT))
    err = exc_info.value
    assert err.code == 2
    assert err.refused is True
    assert "occupied" in err.stderr


async def test_lease_release_calls_heartbeat_release() -> None:
    c = client()
    lease = await c.lease("/repo/.holt/nebelhaus/sparkle", pid=12345)
    await lease.release()
    # No raise: fake-holt's heartbeat branch accepts --release silently.

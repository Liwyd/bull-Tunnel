"""The command channel: the signatures and the records."""

from __future__ import annotations

import json

from bulltunnel import cmdchannel as cm


def test_the_signature_is_the_one_the_engine_computes():
    # the same vector as the test of the engine: both sides must build the
    # very same string, or the agent refuses every command
    cmd = cm.Command(id="abc123", action="status", args={}, ts=1700000000)
    assert cmd.payload() == "abc123|status|{}|1700000000"
    assert cm.sign("the-token", cmd) == "5cc167f05e2ddbc544051150193f9f2072ef2496dd90af5758e4c3dd3e90959c"
    cmd = cm.Command(id="abc123", action="restart", args={"tunnel": "link1"}, ts=1700000000)
    assert cm.sign("the-token", cmd) == "2e7058a61d5cfa0f08bdece43ade5a7b988992d685cd3e7de0d7722031787def"


def test_a_command_is_read_back():
    cmd = cm.Command(id="abc123", action="status", args={}, ts=1700000000)
    text = cm.encode("the-token", cmd)
    got = cm.decode("the-token", text)
    assert got is not None
    assert got.id == "abc123" and got.action == "status" and got.ts == 1700000000
    # the answer of the agent
    answer = cm.decode_answer(json.dumps({"id": "abc123", "ok": True, "result": {"a": 1}}))
    assert answer and answer["ok"] is True


def test_a_command_with_another_key_is_refused():
    cmd = cm.Command(id="abc123", action="status", args={}, ts=1700000000)
    text = cm.encode("the-token", cmd)
    assert cm.decode("another-token", text) is None
    # a tampered command is refused too
    blob = json.loads(text)
    blob["action"] = "restart"
    assert cm.decode("the-token", json.dumps(blob)) is None
    # rubbish is not a command
    assert cm.decode("the-token", "not json") is None
    assert cm.decode_answer("not json") is None


def test_the_records_of_two_servers_do_not_mix():
    a = cm.command_record("s123", "example.com")
    b = cm.command_record("s456", "example.com")
    assert a != b
    assert a.endswith(".example.com")
    assert cm.answer_record("s123", "example.com") != cm.command_record("s123", "example.com")

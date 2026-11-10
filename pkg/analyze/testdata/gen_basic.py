#!/usr/bin/env python3
"""Generates basic.jsonl: a small synthetic Claude Code session.

Not a real session log. Covers: a header record, a response split over two
records sharing one message id (usage repeated), read/search/edit/bash/write,
a failed Bash and its retry, a malformed line, an unknown record type, a
20-minute idle gap, a sub-agent (Task) with sidechain records, a system
api_error, thinking (must not leak), a pr-link, and a final line with no
trailing newline. Run: python3 gen_basic.py > basic.jsonl
"""
import json
import sys

S = "11111111-2222-3333-4444-555555555555"
CWD = "/work/demo"
L = []


def add(o):
    L.append(json.dumps(o))


def rec(t, ts, uuid, **kw):
    d = {"type": t, "sessionId": S, "timestamp": ts, "uuid": uuid, "cwd": CWD,
         "gitBranch": "feat/x", "version": "2.1.0"}
    d.update(kw)
    return d


def asst(ts, uuid, mid, model, content, usage, **kw):
    return rec("assistant", ts, uuid, message={"role": "assistant", "model": model, "id": mid,
                                               "content": content, "usage": usage}, **kw)


def result(ts, uuid, blocks, **kw):
    return rec("user", ts, uuid, message={"role": "user", "content": blocks}, **kw)


def u(i, o, cr=0, cc=0):
    return {"input_tokens": i, "output_tokens": o, "cache_read_input_tokens": cr,
            "cache_creation_input_tokens": cc}


SON = "claude-sonnet-4-5"
add({"type": "permission-mode", "permissionMode": "bypassPermissions", "sessionId": S})
add(rec("user", "2026-11-01T10:00:00Z", "u1",
        message={"role": "user", "content": "Add a greeting function to hello.go and test it"}))
add(asst("2026-11-01T10:00:05Z", "a1", "msg_1", SON,
         [{"type": "text", "text": "I'll look at the code first."}], u(100, 50, 1000, 200)))
add(asst("2026-11-01T10:00:06Z", "a2", "msg_1", SON,
         [{"type": "tool_use", "id": "t1", "name": "Read", "input": {"file_path": "/work/demo/hello.go"}}],
         u(100, 50, 1000, 200)))
add(result("2026-11-01T10:00:07Z", "r1",
           [{"type": "tool_result", "tool_use_id": "t1", "content": "package main\n"}]))
L.append('{"type":"assistant", this line is malformed')
add(asst("2026-11-01T10:00:10Z", "a3", "msg_2", SON,
         [{"type": "tool_use", "id": "t2", "name": "Grep", "input": {"pattern": "func main"}}], u(10, 20)))
add(result("2026-11-01T10:00:11Z", "r2",
           [{"type": "tool_result", "tool_use_id": "t2", "content": [{"type": "text", "text": "hello.go:3:func main"}]}]))
add(asst("2026-11-01T10:00:20Z", "a4", "msg_3", SON,
         [{"type": "tool_use", "id": "t3", "name": "Edit",
           "input": {"file_path": "/work/demo/hello.go", "old_string": "a", "new_string": "b"}}], u(5, 30)))
add(result("2026-11-01T10:00:21Z", "r3", [{"type": "tool_result", "tool_use_id": "t3", "content": "ok"}]))
add(asst("2026-11-01T10:00:30Z", "a5", "msg_4", SON,
         [{"type": "tool_use", "id": "t4", "name": "Bash",
           "input": {"command": "go test ./...", "description": "Run tests"}}], u(5, 10)))
add(result("2026-11-01T10:00:40Z", "r4",
           [{"type": "tool_result", "tool_use_id": "t4", "is_error": True, "content": "FAIL: TestGreet expected hello"}]))
add(asst("2026-11-01T10:00:50Z", "a6", "msg_5", SON,
         [{"type": "tool_use", "id": "t5", "name": "Write", "input": {"file_path": "/work/demo/hello_test.go", "content": "x"}},
          {"type": "tool_use", "id": "t6", "name": "Bash", "input": {"command": "go test ./..."}}], u(5, 40)))
add(result("2026-11-01T10:01:00Z", "r5", [{"type": "tool_result", "tool_use_id": "t5", "content": "ok"},
                                          {"type": "tool_result", "tool_use_id": "t6", "content": "ok"}]))
add(asst("2026-11-01T10:01:05Z", "a7", "msg_6", SON,
         [{"type": "text", "text": "Added Greet() and a test; tests pass."}], u(5, 15)))
add({"type": "ai-title", "aiTitle": "Greeting function", "sessionId": S})
add({"type": "some-future-record", "sessionId": S, "timestamp": "2026-11-01T10:01:06Z", "foo": {"bar": 1}})
# 20-minute idle gap; a second ask answered with a sub-agent and no edit.
add(rec("user", "2026-11-01T10:21:06Z", "u2",
        message={"role": "user", "content": [{"type": "text", "text": "Now explain how the tests are organised"}]}))
add(asst("2026-11-01T10:21:10Z", "a8", "msg_7", "claude-opus-4-1",
         [{"type": "tool_use", "id": "t7", "name": "Task",
           "input": {"description": "Survey test layout", "subagent_type": "Explore", "prompt": "look"}}], u(20, 100)))
add(rec("user", "2026-11-01T10:21:11Z", "s1", isSidechain=True, message={"role": "user", "content": "look"}))
add(asst("2026-11-01T10:21:15Z", "s2", "msg_s1", "claude-haiku-4-5",
         [{"type": "tool_use", "id": "t8", "name": "Glob", "input": {"pattern": "**/*_test.go"}}], u(30, 5),
         isSidechain=True))
add(result("2026-11-01T10:21:16Z", "s3", [{"type": "tool_result", "tool_use_id": "t8", "content": "hello_test.go"}],
           isSidechain=True))
add(result("2026-11-01T10:21:20Z", "r7", [{"type": "tool_result", "tool_use_id": "t7", "content": "One test file."}]))
add(rec("system", "2026-11-01T10:21:21Z", "e1", subtype="api_error", content="overloaded_error"))
add(asst("2026-11-01T10:21:30Z", "a9", "msg_8", "claude-opus-4-1",
         [{"type": "thinking", "thinking": "secret thoughts"},
          {"type": "text", "text": "Tests live next to the code in hello_test.go."}], u(10, 25)))
add({"type": "pr-link", "sessionId": S, "prNumber": 7, "prUrl": "https://github.com/example/demo/pull/7",
     "prRepository": "example/demo"})

sys.stdout.write("\n".join(L) + "\n")
# Final line without a trailing newline (the writer may still be appending).
sys.stdout.write(json.dumps(rec("user", "2026-11-01T10:22:00Z", "u3",
                                message={"role": "user", "content": "thanks"})))

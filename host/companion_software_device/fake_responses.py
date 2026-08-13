#!/usr/bin/env python3
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

MODEL = "tier1-fake-model"

CASES = {
    "Tier1 expense 50k": {
        "id": "expense",
        "tool": "expense.log",
        "args": {
            "items": [{
                "amount_vnd": 50000,
                "category": "food",
                "description": "tier1 deterministic expense",
                "occurred_at": "2026-08-13T15:00:00+07:00",
            }]
        },
    },
    "Tier1 budget weekly": {
        "id": "budget",
        "tool": "budget.set",
        "args": {"period": "weekly", "limit_vnd": 1000000},
    },
    "Tier1 note": {
        "id": "note",
        "tool": "note.create",
        "args": {"content": "tier1 note"},
    },
    "Tier1 journal": {
        "id": "journal",
        "tool": "journal.create",
        "args": {
            "content": "tier1 journal",
            "occurred_at": "2026-08-13T15:10:00+07:00",
        },
    },
    "Tier1 reminder": {
        "id": "reminder",
        "tool": "reminder.create",
        "args": {
            "title": "tier1 reminder",
            "fire_at": "2026-08-14T09:00:00+07:00",
        },
    },
    "Tier1 timer": {
        "id": "timer",
        "tool": "timer.create",
        "args": {"title": "tier1 timer", "delay_seconds": 600},
    },
    "Tier1 memory": {
        "id": "memory",
        "tool": "memory.remember",
        "args": {
            "key": "preferred_language",
            "kind": "semantic",
            "value": "Vietnamese",
            "valid_from": "2026-08-13T15:20:00+07:00",
        },
    },
}


def walk(value):
    yield value
    if isinstance(value, dict):
        for item in value.values():
            yield from walk(item)
    elif isinstance(value, list):
        for item in value:
            yield from walk(item)


def contains_text(value, needle):
    return any(isinstance(item, str) and needle in item for item in walk(value))


def current_case(payload):
    found = []
    for item in walk(payload.get("input", [])):
        if not isinstance(item, str):
            continue
        for transcript, case in CASES.items():
            if transcript in item:
                found.append(case)
    return found[-1] if found else None


def tool_names(payload):
    names = set()
    for tool in payload.get("tools", []):
        if not isinstance(tool, dict):
            continue
        name = tool.get("name")
        if isinstance(name, str):
            names.add(name)
        function = tool.get("function")
        if isinstance(function, dict) and isinstance(function.get("name"), str):
            names.add(function["name"])
    return names


def successful_function_output(payload, call_id):
    for item in walk(payload.get("input", [])):
        if not isinstance(item, dict) or item.get("type") != "function_call_output":
            continue
        if item.get("call_id") != call_id:
            continue
        output = item.get("output")
        if not isinstance(output, str):
            continue
        try:
            decoded = json.loads(output)
        except json.JSONDecodeError:
            continue
        if isinstance(decoded, dict) and decoded.get("ok") is True:
            return True
    return False


def response_meta(response_id, total_tokens=8):
    return {
        "id": response_id,
        "model": MODEL,
        "status": "completed",
        "usage": {
            "input_tokens": total_tokens // 2,
            "input_tokens_details": {"cached_tokens": 0},
            "output_tokens": total_tokens // 2,
            "output_tokens_details": {"reasoning_tokens": 0},
            "total_tokens": total_tokens,
        },
    }


def text_events(response_id, text):
    return [
        {"type": "response.created", "response": response_meta(response_id, 0)},
        {"type": "response.output_text.delta", "delta": text},
        {"type": "response.output_text.done", "text": text},
        {"type": "response.completed", "response": response_meta(response_id)},
    ]


def function_call_events(response_id, case):
    arguments = json.dumps(case["args"], ensure_ascii=False, separators=(",", ":"))
    item_id = f"fc_{case['id']}_1"
    call_id = f"call_{case['id']}_1"
    tool = case["tool"]
    return [
        {"type": "response.created", "response": response_meta(response_id, 0)},
        {
            "type": "response.output_item.added",
            "output_index": 0,
            "item": {
                "type": "function_call",
                "id": item_id,
                "call_id": call_id,
                "name": tool,
                "arguments": "",
                "status": "in_progress",
            },
        },
        {
            "type": "response.function_call_arguments.delta",
            "item_id": item_id,
            "output_index": 0,
            "delta": arguments,
        },
        {
            "type": "response.function_call_arguments.done",
            "item_id": item_id,
            "output_index": 0,
            "name": tool,
            "arguments": arguments,
        },
        {
            "type": "response.output_item.done",
            "output_index": 0,
            "item": {
                "type": "function_call",
                "id": item_id,
                "call_id": call_id,
                "name": tool,
                "arguments": arguments,
                "status": "completed",
            },
        },
        {"type": "response.completed", "response": response_meta(response_id)},
    ]


class Handler(BaseHTTPRequestHandler):
    sequence = 0

    def log_message(self, fmt, *args):
        return

    def do_GET(self):
        if self.path == "/healthz":
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"ok")
            return
        self.send_error(404)

    def do_POST(self):
        if self.path != "/v1/responses":
            self.send_error(404, "expected /v1/responses")
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            payload = json.loads(self.rfile.read(length))
        except Exception as exc:
            self.send_error(400, str(exc))
            return

        if payload.get("model") != MODEL:
            self.send_error(400, "unexpected model")
            return

        Handler.sequence += 1
        response_id = f"resp_tier1_{Handler.sequence}"
        case = current_case(payload)

        if case is not None:
            names = tool_names(payload)
            if case["tool"] not in names:
                self.send_error(409, f"{case['tool']} was not exposed by ADK ToolRegistry")
                return
            if "expense.create" in names:
                self.send_error(409, "hidden expense.create leaked into ADK ToolRegistry")
                return
            call_id = f"call_{case['id']}_1"
            if successful_function_output(payload, call_id):
                events = text_events(response_id, f"Tier-1 tool parity ok: {case['id']}")
            else:
                events = function_call_events(response_id, case)
        elif contains_text(payload.get("input", []), "tier1 transcript"):
            events = text_events(response_id, "Tier-1 ADK response.")
        else:
            self.send_error(409, "unexpected deterministic model request")
            return

        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "close")
        self.end_headers()
        for event in events:
            encoded = json.dumps(event, ensure_ascii=False, separators=(",", ":"))
            self.wfile.write(f"data: {encoded}\n\n".encode("utf-8"))
            self.wfile.flush()
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()


if __name__ == "__main__":
    ThreadingHTTPServer(("127.0.0.1", 19000), Handler).serve_forever()

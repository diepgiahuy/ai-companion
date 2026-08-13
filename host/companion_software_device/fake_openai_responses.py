#!/usr/bin/env python3
"""Deterministic OpenAI Responses API SSE fixture for Tier-1 ADK orchestration.

It intentionally implements only the subset exercised by google/adk-go v2.2.0:
POST /v1/responses with streaming SSE events for text and function calls.
"""

import json
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

MODEL = "tier1-fake-model"


def walk(value):
    if isinstance(value, dict):
        yield value
        for child in value.values():
            yield from walk(child)
    elif isinstance(value, list):
        for child in value:
            yield from walk(child)


def all_text(value):
    parts = []
    if isinstance(value, str):
        parts.append(value)
    elif isinstance(value, dict):
        for child in value.values():
            parts.extend(all_text(child))
    elif isinstance(value, list):
        for child in value:
            parts.extend(all_text(child))
    return parts


def has_function_output(payload):
    return any(item.get("type") == "function_call_output" for item in walk(payload))


def response_meta(response_id, usage=False):
    data = {"id": response_id, "object": "response", "model": MODEL, "status": "completed"}
    if usage:
        data["usage"] = {
            "input_tokens": 11,
            "input_tokens_details": {"cached_tokens": 0},
            "output_tokens": 7,
            "output_tokens_details": {"reasoning_tokens": 0},
            "total_tokens": 18,
        }
    return data


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):
        return

    def do_GET(self):
        if self.path == "/healthz":
            body = b"ok"
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        self.send_error(404)

    def send_sse(self, events):
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "close")
        self.end_headers()
        for event in events:
            data = event if isinstance(event, str) else json.dumps(event, ensure_ascii=False, separators=(",", ":"))
            self.wfile.write(f"data: {data}\n\n".encode("utf-8"))
            self.wfile.flush()

    def do_POST(self):
        if self.path != "/v1/responses":
            self.send_error(404)
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            payload = json.loads(self.rfile.read(length) or b"{}")
        except Exception as exc:
            self.send_error(400, str(exc))
            return

        request_text = "\n".join(all_text(payload)).lower()
        response_id = f"resp_tier1_{time.time_ns()}"
        created = {"type": "response.created", "response": response_meta(response_id)}
        completed = {"type": "response.completed", "response": response_meta(response_id, usage=True)}

        if has_function_output(payload):
            # ADK must feed the authoritative ToolRegistry result back through a
            # Responses function_call_output item before the final model text.
            serialized = json.dumps(payload, ensure_ascii=False)
            if '"ok":true' not in serialized.replace(" ", ""):
                self.send_error(409, "authoritative tool result was not successful")
                return
            events = [
                created,
                {"type": "response.output_text.delta", "delta": "Đã lưu đúng một khoản 50 nghìn cho Tier-1."},
                completed,
                "[DONE]",
            ]
            self.send_sse(events)
            return

        if "đi chợ 50k" in request_text or "di cho 50k" in request_text:
            arguments = json.dumps(
                {
                    "amount_vnd": 50000,
                    "category": "food",
                    "description": "tier1 deterministic expense",
                    "occurred_at": "2026-08-13T15:00:00+07:00",
                },
                ensure_ascii=False,
                separators=(",", ":"),
            )
            events = [
                created,
                {
                    "type": "response.output_item.added",
                    "item": {
                        "type": "function_call",
                        "id": "fc_tier1_expense",
                        "call_id": "call_tier1_expense",
                        "name": "expense.create",
                    },
                },
                {
                    "type": "response.function_call_arguments.delta",
                    "item_id": "fc_tier1_expense",
                    "delta": arguments,
                },
                {
                    "type": "response.function_call_arguments.done",
                    "item_id": "fc_tier1_expense",
                    "name": "expense.create",
                    "arguments": "",
                },
                completed,
                "[DONE]",
            ]
            self.send_sse(events)
            return

        events = [
            created,
            {"type": "response.output_text.delta", "delta": "Tier-1 ADK response."},
            completed,
            "[DONE]",
        ]
        self.send_sse(events)


if __name__ == "__main__":
    ThreadingHTTPServer(("127.0.0.1", 19000), Handler).serve_forever()

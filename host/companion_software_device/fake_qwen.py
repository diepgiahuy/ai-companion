#!/usr/bin/env python3
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

REQUESTS = 0


class Handler(BaseHTTPRequestHandler):
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
        global REQUESTS
        if self.path != "/chat/completions":
            self.send_error(404)
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            payload = json.loads(self.rfile.read(length))
        except Exception as exc:
            self.send_error(400, str(exc))
            return
        REQUESTS += 1
        if REQUESTS == 1:
            response = {
                "choices": [{
                    "message": {
                        "role": "assistant",
                        "tool_calls": [{
                            "id": "tier1-expense-call",
                            "type": "function",
                            "function": {
                                "name": "expense.create",
                                "arguments": json.dumps({
                                    "amount_vnd": 50000,
                                    "category": "food",
                                    "description": "tier1 deterministic expense",
                                    "occurred_at": "2026-08-13T15:00:00+07:00",
                                }, separators=(",", ":")),
                            },
                        }],
                    }
                }]
            }
        elif REQUESTS == 2:
            messages = payload.get("messages", [])
            if not messages or messages[-1].get("role") != "tool" or '"ok":true' not in messages[-1].get("content", ""):
                self.send_error(409, "production tool result was not successful")
                return
            response = {
                "choices": [{
                    "message": {
                        "role": "assistant",
                        "content": "Đã lưu đúng một khoản 50 nghìn cho Tier-1.",
                    }
                }]
            }
        else:
            self.send_error(409, "unexpected extra model request")
            return
        encoded = json.dumps(response, ensure_ascii=False).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)


if __name__ == "__main__":
    ThreadingHTTPServer(("127.0.0.1", 19000), Handler).serve_forever()

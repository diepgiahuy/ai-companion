#!/usr/bin/env python3
"""Benchmark-only OpenAI-compatible bridge for Fun-ASR-MLT-Nano-2512.

This process exists only so #105 can exercise the production Go FunASR adapter
against the official FunASR inference runtime. It is not a product server and it
does not create a second production ASR path.
"""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import sys
import tempfile
import threading
from email import policy
from email.parser import BytesParser
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

MODEL = None
MODEL_LOCK = threading.Lock()

LANGUAGES = {
    "vi": "越南语",
    "vi-vn": "越南语",
    "en": "英文",
    "en-us": "英文",
}


def load_model():
    global MODEL
    model_path = pathlib.Path(os.environ["FUNASR_EVAL_MODEL_PATH"]).resolve()
    remote_code = pathlib.Path(os.environ["FUNASR_EVAL_REMOTE_CODE"]).resolve()
    if not model_path.exists():
        raise RuntimeError(f"model path does not exist: {model_path}")
    if not remote_code.is_file():
        raise RuntimeError(f"remote code does not exist: {remote_code}")

    # The official model.py imports companion modules such as ctc.py from the
    # same repository checkout. Keep that exact pinned checkout importable.
    sys.path.insert(0, str(remote_code.parent))
    from funasr import AutoModel

    MODEL = AutoModel(
        model=str(model_path),
        trust_remote_code=True,
        remote_code=str(remote_code),
        device=os.environ.get("FUNASR_EVAL_DEVICE", "cpu"),
        disable_update=True,
        disable_pbar=True,
    )


def parse_multipart(content_type: str, body: bytes) -> tuple[bytes, dict[str, str]]:
    message = BytesParser(policy=policy.default).parsebytes(
        b"Content-Type: "
        + content_type.encode("utf-8")
        + b"\r\nMIME-Version: 1.0\r\n\r\n"
        + body
    )
    if not message.is_multipart():
        raise ValueError("expected multipart/form-data request")
    audio = b""
    fields: dict[str, str] = {}
    for part in message.iter_parts():
        name = part.get_param("name", header="content-disposition")
        if not name:
            continue
        payload = part.get_payload(decode=True) or b""
        if name == "file":
            audio = payload
        else:
            fields[name] = payload.decode("utf-8", errors="strict").strip()
    if not audio:
        raise ValueError("multipart request contains no file audio")
    return audio, fields


def extract_text(result) -> str:
    if not result:
        return ""
    first = result[0]
    if isinstance(first, dict):
        return str(first.get("text") or "").strip()
    if isinstance(first, (list, tuple)) and first and isinstance(first[0], dict):
        return str(first[0].get("text") or "").strip()
    return ""


class Handler(BaseHTTPRequestHandler):
    server_version = "companion-funasr-eval/1"

    def log_message(self, fmt: str, *args) -> None:
        sys.stderr.write("funasr_eval_server: " + (fmt % args) + "\n")

    def send_json(self, status: int, payload: dict) -> None:
        raw = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self) -> None:
        if self.path == "/healthz":
            self.send_json(HTTPStatus.OK, {"status": "ok"})
            return
        self.send_json(HTTPStatus.NOT_FOUND, {"error": "not found"})

    def do_POST(self) -> None:
        if self.path != "/v1/audio/transcriptions":
            self.send_json(HTTPStatus.NOT_FOUND, {"error": "not found"})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if length <= 0 or length > 8 * 1024 * 1024:
                raise ValueError(f"invalid request body length {length}")
            body = self.rfile.read(length)
            audio, fields = parse_multipart(self.headers.get("Content-Type", ""), body)
            language = LANGUAGES.get(fields.get("language", "").lower())
            with tempfile.NamedTemporaryFile(suffix=".wav") as wav:
                wav.write(audio)
                wav.flush()
                kwargs = {
                    "input": [wav.name],
                    "cache": {},
                    "batch_size": 1,
                    "itn": True,
                    "llm_kwargs": {"do_sample": False},
                }
                if language:
                    kwargs["language"] = language
                with MODEL_LOCK:
                    result = MODEL.generate(**kwargs)
            text = extract_text(result)
            if not text:
                raise RuntimeError(f"FunASR returned no text: {result!r}")
            self.send_json(HTTPStatus.OK, {"text": text})
        except Exception as exc:
            self.log_message("transcription failed: %s", exc)
            self.send_json(HTTPStatus.INTERNAL_SERVER_ERROR, {"error": str(exc)})


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=18080)
    args = parser.parse_args()
    load_model()
    server = ThreadingHTTPServer((args.host, args.port), Handler)
    print(f"FunASR eval bridge ready on http://{args.host}:{args.port}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

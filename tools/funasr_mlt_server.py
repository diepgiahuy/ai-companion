#!/usr/bin/env python3
"""Local OpenAI-compatible sidecar for Fun-ASR-MLT-Nano-2512.

This intentionally serves exactly one configured checkpoint behind the small
HTTP boundary Companion already uses. It exists because upstream FunASR's stock
OpenAI API aliases are not a reliable way to distinguish the multilingual MLT
checkpoint from the standard Nano/SenseVoice routes.

Development/benchmark use only: bind to localhost. Do not expose this process to
an untrusted network without a real TLS/auth/rate-limit gateway.
"""

from __future__ import annotations

import argparse
import os
import tempfile
import threading
import time
from pathlib import Path
from typing import Any

import uvicorn
from fastapi import FastAPI, File, Form, HTTPException, UploadFile
from funasr import AutoModel

DEFAULT_CHECKPOINT = "FunAudioLLM/Fun-ASR-MLT-Nano-2512"
DEFAULT_SERVED_MODEL = "companion-funasr-mlt"
MAX_UPLOAD_BYTES = 4 * 1024 * 1024


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=18080)
    parser.add_argument("--device", default=os.getenv("FUNASR_DEVICE", "mps"))
    parser.add_argument("--checkpoint", default=os.getenv("FUNASR_CHECKPOINT", DEFAULT_CHECKPOINT))
    parser.add_argument("--served-model", default=os.getenv("FUNASR_SERVED_MODEL", DEFAULT_SERVED_MODEL))
    parser.add_argument(
        "--remote-code",
        default=os.getenv("FUNASR_REMOTE_CODE", ""),
        help="Path to the upstream Fun-ASR model.py used by the MLT checkpoint",
    )
    parser.add_argument("--max-upload-bytes", type=int, default=MAX_UPLOAD_BYTES)
    return parser.parse_args()


class Runtime:
    def __init__(self, args: argparse.Namespace) -> None:
        if args.host not in {"127.0.0.1", "localhost", "::1"}:
            raise SystemExit("FunASR MLT sidecar must bind to localhost for the POC")
        if args.max_upload_bytes <= 0:
            raise SystemExit("--max-upload-bytes must be positive")
        remote_code = str(args.remote_code).strip()
        if not remote_code:
            raise SystemExit(
                "FUNASR_REMOTE_CODE/--remote-code is required; point it at the exact upstream Fun-ASR model.py used for this benchmark"
            )
        remote_path = Path(remote_code).expanduser().resolve()
        if not remote_path.is_file():
            raise SystemExit(f"FunASR remote model code not found: {remote_path}")

        self.checkpoint = str(args.checkpoint).strip()
        self.served_model = str(args.served_model).strip()
        self.device = str(args.device).strip()
        self.max_upload_bytes = int(args.max_upload_bytes)
        self.lock = threading.Lock()
        self.loaded_at = time.time()
        self.model = AutoModel(
            model=self.checkpoint,
            hub="hf",
            trust_remote_code=True,
            remote_code=str(remote_path),
            device=self.device,
        )

    def transcribe(self, wav_path: str, language: str) -> str:
        kwargs: dict[str, Any] = {
            "input": [wav_path],
            "cache": {},
            "batch_size": 1,
            "itn": True,
            "llm_kwargs": {"do_sample": False},
        }
        if language:
            kwargs["language"] = language
        # The model object is shared to avoid reload cost. Serialize first-P0C
        # inference until benchmark evidence justifies a concurrency policy.
        with self.lock:
            result = self.model.generate(**kwargs)
        if not result or not isinstance(result[0], dict):
            raise RuntimeError(f"unexpected FunASR result shape: {type(result)!r}")
        return str(result[0].get("text", "")).strip()


def build_app(runtime: Runtime) -> FastAPI:
    app = FastAPI(title="Companion FunASR MLT local sidecar", version="1")

    @app.get("/health")
    def health() -> dict[str, Any]:
        return {
            "ok": True,
            "model": runtime.served_model,
            "checkpoint": runtime.checkpoint,
            "device": runtime.device,
        }

    @app.get("/v1/models")
    def models() -> dict[str, Any]:
        return {
            "object": "list",
            "data": [
                {
                    "id": runtime.served_model,
                    "object": "model",
                    "owned_by": "local-funasr",
                }
            ],
        }

    @app.post("/v1/audio/transcriptions")
    async def transcriptions(
        file: UploadFile = File(...),
        model: str = Form(...),
        response_format: str = Form("json"),
        language: str = Form(""),
    ) -> dict[str, Any]:
        if model != runtime.served_model:
            raise HTTPException(status_code=400, detail=f"unknown model {model!r}")
        if response_format not in {"json", "verbose_json"}:
            raise HTTPException(status_code=400, detail="only json/verbose_json are supported")

        payload = await file.read(runtime.max_upload_bytes + 1)
        if len(payload) > runtime.max_upload_bytes:
            raise HTTPException(status_code=413, detail="audio upload exceeds configured limit")
        if len(payload) < 44 or payload[:4] != b"RIFF" or payload[8:12] != b"WAVE":
            raise HTTPException(status_code=400, detail="POC sidecar requires WAV input")

        tmp_path = ""
        try:
            with tempfile.NamedTemporaryFile(prefix="companion-funasr-", suffix=".wav", delete=False) as handle:
                handle.write(payload)
                tmp_path = handle.name
            started = time.perf_counter()
            try:
                text = runtime.transcribe(tmp_path, language.strip())
            except Exception as exc:
                raise HTTPException(status_code=502, detail=f"FunASR inference failed: {type(exc).__name__}: {exc}") from exc
            duration_ms = round((time.perf_counter() - started) * 1000, 3)
            response: dict[str, Any] = {"text": text}
            if response_format == "verbose_json":
                response.update(
                    {
                        "model": runtime.served_model,
                        "checkpoint": runtime.checkpoint,
                        "device": runtime.device,
                        "inference_ms": duration_ms,
                    }
                )
            return response
        finally:
            if tmp_path:
                Path(tmp_path).unlink(missing_ok=True)

    return app


def main() -> None:
    args = parse_args()
    runtime = Runtime(args)
    uvicorn.run(build_app(runtime), host=args.host, port=args.port, log_level="info")


if __name__ == "__main__":
    main()

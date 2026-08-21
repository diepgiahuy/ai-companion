#!/usr/bin/env python3
"""Prepare a tiny reproducible human-recorded FLEURS corpus for #105.

The Hugging Face Dataset Viewer `/rows` endpoint does not accept a revision
parameter. Therefore this script does not claim revision-addressed row reads.
Instead it:

* records the FLEURS repository head observed before extraction;
* verifies the repository head did not move while rows were fetched;
* records every selected row and normalized 16-kHz mono PCM SHA-256; and
* relies on the emitted manifest SHA-256 + PCM hashes as the immutable benchmark
  corpus identity used to compare provider candidates.

The mixed case is a deterministic concatenation of one Vietnamese and one
English human recording and is explicitly labelled ``composite_recorded``.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
import subprocess
import sys
import urllib.parse
import urllib.request

DATASET = "google/fleurs"
SPLIT = "validation"
SOURCE_API = "https://datasets-server.huggingface.co/rows"
REVISION_SEMANTICS = (
    "repository_head_observed_and_stable_during_dataset_viewer_extraction; "
    "viewer_rows_are_not_revision_addressable; manifest_and_pcm_sha256_are_corpus_identity"
)
OFFSETS = {"vi": ("vi_vn", [0, 7]), "en": ("en_us", [0, 7])}


def get_json(url: str) -> dict:
    request = urllib.request.Request(url, headers={"User-Agent": "ai-companion-voice-eval/1"})
    with urllib.request.urlopen(request, timeout=60) as response:
        return json.load(response)


def dataset_revision() -> str:
    metadata = get_json("https://huggingface.co/api/datasets/google/fleurs")
    revision = str(metadata.get("sha") or "").strip()
    if len(revision) < 12:
        raise RuntimeError("could not resolve google/fleurs repository head")
    return revision


def download(url: str, path: pathlib.Path) -> None:
    request = urllib.request.Request(url, headers={"User-Agent": "ai-companion-voice-eval/1"})
    with urllib.request.urlopen(request, timeout=120) as response, path.open("wb") as out:
        while True:
            chunk = response.read(1024 * 1024)
            if not chunk:
                break
            out.write(chunk)


def normalize_audio(src: pathlib.Path, dst: pathlib.Path) -> None:
    subprocess.run(
        [
            "ffmpeg",
            "-nostdin",
            "-hide_banner",
            "-loglevel",
            "error",
            "-y",
            "-i",
            str(src),
            "-ac",
            "1",
            "-ar",
            "16000",
            "-f",
            "s16le",
            str(dst),
        ],
        check=True,
    )


def sha256(path: pathlib.Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def duration_ms(path: pathlib.Path) -> float:
    return path.stat().st_size / (16000 * 2) * 1000.0


def fetch_row(config: str, offset: int) -> dict:
    query = urllib.parse.urlencode(
        {
            "dataset": DATASET,
            "config": config,
            "split": SPLIT,
            "offset": offset,
            "length": 1,
        }
    )
    payload = get_json(SOURCE_API + "?" + query)
    rows = payload.get("rows") or []
    if len(rows) != 1:
        raise RuntimeError(f"dataset viewer returned {len(rows)} rows for {config}:{offset}")
    return rows[0]


def transcript(row: dict) -> str:
    record = row.get("row") or {}
    text = record.get("transcription") or record.get("raw_transcription") or record.get("text")
    if not isinstance(text, str) or not text.strip():
        raise RuntimeError(f"FLEURS row has no transcription field: keys={sorted(record)}")
    return " ".join(text.split())


def audio_url(row: dict) -> str:
    """Return the first downloadable audio source exposed by Dataset Viewer.

    Dataset Viewer currently serializes the Audio feature as a list of media
    source dictionaries even for a single recording. Older responses and local
    fixtures may expose a single dictionary, so accept both shapes explicitly.
    """
    record = row.get("row") or {}
    audio = record.get("audio")
    candidates = audio if isinstance(audio, list) else [audio]
    for candidate in candidates:
        if isinstance(candidate, dict) and isinstance(candidate.get("src"), str):
            source = candidate["src"].strip()
            if source:
                return source
    raise RuntimeError(f"FLEURS row has no downloadable audio.src: audio={audio!r}")


def tts_response(language: str) -> str:
    if language == "vi":
        return "Tôi đã nghe rõ. Đây là phản hồi kiểm tra của Companion."
    if language == "en":
        return "I heard you clearly. This is the Companion benchmark response."
    return "Tôi đã nghe rõ. I heard you clearly."


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--out-dir", required=True)
    parser.add_argument("--manifest", required=True)
    args = parser.parse_args()

    out_dir = pathlib.Path(args.out_dir).resolve()
    out_dir.mkdir(parents=True, exist_ok=True)
    manifest_path = pathlib.Path(args.manifest).resolve()
    manifest_path.parent.mkdir(parents=True, exist_ok=True)

    revision = dataset_revision()
    cases: list[dict] = []
    first_by_language: dict[str, dict] = {}
    for language, (config, offsets) in OFFSETS.items():
        for offset in offsets:
            row = fetch_row(config, offset)
            row_index = int(row.get("row_idx", offset))
            source = out_dir / f"{language}-{row_index}.source"
            pcm = out_dir / f"{language}-{row_index}.pcm"
            download(audio_url(row), source)
            normalize_audio(source, pcm)
            source.unlink(missing_ok=True)
            case = {
                "id": f"fleurs-{language}-{row_index}",
                "language": language,
                "reference": transcript(row),
                "pcm_path": str(pcm),
                "pcm_sha256": sha256(pcm),
                "duration_ms": duration_ms(pcm),
                "source_row": row_index,
                "source_kind": "human_recorded",
                "asr_language": "vi" if language == "vi" else "en",
                "tts_voice": "",
                "tts_response": tts_response(language),
            }
            cases.append(case)
            first_by_language.setdefault(language, case)

    revision_after = dataset_revision()
    if revision_after != revision:
        raise RuntimeError(
            "google/fleurs repository head changed during Dataset Viewer extraction: "
            f"before={revision} after={revision_after}; rerun to avoid ambiguous provenance"
        )

    vi = first_by_language["vi"]
    en = first_by_language["en"]
    mixed_pcm = out_dir / "mixed-vi-en.pcm"
    silence = b"\x00\x00" * int(16000 * 0.25)
    with mixed_pcm.open("wb") as out:
        out.write(pathlib.Path(vi["pcm_path"]).read_bytes())
        out.write(silence)
        out.write(pathlib.Path(en["pcm_path"]).read_bytes())
    cases.append(
        {
            "id": "fleurs-mixed-vi-en",
            "language": "mixed",
            "reference": vi["reference"] + " " + en["reference"],
            "pcm_path": str(mixed_pcm),
            "pcm_sha256": sha256(mixed_pcm),
            "duration_ms": duration_ms(mixed_pcm),
            "source_row": vi["source_row"],
            "source_rows": [vi["source_row"], en["source_row"]],
            "source_kind": "composite_recorded",
            "asr_language": "",
            "tts_voice": "",
            "tts_response": tts_response("mixed"),
        }
    )

    manifest = {
        "dataset": DATASET,
        "revision": revision,
        "revision_semantics": REVISION_SEMANTICS,
        "source_api": SOURCE_API,
        "license": "CC-BY-4.0",
        "split": SPLIT,
        "cases": cases,
    }
    manifest_path.write_text(
        json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    print(f"prepared {len(cases)} content-addressed cases from {DATASET}; observed_head={revision}")
    for case in cases:
        print(
            f"{case['id']}: {case['pcm_sha256']} "
            f"{case['duration_ms']:.1f}ms {case['source_kind']}"
        )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"prepare_voice_eval_corpus: {exc}", file=sys.stderr)
        raise

# Companion asset bundle v1

This document defines the pre-activation Product-v1 contract for shipping bounded visual
assets independently from firmware. It is intentionally a **data-only** contract. It does
not switch the product display runtime, reserve a flash partition, or permit remote code.

## Why this shape

As of 2026-08-16, Espressif `esp_mmap_assets` 2.0.0 is the maintained asset-packaging
component and supports separating image/font assets from application code. LVGL 9.6 can
load XML Screens and Components at runtime, but Widgets remain C code. Product-v1 does
not need runtime XML to freeze the asset trust boundary, so schema v1 excludes XML,
script, Wasm, native code, remote-fetch URLs in render data, and device-capability instructions. A later renderer
issue may consume validated v1 assets or introduce an explicitly versioned schema after
its own resource/security proof.

Current firmware uses Ed25519 for signed OTA metadata on the backend and SHA-256 for
firmware artifacts. Asset bundles reuse those well-understood primitives but use a
separate manifest and key id; an asset key is not implicitly a firmware-signing key.

The current 16-MB partition table has two 4-MB app slots, a 4-MB model partition and a
`0x3D0000` LittleFS partition. **No bytes are reserved for assets by this contract.** The
2-MiB default expanded-content ceiling is a conservative host-side safety ceiling below
the currently available filesystem partition, not a promise that 2 MiB is installable.
The later device installer must apply the selected hardware/storage budget before write.

## Container

A bundle is a ZIP container with a product extension chosen by the caller. The default
whole-container ceiling is 3 MiB. Schema v1 requires every ZIP member to use `STORE`
(no outer compression) and to be a regular file (no directories, symlinks, or devices). Images and fonts are
already encoded formats; disabling archive compression removes decompression-bomb and
compression-ratio ambiguity from the device-facing boundary.

Required entries:

- `manifest.json` — canonical compact JSON, at most 64 KiB by default;
- `manifest.sig` — raw 64-byte Ed25519 signature of the exact `manifest.json` bytes;
- one or more declared files under `assets/`.

No undeclared archive entries are accepted. Duplicate paths are rejected. Identical
manifest/source bytes plus the same Ed25519 key produce byte-identical bundles because
assets are path-sorted and ZIP metadata is fixed.

## Manifest v1

```json
{
  "schema_version": 1,
  "bundle_id": "desk-face",
  "version": "1.0.0",
  "min_asset_abi": 1,
  "target": {
    "board": "esp32-s3",
    "display_width": 240,
    "display_height": 240
  },
  "expanded_bytes": 12345,
  "assets": [
    {
      "role": "avatar.happy",
      "type": "image/png",
      "path": "assets/happy.png",
      "sha256": "...64 lowercase hex...",
      "size": 1234,
      "width": 64,
      "height": 64,
      "license": {
        "id": "CC0-1.0",
        "source": "generated:companion"
      }
    }
  ],
  "signature": {
    "algorithm": "ed25519",
    "key_id": "asset-prod-2026-01"
  }
}
```

`display_width` and `display_height` are either both omitted/zero (not resolution-specific)
or both set and matched exactly at validation. `min_asset_abi` is monotonic compatibility,
not firmware versioning.

`bundle_sha256` is deliberately not embedded in the signed manifest because that creates
a self-reference with the container bytes. `Validate` returns both the manifest SHA-256
and whole-bundle SHA-256 so the control plane/download layer can record transport identity.
Integrity inside the bundle is the Ed25519-signed canonical manifest plus each asset's
SHA-256 and exact size.

## Allowed asset types

Schema v1 allows only:

- `theme/json` — a typed palette object only. Schema v1 accepts 1–32 named palette
  entries whose values are `#RRGGBB` or `#RRGGBBAA`; unknown top-level fields, URLs,
  expressions and other render instructions are rejected;
- `image/png` — bounded PNG; dimensions are decoded from the file and must match the
  signed manifest;
- `font/ttf` — bounded TTF/OTF-sfnt TrueType font data. A font that declares `en` or
  `vi` responsibility is checked against its Unicode `cmap`; manifest metadata alone is
  not trusted for glyph coverage.

Semantic roles such as `background.main`, `emoji.smile`, `avatar.sleepy`,
`font.primary`, or `theme.default` are data identifiers. Roles are unique inside a
bundle. Unknown MIME/content types fail closed.

Runtime XML, animation, QOI/LVGL binary images, binary LVGL fonts, and other formats are
**not rejected forever**; they are simply not part of schema v1 until the selected
renderer/toolchain proves the format, memory budget, parser boundary, and fallback.
Espressif `esp_mmap_assets` may later transform or stage validated source assets without
changing this trust boundary.

## Default host safety limits

`DefaultLimits()` currently enforces:

| Limit | Default |
|---|---:|
| whole bundle bytes | 3 MiB |
| manifest bytes | 64 KiB |
| asset files | 64 |
| path bytes | 128 |
| one asset | 1 MiB |
| total expanded assets | 2 MiB |
| PNG width / height | 1024 px each |
| ZIP method | STORE only |

Callers may choose **stricter** device/profile limits. Raising these values is a reviewed
contract/resource change, not an Owner setting.

## Validation order and security invariants

Before any device activation, host validation fails closed on:

1. whole-bundle byte ceiling, corrupt/truncated ZIP, unsupported compression, non-regular entries, count/size limits;
2. non-UTF-8, absolute, non-normalized, backslash/NUL or traversal paths;
3. duplicate archive paths;
4. bounded canonical JSON with unknown manifest fields rejected;
5. exact 64-byte Ed25519 signature and trusted public key;
6. schema, bundle/version ids, asset ABI, board/display compatibility;
7. unique manifest path and semantic role;
8. required bounded `license.id` and provenance source per shipped asset; source must be HTTPS or explicit `generated:` test/original metadata and is never a device fetch instruction;
9. exact asset size and SHA-256;
10. content-specific PNG dimensions / font glyph coverage;
11. no undeclared archive entry and exact expanded-byte accounting.

Device-side code must repeat the security-critical checks before writing/activating:
trusted signature, schema/ABI/target, path normalization, count/size ceilings, per-entry
hash/size, allowed type, and final storage budget. Host validation is not a trust bypass.

## Font profiles and fixture rights

`en` covers printable ASCII. `vi` covers that base plus the precomposed Vietnamese Latin
letters used by the language. The validator reads the font's Unicode cmap (formats 4 and
12) and fails on the first missing required code point.

Tests contain small deterministic gzip-stored subsets derived from Noto Sans; tests inflate them only to create/validate real `font/ttf` bundle entries. Noto fonts are
licensed under SIL Open Font License 1.1; the fixture manifest records `OFL-1.1` and the
Noto Latin/Greek/Cyrillic project as source. The subsets exist only as deterministic test
corpus and are not Product-v1 visual-design choices.

## Host tooling

The reusable package is `backend/internal/assetbundle`:

- `Pack` builds and signs deterministic v1 bundles from trusted source bytes;
- `Validate` verifies trust, compatibility, accounting and content bounds;
- `Inspect` reads only the bounded manifest for diagnostics and is **not** a trust check.

`backend/cmd/assetbundle` exposes `inspect` and `validate`. It intentionally does not take
production private signing keys on a CLI command line; later Assets Studio/control-plane
signing should call the package behind an explicit key-management boundary.

Example validation:

```text
go run ./cmd/assetbundle validate \
  -public-key <RAW_URLSAFE_BASE64_ED25519_PUBLIC_KEY> \
  -board esp32-s3 -display-width 240 -display-height 240 -asset-abi 1 \
  bundle.cabundle
```

## Evidence boundary

The tests are Tier-0/L1 contract evidence: real ZIPs are generated, signed, verified and
parsed; corruption, hostile paths, wrong keys/hashes, resource limits, target mismatch,
unsupported executable types and font coverage are exercised. Fuzzing checks that the
validator does not panic on malformed bytes.

This does **not** prove flash installation, LVGL decode/rendering, frame rate, PSRAM use,
power behavior, rollback after power loss, or physical display quality. Those claims
belong to later device integration and trusted HIL issues.

## Official references checked for this decision

- Espressif Component Registry, `espressif/esp_mmap_assets` 2.0.0.
- LVGL 9.6 XML Overview and runtime XML integration documentation.
- LVGL 9.6 font asset documentation.
- Noto Latin/Greek/Cyrillic project, SIL Open Font License 1.1.

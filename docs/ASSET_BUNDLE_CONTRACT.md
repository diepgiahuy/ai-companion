# Signed bounded asset bundle contract

Status: Product-v1 schema v1 contract for host packaging/validation. This document does **not** activate a device asset runtime.

## Decision

Schema v1 uses one deterministic ZIP container with `ZIP Store` only, a root `manifest.json`, Ed25519 trust metadata, and SHA-256 for every payload. The bundle is intentionally non-executable.

Allowed v1 payload types:

- `theme_json` — bounded local theme/palette metadata;
- `image_png` — raster backgrounds, avatars, emoji/expression frames;
- `font_ttf` — UI font/subset data with optional declared `en`/`vi` coverage.

Not allowed in schema v1:

- scripts or native code;
- Wasm;
- executable LVGL XML;
- arbitrary runtime URLs/data URLs;
- arbitrary device-capability instructions;
- outer ZIP compression.

This keeps the trust/parser boundary small while hardware/display selection (#8) and expressive rendering (#9) remain separate concerns. Current official tooling can still be used later behind this contract: Espressif `esp_mmap_assets` 2.0.0 packages/maps images/fonts and LVGL 9.6 can runtime-load XML Components/Screens, while Widgets remain compiled C behavior. XML is deliberately deferred from schema v1 rather than implicitly trusted.

## Archive layout

```text
bundle.zip
├── manifest.json
└── assets/
    ├── theme.json
    ├── avatar.png
    └── ui.ttf
```

Every path is a normalized UTF-8 relative path under `assets/`. Backslashes, absolute paths, `.`/`..`, duplicate archive paths and undeclared payloads are rejected before activation/extraction.

## Manifest

```json
{
  "schema_version": 1,
  "bundle_id": "companion.default",
  "version": "1.0.0",
  "min_asset_abi": 1,
  "targets": [
    {"board": "esp32s3", "width": 320, "height": 240}
  ],
  "assets": [
    {
      "role": "avatar.neutral",
      "type": "image_png",
      "path": "assets/avatar.png",
      "sha256": "<64 hex>",
      "size": 1234,
      "width": 64,
      "height": 64,
      "license": {
        "source": "project:original",
        "license": "LicenseRef-Project-Original"
      }
    }
  ],
  "signature": {
    "algorithm": "ed25519",
    "key_id": "asset-prod-1",
    "value": "<base64url signature>"
  }
}
```

`Pack` computes every payload `size` and `sha256`; callers do not supply trustworthy values. The Ed25519 signature covers canonical Go JSON for the complete manifest with `signature.value` blank. Because the signed manifest contains every entry digest, changing either metadata or payload bytes invalidates validation.

`license.source` and `license.license` are required shipping provenance. `source` is informational provenance only; it is never fetched or interpreted by device/runtime code. `attribution` is optional.

## Compatibility

A validator invocation supplies the target board, display width/height and supported asset ABI. Validation succeeds only when:

- `target.board` is the exact board or `*`;
- target width/height match exactly;
- device/consumer ABI is at least `min_asset_abi`.

This intentionally avoids guessing image scaling or renderer compatibility.

## Security/resource limits

Host schema-v1 limits are constants in `internal/assetbundle`:

- archive: 2 MiB maximum;
- manifest: 64 KiB maximum;
- assets: 64 maximum;
- each asset: 1 MiB maximum;
- total asset payload bytes: 2 MiB maximum;
- path: 160 bytes maximum;
- image dimension: 2048 maximum on either axis;
- image pixel count: 1,048,576 maximum;
- ZIP method: Store only, with compressed size equal to expanded size.

Store-only is deliberate: schema v1 has no outer decompressor and therefore no archive decompression-bomb class. Formats with their own decoder still require device/render-library bounds.

PNG validation checks the actual IHDR dimensions against manifest metadata and resource limits. Theme JSON is parsed and rejects runtime `http:`, `https:`, `data:` and `javascript:` string references. TTF coverage reads the font `cmap` table (formats 4/12) and checks a deterministic Product-v1 English/Vietnamese glyph set when those languages are declared.

The TTF check is a glyph-coverage oracle, not a substitute for the eventual LVGL/font rasterizer. Device-side rendering compatibility remains owned by #8/#9.

## Device-side requirements for later activation

Host validation is not a trust boundary by itself. A future device implementation must revalidate at least:

1. schema and ABI compatibility;
2. signature/key trust;
3. exact allow-listed content types;
4. normalized paths and duplicate entries;
5. per-entry and total byte budgets;
6. payload SHA-256 while streaming;
7. image dimensions before render/allocation;
8. target display compatibility.

The device should verify into inactive/staging storage and switch the active asset version only after the whole bundle passes. Do not extract untrusted paths directly into the active filesystem.

The contract is designed so a device can keep the manifest bounded to 64 KiB and stream payload hashing with a small fixed buffer rather than loading a 2 MiB bundle into RAM. Exact firmware code/heap/flash cost must be measured when the device loader is implemented; schema v1 does not claim a physical memory result.

## Host tooling

The Go package `internal/assetbundle` owns `Pack` and `Validate`.

The `cmd/assetbundle` tool supports:

```text
assetbundle pack \
  -manifest manifest.json \
  -assets-dir . \
  -private-key-file test-private-key.b64 \
  -key-id test-key \
  -out bundle.zip

assetbundle validate \
  -bundle bundle.zip \
  -public-key-file test-public-key.b64 \
  -key-id test-key \
  -board esp32s3 \
  -width 320 -height 240 -abi 1
```

Key files contain raw Ed25519 key bytes encoded with unpadded base64url. Production private keys must stay in the appropriate secret/signing system; the repository test suite generates ephemeral keys only.

## Evidence and negative corpus

Tests generate real ZIP bundles and original/synthetic test assets at runtime, so no third-party redistributable asset is needed in the repository. The corpus covers valid deterministic round-trip plus traversal, duplicate path, wrong digest, invalid signature, compressed ZIP entry, oversized/extreme dimensions, missing Vietnamese glyphs, incompatible target/ABI, executable type, runtime URL, invalid schema, missing provenance, undeclared payload, archive truncation and archive byte budget.

A fuzz target feeds arbitrary archive bytes to `Validate` and asserts the parser does not panic. This is Tier-0/host contract evidence only; it does not promote display, storage, timing or physical-device claims.

## Rollback/versioning

No product runtime consumes this bundle yet, so schema v1 can still be revised before promotion. Once a schema version is consumed by shipped devices, incompatible interpretation changes require a new schema version rather than silently changing v1 semantics.

#!/usr/bin/env python3
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
FLASH = 16 * 1024 * 1024


def number(value: str) -> int:
    return int(value, 0)


rows = []
for raw in (ROOT / "partitions.csv").read_text().splitlines():
    raw = raw.strip()
    if raw and not raw.startswith("#"):
        rows.append([item.strip() for item in raw.split(",")])

end = 0
ota_slots = []
for name, _, _, offset, size, *_ in rows:
    start = number(offset)
    length = number(size)
    end = max(end, start + length)
    if name.startswith("ota_"):
        ota_slots.append((name, length))

if end > FLASH:
    raise SystemExit(f"partition table exceeds 16 MiB: 0x{end:X}")
if len(ota_slots) != 2 or any(size < 0x400000 for _, size in ota_slots):
    raise SystemExit("expected two OTA app partitions of at least 4 MiB each")

# Conservative static design budget for this POC. Target measurements from
# idf.py size remain the final authority once ESP-IDF is installed.
internal = {
    "i2s_dma_descriptors": 32 * 1024,
    "audio_work_buffers": 16 * 1024,
    "bounded_network_queues": 32 * 1024,
    "ssd1306_framebuffer": 512,
    "application_network_driver_stacks": 80 * 1024,
}
psram = {
    "opus_codec_workspace": 128 * 1024,
}

print(f"PASS partition end: 0x{end:X} / 0x{FLASH:X} ({end / FLASH:.1%})")
print("OTA slots:", ", ".join(f"{name}={size / 1024 / 1024:.2f} MiB" for name, size in ota_slots))
print(f"Internal SRAM design cap: {sum(internal.values()) / 1024:.1f} KiB")
for name, size in internal.items():
    print(f"  {name}: {size / 1024:.1f} KiB")
print(f"PSRAM codec reserve: {sum(psram.values()) / 1024:.1f} KiB")
for name, size in psram.items():
    print(f"  {name}: {size / 1024:.1f} KiB")

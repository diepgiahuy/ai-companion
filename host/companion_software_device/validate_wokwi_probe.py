#!/usr/bin/env python3
import json
import sys
from pathlib import Path

path = Path(sys.argv[1])
data = json.loads(path.read_text(encoding="utf-8"))
if data.get("schema_version") != 1 or data.get("evidence_class") != "tier2_capability_probe":
    raise SystemExit("Wokwi probe: invalid evidence identity")
status = data.get("status")
if status == "unavailable":
    if data.get("blocker_code") != "missing_wokwi_cli_token":
        raise SystemExit("Wokwi probe: unavailable result lacks exact token blocker")
    if data.get("simulation_ran") is not False:
        raise SystemExit("Wokwi probe: unavailable result must not claim simulation")
    print("WOKWI TIER-2 UNAVAILABLE: repository WOKWI_CLI_TOKEN is not available to this trusted workflow")
    raise SystemExit(0)
if status == "token_available":
    if data.get("simulation_ran") is not False:
        raise SystemExit("Wokwi probe: token probe must not claim simulation")
    print("WOKWI TIER-2 TOKEN AVAILABLE: a real targeted simulation scenario is now required")
    raise SystemExit(3)
raise SystemExit(f"Wokwi probe: unsupported status {status!r}")

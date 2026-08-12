# Simulation

## Host simulator — complete logical POC

The host simulator replaces mic, speaker, display and button with deterministic
adapters while using the production `CompanionApp` and `MockVoiceBackend`.

```bash
./scripts/check.sh
./build-host/companion_sim
```

Interactive example:

```text
press
tick 20
tick 20
press
tick 250
quit
```

## Wokwi — board/UI POC

`wokwi/diagram.json` connects:

- SSD1306 SDA GPIO41, SCL GPIO42;
- active-low push button GPIO40;
- ESP32-S3 DevKitC-1.

Build the ESP-IDF firmware first, then open the project in Wokwi. The supplied
`wokwi.toml` points to the normal ESP-IDF `.bin` and `.elf` outputs.

Wokwi does not faithfully model the exact INMP441 and MAX98357A acoustic path.
An analog microphone substitute would test a different circuit and is therefore
not used. The real I2S drivers follow Espressif's current standard-mode channel
API and must receive final acceptance on the physical modules.

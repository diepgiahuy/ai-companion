#!/usr/bin/env python3
from pathlib import Path

path = Path("host/companion_software_device/main.cpp")
text = path.read_text(encoding="utf-8")

replacements = [
    (
        '  std::string evidence_path = "software-device-evidence.json";\n',
        '  std::string evidence_path = "software-device-evidence.json";\n  std::string scenario_set = "core";\n',
    ),
    (
        '    else if (arg == "--evidence") evidence_path = value("--evidence");\n    else throw std::runtime_error("unknown argument: " + arg);\n',
        '    else if (arg == "--evidence") evidence_path = value("--evidence");\n    else if (arg == "--scenario-set") scenario_set = value("--scenario-set");\n    else throw std::runtime_error("unknown argument: " + arg);\n',
    ),
    (
        '  std::vector<ScenarioResult> results;\n\n  results.push_back(run_scenario("hello_turn_tts",',
        '  std::vector<ScenarioResult> results;\n\n  if (scenario_set == "core") {\n  results.push_back(run_scenario("hello_turn_tts",',
    ),
    (
        '''  results.push_back(run_scenario("protocol_v1_rejected", [&](ScenarioResult&) {
    require(probe_v1_rejection(host, port, token),
            "v1 probe did not receive unsupported_protocol_version");
  }));

  bool all_passed = true;
''',
        '''  results.push_back(run_scenario("protocol_v1_rejected", [&](ScenarioResult&) {
    require(probe_v1_rejection(host, port, token),
            "v1 probe did not receive unsupported_protocol_version");
  }));
  } else if (scenario_set == "tool") {
    results.push_back(run_scenario("agent_tool_authoritative_mutation", [&](ScenarioResult& result) {
      DeviceFixture fixture(url, token, "software-device-tool");
      fixture.require_ready();
      fixture.begin_audio_turn();
      fixture.finish_audio_turn();
      require(fixture.until([&] { return fixture.app.state() == UiState::ready; }),
              "tool turn did not return ready");
      require(fixture.display.contains("Đã lưu đúng một khoản 50 nghìn"),
              "deterministic model/tool response was not rendered");
      require(fixture.speaker.samples > 0, "tool turn produced no decoded TTS PCM");
      result.counters = stats_json(fixture.backend.stats());
      result.counters["speaker_samples"] = fixture.speaker.samples;
    }));
  } else {
    throw std::runtime_error("unsupported scenario set: " + scenario_set);
  }

  bool all_passed = true;
''',
    ),
    (
        '''  const char* commit = std::getenv("COMPANION_EVIDENCE_COMMIT");
  const char* fingerprint = std::getenv("COMPANION_EVIDENCE_CONFIG_SHA256");
  json evidence{{"schema_version", 1},
''',
        '''  const char* commit = std::getenv("COMPANION_EVIDENCE_COMMIT");
  const char* fingerprint = std::getenv("COMPANION_EVIDENCE_CONFIG_SHA256");
  const json providers = scenario_set == "tool"
                             ? json{{"asr", "mock"}, {"agent", "fake_model"}, {"tts", "mock"}}
                             : json{{"asr", "mock"}, {"agent", "mock"}, {"tts", "mock"}};
  json evidence{{"schema_version", 1},
''',
    ),
    (
        '''                {"device_fsm", "production_companion_app"},
                {"protocol", "v2"},
                {"providers", {{"asr", "mock"}, {"agent", "mock"}, {"tts", "mock"}}},
                {"promotion", "orchestration_only"},
''',
        '''                {"device_fsm", "production_companion_app"},
                {"protocol", "v2"},
                {"scenario_set", scenario_set},
                {"providers", providers},
                {"promotion", "orchestration_only"},
''',
    ),
]

for old, new in replacements:
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"guarded main.cpp target count={count}: {old[:80]!r}")
    text = text.replace(old, new, 1)

path.write_text(text, encoding="utf-8")

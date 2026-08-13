#!/usr/bin/env python3
from pathlib import Path

path = Path("host/companion_software_device/main.cpp")
text = path.read_text()


def replace_once(old: str, new: str, label: str):
    global text
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{label}: expected one match, found {count}")
    text = text.replace(old, new, 1)


replace_once(
'''void require(bool value, std::string_view message) {
  if (!value) throw std::runtime_error(std::string(message));
}

void patch_device_config''',
'''void require(bool value, std::string_view message) {
  if (!value) throw std::runtime_error(std::string(message));
}

http::response<http::string_body> admin_request(const std::string& host, const std::string& port,
                                                const std::string& admin_token, http::verb method,
                                                const std::string& target, const json& body = json::object()) {
  net::io_context io;
  tcp::resolver resolver(io);
  beast::tcp_stream stream(io);
  stream.connect(resolver.resolve(host, port));
  http::request<http::string_body> request{method, target, 11};
  request.set(http::field::host, host);
  request.set(http::field::authorization, "Bearer " + admin_token);
  request.set(http::field::content_type, "application/json");
  if (method != http::verb::delete_) {
    request.body() = body.dump();
    request.prepare_payload();
  }
  http::write(stream, request);
  beast::flat_buffer buffer;
  http::response<http::string_body> response;
  http::read(stream, buffer, response);
  beast::error_code ignored;
  stream.socket().shutdown(tcp::socket::shutdown_both, ignored);
  return response;
}

std::string enroll_device_credential(const std::string& host, const std::string& port,
                                     const std::string& admin_token, const std::string& device_id) {
  const auto response = admin_request(
      host, port, admin_token, http::verb::post,
      "/v1/admin/devices/" + device_id + "/credential",
      json{{"user_id", "tier1-owner"}, {"tenant_id", "tier1"}, {"plan", "test"}});
  require(response.result() == http::status::ok, "device credential enrollment did not return 200");
  const auto payload = json::parse(response.body());
  const std::string credential = payload.value("token", "");
  require(credential.size() >= 16, "device credential enrollment returned no usable credential");
  return credential;
}

void revoke_device_credential(const std::string& host, const std::string& port,
                              const std::string& admin_token, const std::string& device_id) {
  const auto response = admin_request(host, port, admin_token, http::verb::delete_,
                                      "/v1/admin/devices/" + device_id + "/credential");
  require(response.result() == http::status::no_content, "device credential revoke did not return 204");
}

bool probe_auth_rejected(const std::string& host, const std::string& port,
                         const std::string& device_id, const std::string& credential) {
  net::io_context io;
  tcp::resolver resolver(io);
  beast::tcp_stream stream(io);
  stream.connect(resolver.resolve(host, port));
  http::request<http::empty_body> request{http::verb::get, "/v2/device", 11};
  request.set(http::field::host, host);
  request.set(http::field::connection, "Upgrade");
  request.set(http::field::upgrade, "websocket");
  request.set("Sec-WebSocket-Version", "13");
  request.set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==");
  request.set("Device-Id", device_id);
  request.set(http::field::authorization, "Bearer " + credential);
  http::write(stream, request);
  beast::flat_buffer buffer;
  http::response<http::string_body> response;
  http::read(stream, buffer, response);
  return response.result() == http::status::unauthorized;
}

void patch_device_config''',
"insert enrolled auth helpers")

replace_once(
'''bool probe_v1_rejection(const std::string& host, const std::string& port,
                        const std::string& token) {''',
'''bool probe_v1_rejection(const std::string& host, const std::string& port,
                        const std::string& credential, const std::string& device_id) {''',
"v1 probe signature")
replace_once('request.set(http::field::authorization, "Bearer " + token);\n        request.set("Device-Id", "software-device-negative");\n        request.set("Client-Id", "software-device-negative");',
             'request.set(http::field::authorization, "Bearer " + credential);\n        request.set("Device-Id", device_id);\n        request.set("Client-Id", device_id);',
             "v1 probe credential headers")

replace_once('  std::string token = "tier1-device-token";\n', '', "remove default shared token")
replace_once('    else if (arg == "--token") token = value("--token");\n', '', "remove token cli")

for device, label in [
    ("software-device-happy", "happy"),
    ("software-device-duplicate", "duplicate"),
    ("software-device-barge", "barge"),
    ("software-device-reconnect", "reconnect"),
]:
    replace_once(
        f'DeviceFixture fixture(url, token, "{device}");',
        f'const std::string device = "{device}";\n    const std::string credential = enroll_device_credential(host, port, admin_token, device);\n    DeviceFixture fixture(url, credential, device);',
        f"{label} credential")

replace_once(
'''    const std::string device = "software-device-config";
    DeviceFixture fixture(url, token, device);''',
'''    const std::string device = "software-device-config";
    const std::string credential = enroll_device_credential(host, port, admin_token, device);
    DeviceFixture fixture(url, credential, device);''',
"config credential")

replace_once(
'''  results.push_back(run_scenario("protocol_v1_rejected", [&](ScenarioResult&) {
    require(probe_v1_rejection(host, port, token),
            "v1 probe did not receive unsupported_protocol_version");
  }));''',
'''  results.push_back(run_scenario("protocol_v1_rejected", [&](ScenarioResult&) {
    const std::string device = "software-device-v1-negative";
    const std::string credential = enroll_device_credential(host, port, admin_token, device);
    require(probe_v1_rejection(host, port, credential, device),
            "v1 probe did not receive unsupported_protocol_version");
  }));

  results.push_back(run_scenario("enrolled_auth_rejects_wrong_and_revoked", [&](ScenarioResult&) {
    const std::string device = "software-device-auth-negative";
    const std::string credential = enroll_device_credential(host, port, admin_token, device);
    require(probe_auth_rejected(host, port, device, "wrong-tier1-device-credential"),
            "wrong per-device credential was not rejected before WebSocket upgrade");
    revoke_device_credential(host, port, admin_token, device);
    require(probe_auth_rejected(host, port, device, credential),
            "revoked per-device credential was not rejected before WebSocket upgrade");
  }));''',
"auth negative scenario")

replace_once(
'''      DeviceFixture fixture(url, token, "software-device-tool");''',
'''      const std::string device = "software-device-tool";
      const std::string credential = enroll_device_credential(host, port, admin_token, device);
      DeviceFixture fixture(url, credential, device);''',
"tool credential")

replace_once(
'''  const json providers = scenario_set == "tool"
                             ? json{{"asr", "mock"}, {"agent", "fake_model"}, {"tts", "mock"}}
                             : json{{"asr", "mock"}, {"agent", "mock"}, {"tts", "mock"}};''',
'''  const json providers = json{{"asr", "mock"}, {"agent", "adk_fake_model"}, {"tts", "mock"}};''',
"provider evidence")

path.write_text(text)

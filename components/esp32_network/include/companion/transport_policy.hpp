#pragma once

#include <string_view>

namespace companion {

// Product firmware has no plaintext transport fallback. Local development that
// needs ws:// belongs in host/software-device harnesses, not the flashed device.
constexpr bool secure_product_transport(std::string_view url,
                                        std::string_view credential) {
  return !credential.empty() && url.size() > 6 && url.starts_with("wss://");
}

} // namespace companion

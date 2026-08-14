#include "companion/transport_policy.hpp"

#include <cassert>

using companion::secure_product_transport;

int main() {
  static_assert(secure_product_transport("wss://companion.example/v2/device", "device-token"));
  static_assert(!secure_product_transport("ws://companion.example/v2/device", "device-token"));
  static_assert(!secure_product_transport("http://companion.example/v2/device", "device-token"));
  static_assert(!secure_product_transport("https://companion.example/v2/device", "device-token"));
  static_assert(!secure_product_transport("wss://companion.example/v2/device", ""));
  static_assert(!secure_product_transport("", "device-token"));
  assert(secure_product_transport("wss://127.0.0.1/v2/device", "x"));
  return 0;
}

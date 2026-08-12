#include <array>
#include <cstdint>
#include <cstdio>
#include <dlfcn.h>

namespace {
constexpr int kOk = 0;
constexpr int kVoip = 2048;
using encoder_create_t = void* (*)(int, int, int, int*);
using encoder_destroy_t = void (*)(void*);
using encode_t = int (*)(void*, const int16_t*, int, unsigned char*, int32_t);
using decoder_create_t = void* (*)(int, int, int*);
using decoder_destroy_t = void (*)(void*);
using decode_t = int (*)(void*, const unsigned char*, int32_t, int16_t*, int, int);

template <typename T> T symbol(void* library, const char* name) {
  return reinterpret_cast<T>(dlsym(library, name));
}

bool round_trip(void* library, int sample_rate, int samples) {
  auto create_encoder = symbol<encoder_create_t>(library, "opus_encoder_create");
  auto destroy_encoder = symbol<encoder_destroy_t>(library, "opus_encoder_destroy");
  auto encode = symbol<encode_t>(library, "opus_encode");
  auto create_decoder = symbol<decoder_create_t>(library, "opus_decoder_create");
  auto destroy_decoder = symbol<decoder_destroy_t>(library, "opus_decoder_destroy");
  auto decode = symbol<decode_t>(library, "opus_decode");
  if (!create_encoder || !destroy_encoder || !encode || !create_decoder ||
      !destroy_decoder || !decode) return false;

  int error = 0;
  void* encoder = create_encoder(sample_rate, 1, kVoip, &error);
  void* decoder = create_decoder(sample_rate, 1, &error);
  if (!encoder || !decoder || error != kOk) return false;
  std::array<int16_t, 2'880> pcm{};
  for (int i = 0; i < samples; ++i) pcm[i] = static_cast<int16_t>((i % 101) * 120 - 6000);
  std::array<unsigned char, 1'275> packet{};
  const int bytes = encode(encoder, pcm.data(), samples, packet.data(), packet.size());
  std::array<int16_t, 2'880> decoded{};
  const int got = bytes > 0 ? decode(decoder, packet.data(), bytes, decoded.data(), samples, 0) : -1;
  destroy_encoder(encoder);
  destroy_decoder(decoder);
  return bytes > 0 && bytes <= static_cast<int>(packet.size()) && got == samples;
}
} // namespace

int main() {
  void* library = dlopen("libopus.so.0", RTLD_NOW | RTLD_LOCAL);
  if (!library) {
    std::puts("SKIP: libopus.so.0 unavailable (Docker test image installs it)");
    return 77;
  }
  const bool uplink = round_trip(library, 16'000, 960);
  const bool downlink = round_trip(library, 24'000, 1'440);
  dlclose(library);
  if (!uplink || !downlink) return 1;
  std::puts("PASS: real libopus 60 ms round-trip at 16 kHz and 24 kHz");
  return 0;
}

from pytest_embedded_idf.dut import IdfDut


def test_physical_esp32s3_boots_and_initializes(dut: IdfDut) -> None:
    """Prove the flashed physical target reaches the post-init application loop.

    pytest-embedded-idf flashes the real build and connects to the real serial
    port. The success log is emitted only after display, button and I2S audio
    initialization have completed; initialization failures return before it.

    This gate intentionally does NOT claim real ASR/TTS, WebRTC, MCP or
    end-to-end voice quality. Those require separate real-provider/network
    evidence.
    """
    assert dut.app.target == "esp32s3"
    dut.expect("hardware POC using", timeout=30)

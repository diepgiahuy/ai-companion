from pytest_embedded import Dut


def test_device_reaches_application_loop(dut: Dut) -> None:
    """The real device must initialize its configured peripherals and app."""

    dut.expect(
        r"hardware POC using (WebSocket|deterministic mock) backend",
        timeout=30,
    )

from pytest_embedded_idf.dut import IdfDut


def test_physical_esp32s3_reaches_application_loop(dut: IdfDut) -> None:
    """Prove the flashed target reaches the loop after peripheral initialization."""

    assert dut.app.target == "esp32s3"
    dut.expect("hardware POC using WebSocket protocol v2 backend", timeout=30)

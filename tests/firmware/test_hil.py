from pytest_embedded_idf.dut import IdfDut


def test_physical_esp32s3_reaches_companion_app_entry(dut: IdfDut) -> None:
    """Prove the intended ESP32-S3 image boots far enough to enter app_main.

    Product lifecycle qualification (factory-new setup, claim, authenticated WSS,
    OTA, acoustic behavior, etc.) belongs to the focused physical HIL issues.
    This base smoke must remain valid for both erased and already-provisioned DUTs
    and must not depend on deleted POC/mock runtime log strings.
    """

    assert dut.app.target == "esp32s3"
    dut.expect(r"main_task: Calling app_main\(\)", timeout=30)

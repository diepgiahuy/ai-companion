import pytest
from pytest_embedded import Dut

def test_device_boot(dut: Dut):
    """
    Test that the device boots up properly and initializes without crashing.
    """
    # Wait for the bootloader and app start
    dut.expect('app_main: Firmware booted', timeout=10)

def test_bump_to_pair(dut: Dut):
    """
    Test the Bump-to-Pair logic using pytest-embedded.
    """
    # Simulate a fake BLE RSSI spike via a console command (assuming a mock command exists for testing)
    dut.write('mock_ble_rssi -45')
    
    # Assert that the device responds with the pairing log
    dut.expect('Pairing successful', timeout=5)

def test_voice_mail_trigger(dut: Dut):
    """
    Test that holding the button triggers the voice mail recording.
    """
    # Simulate GPIO button hold
    dut.write('mock_gpio_hold 40')
    
    # Check for the correct state transition
    dut.expect('Recording Voice Mail...', timeout=3)
    
    # Release the button
    dut.write('mock_gpio_release 40')
    
    # Check that it saved the file
    dut.expect('Voice Mail saved to FIFO', timeout=5)

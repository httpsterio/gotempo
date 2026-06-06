import asyncio
from bleak import BleakClient, BleakScanner

MAC          = "24:AC:AC:18:41:CC"
HR_CHAR_UUID = "00002a37-0000-1000-8000-00805f9b34fb"

async def main():
    print("Scanning...")
    device = await BleakScanner.find_device_by_address(MAC, timeout=10.0)
    if device is None:
        print("Device not found")
        return
    print(f"Found: {device.name} ({device.address})")

    async with BleakClient(device) as client:
        print("Connected, services resolved")
        print("Services:", [str(s.uuid) for s in client.services])

        def handler(sender, data: bytearray):
            flags = data[0]
            bpm = int.from_bytes(data[1:3], "little") if flags & 1 else data[1]
            print(f"BPM: {bpm}")

        await client.start_notify(HR_CHAR_UUID, handler, bluez={"use_start_notify": False})
        print("Subscribed — waiting for data...")
        await asyncio.sleep(30)

asyncio.run(main())

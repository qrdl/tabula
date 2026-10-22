# Tabula firmware

Firmware for the [Waveshare ESP32-S3 PhotoPainter](https://www.waveshare.com/wiki/ESP32-S3-PhotoPainter). It connects to Wi-Fi, downloads the task list image from the Tabula server and draws it on the 7.3" e-paper panel. It is written in C for ESP-IDF 5.5.

## What you need

| Item | Notes |
|---|---|
| ESP32-S3 PhotoPainter | Waveshare's board with the 7.3" Spectra 6 panel, 16 MB flash and 8 MB PSRAM |
| USB-C cable | For power, flashing and logs. The device runs on USB power |
| ESP-IDF 5.5 | Tested with 5.5.6. Install it with Espressif's [Linux and macOS guide](https://docs.espressif.com/projects/esp-idf/en/v5.5.6/esp32s3/get-started/linux-macos-setup.html) or the [Get Started](https://docs.espressif.com/projects/esp-idf/en/v5.5.6/esp32s3/get-started/index.html) page for other systems |
| Serial port access | On Linux, your user must be in the `dialout` group: [Espressif's instructions](https://docs.espressif.com/projects/esp-idf/en/v5.5.6/esp32s3/get-started/establish-serial-connection.html#adding-user-to-dialout-or-uucp-on-linux) |
| 2.4 GHz Wi-Fi | The ESP32-S3 has no 5 GHz radio |
| Tabula server | Running and reachable from that Wi-Fi network; see the [main README](../README.md#server) |

## Settings

Your settings go in `sdkconfig.local`. Start from the example:

```bash
cp sdkconfig.local.example sdkconfig.local
```

| Setting | Must be set? | Default | Meaning |
|---|---|---|---|
| `CONFIG_TABULA_WIFI_SSID` | Yes | empty | Wi-Fi network name |
| `CONFIG_TABULA_WIFI_PASSWORD` | Yes, unless the network is open | empty | Wi-Fi password. Empty = open network |
| `CONFIG_TABULA_SERVER_URL` | If the default doesn't reach your server | `http://tabula:1337` | The device downloads `<URL>/api/image.bin`. A host name is looked up on every request, so it must resolve on your network, e.g. through your router's DNS. `https://` also works for servers with certificates from a public CA ([certificate bundle](https://docs.espressif.com/projects/esp-idf/en/v5.5.6/esp32s3/api-reference/protocols/esp_crt_bundle.html)) |
| `CONFIG_TABULA_SERVER_USER` | Only if the server uses auth | empty | The server's `auth.user`. Empty = no auth |
| `CONFIG_TABULA_SERVER_PASSWORD` | Only if the server uses auth | empty | The server's `auth.password` |
| `CONFIG_TABULA_MIN_REDRAW_INTERVAL_S` | No | `180` | Minimum seconds between redraws. Waveshare advises at least 180 s |
| `CONFIG_TABULA_BUTTON_MIN_REDRAW_INTERVAL_S` | No | `30` | Minimum seconds between redraws after a button press |
| `CONFIG_TABULA_DAILY_REDRAW_HOURS` | No | `24` | Redraw the same image at least this often. Waveshare advises once every 24 h |

The same settings are under **Tabula display** in `idf.py menuconfig`, but changes made there only last until `sdkconfig` is next regenerated. Put lasting changes in `sdkconfig.local`.

### Configuration files

| File | Holds | In git |
|---|---|---|
| `sdkconfig.defaults` | Board settings: 16 MB flash, octal PSRAM, partition table, logs on the USB port, network host name `photopainter` | Yes |
| `sdkconfig.local` | This device's Wi-Fi, server URL and server user/password | No (it holds the Wi-Fi password) |
| `sdkconfig` | The full configuration the build uses, generated from the two files above | No (it holds the Wi-Fi password) |

Defaults files only fill in options that `sdkconfig` doesn't have yet ([ESP-IDF docs](https://docs.espressif.com/projects/esp-idf/en/v5.5.6/esp32s3/api-guides/build-system.html#custom-sdkconfig-defaults)). After editing `sdkconfig.local` or `sdkconfig.defaults`, delete `sdkconfig` so it's regenerated:

```bash
rm sdkconfig && idf.py build
```

## Build

```bash
. ~/esp/esp-idf/export.sh      # once per terminal; the path depends on where ESP-IDF is installed
cd firmware
idf.py build
```

Or run `make firmware` from the top folder, which loads ESP-IDF itself ([Make](../README.md#make)).

## Flash

The board appears as `/dev/ttyACM0` on Linux (USB ID `303a:1001`). Replace the port below if yours differs.

1. **Back up the board first.** This copies the whole 16 MB flash, including Waveshare's original firmware, so it can be restored at any time ([`read_flash`](https://docs.espressif.com/projects/esptool/en/release-v4/esp32s3/esptool/basic-commands.html#read-flash-contents-read-flash)):

   ```bash
   esptool.py --chip esp32s3 -p /dev/ttyACM0 -b 460800 read_flash 0 0x1000000 photopainter-backup.bin
   ```

2. Flash and watch the log ([`idf.py flash`](https://docs.espressif.com/projects/esp-idf/en/v5.5.6/esp32s3/api-guides/tools/idf-py.html#flash-the-project-flash), [IDF Monitor](https://docs.espressif.com/projects/esp-idf/en/v5.5.6/esp32s3/api-guides/tools/idf-monitor.html)):

   ```bash
   idf.py -p /dev/ttyACM0 flash monitor     # Ctrl+] leaves the monitor
   ```

   `make flash` from the top folder flashes the same way, without opening the log.

   After a restart the log shows `wifi: connected, address …`, then `fetch: new image …` once the image has been downloaded.

If the board doesn't respond to flashing, put it into download mode: unplug it, hold **BOOT**, plug it back in, then release BOOT ([boot modes](https://docs.espressif.com/projects/esptool/en/release-v4/esp32s3/advanced-topics/boot-mode-selection.html)). The chip's loader is in read-only memory, so a bad flash can't break the board for good.

### From another computer

Make one file that holds the bootloader, partition table and app ([`merge-bin`](https://docs.espressif.com/projects/esp-idf/en/v5.5.6/esp32s3/api-guides/tools/idf-py.html#merge-binaries-merge-bin)):

```bash
idf.py merge-bin -o tabula-display-merged.bin    # written to build/
```

Write it at address `0x0`, either with `esptool.py write_flash 0x0 tabula-display-merged.bin` or in the browser with Espressif's [ESP Launchpad](https://espressif.github.io/esp-launchpad/), as described in Waveshare's [flashing instructions](https://www.waveshare.com/wiki/ESP32-S3-PhotoPainter#Firmware_Flashing_Instructions).

### Restoring the original firmware

From your backup ([`write_flash`](https://docs.espressif.com/projects/esptool/en/release-v4/esp32s3/esptool/basic-commands.html#write-binary-data-to-flash-write-flash)):

```bash
esptool.py --chip esp32s3 -p /dev/ttyACM0 -b 460800 write_flash 0x0 photopainter-backup.bin
```

Without a backup, use Waveshare's published firmware from [`03_Firmware`](https://github.com/waveshareteam/ESP32-S3-PhotoPainter/tree/main/03_Firmware) (`ESP32-S3-PhotoPainter-IDF5.5.2.bin`), also written at `0x0`.

## What the device does

- On boot and every Wi-Fi reconnect: downloads `/api/image.bin?rotation=<rotation>` (header `X-Device: photopainter`, so the server learns its address) and draws it.
- `POST /refresh` on port 80: downloads again; a 304 (unchanged) means no redraw.
- **BOOT** (short press): downloads and redraws, even if nothing changed.
- **KEY** (short press): turns the image 90° clockwise. Press twice for 180°. It waits 3 s after the last press, then redraws once. The rotation is kept across restarts.
- The green LED blinks on each press. Long presses are ignored; holding BOOT while plugging in still enters download mode.
- Redraws are at least 180 s apart, or 30 s after a button press. A request that comes sooner waits; it isn't dropped.
- Redraws the same image every 24 h.
- The panel is woken for each redraw and put into deep sleep afterwards. Waveshare's manual warns that leaving it powered damages it.
- A failed download is retried every 30 s.

| Log message | Fix |
|---|---|
| `no Wi-Fi network set` | Set `CONFIG_TABULA_WIFI_SSID` in `sdkconfig.local`, then `rm sdkconfig && idf.py build` and flash |
| `disconnected (reason …), retrying` | Check the Wi-Fi name and password, and that the network is 2.4 GHz |
| `wrong or missing server user/password` | Set `CONFIG_TABULA_SERVER_USER` and `CONFIG_TABULA_SERVER_PASSWORD` to the server's `auth` values |
| `…/api/image.bin: ESP_ERR_HTTP_CONNECT` | The server isn't reachable at `CONFIG_TABULA_SERVER_URL`: check that it's running and the host name resolves |

## Hardware references

| Document | Used for |
|---|---|
| [PhotoPainter wiki](https://www.waveshare.com/wiki/ESP32-S3-PhotoPainter) | Board overview, buttons, flashing |
| [PhotoPainter schematic](https://files.waveshare.com/wiki/ESP32-S3-PhotoPainter/ESP32-S3-PhotoPainter-Schematic.pdf) | Pins and power wiring |
| [Waveshare's PhotoPainter code](https://github.com/waveshareteam/ESP32-S3-PhotoPainter/tree/main/01_Example/xiaozhi-esp32) | Panel pins and start-up commands (`main/epd.c`), button and LED pins (`main/buttons.c`), AXP2101 power chip setup (`main/power.c`) |
| [7.3" e-Paper (E) manual](https://www.waveshare.com/wiki/7.3inch_e-Paper_HAT_(E)_Manual#Precautions) | Panel care: 180 s between refreshes, a refresh every 24 h, sleep when idle |
| [7.3" e-Paper (E) user manual (PDF)](https://files.waveshare.com/wiki/7.3inch-e-Paper-HAT-(E)/7.3inch-e-Paper-(E)-user-manual.pdf) | Panel datasheet |
| [ESP32-S3 datasheet](https://www.espressif.com/sites/default/files/documentation/esp32-s3_datasheet_en.pdf) | The chip |
| [ESP-IDF Programming Guide v5.5.6](https://docs.espressif.com/projects/esp-idf/en/v5.5.6/esp32s3/index.html) | Everything about the toolkit |

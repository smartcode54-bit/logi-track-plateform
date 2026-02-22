# Run LogiTrack Mobile on a Physical Device

## Prerequisites

- Flutter SDK installed
- Android device with **USB debugging** enabled
- USB cable to connect device to your computer

---

## Step 1: Enable USB debugging on your phone

1. Open **Settings** → **About phone**
2. Tap **Build number** 7 times until you see "You are now a developer"
3. Go back to **Settings** → **Developer options**
4. Turn on **USB debugging**
5. (Optional) Turn on **Install via USB** if you use some Xiaomi/OPPO devices

---

## Step 2: Connect the device

1. Connect your phone to the PC with a USB cable
2. On the phone, when prompted **"Allow USB debugging?"** → tap **Allow** (optionally check "Always allow from this computer")
3. Unlock the phone and leave the screen on if it asks for authorization

---

## Step 3: Check that Flutter sees the device

Open a terminal in the project and run:

```bash
cd logitrack-mobile
flutter devices
```

You should see your phone listed, for example:

```
25078PC3EG (mobile) • RG79MFIJZLY9MFTS • android-arm64 • Android 15 (API 35)
```

If the device does **not** appear:

- Try another USB cable (some are charge-only)
- Try another USB port
- Re-enable USB debugging and reconnect
- On Windows: ensure [USB drivers](https://developer.android.com/studio/run/oem-usb) for your phone are installed (often automatic for popular brands)

---

## Step 4: Run the app on the physical device

**Option A – Let Flutter pick the device (if only one is connected):**

```bash
cd logitrack-mobile
flutter run
```

**Option B – Specify the device by ID:**

```bash
cd logitrack-mobile
flutter run -d <device_id>
```

Use the **device ID** from `flutter devices` (e.g. `RG79MFIJZLY9MFTS`):

```bash
flutter run -d RG79MFIJZLY9MFTS
```

**Option C – Release build (smaller, faster on device):**

```bash
flutter run --release
```

---

## Step 5: While the app is running

- **r** – Hot reload  
- **R** – Hot restart  
- **q** – Quit and stop the app on the device  

---

## Troubleshooting

| Problem | What to try |
|--------|---------------------|
| Device not in `flutter devices` | Reconnect USB, allow debugging on phone, try another cable/port |
| "No devices found" | Install/update USB driver for your phone (Windows) |
| Build fails (Android) | Run `flutter doctor` and fix any Android toolchain / license issues |
| App installs but crashes | Check log with `flutter run` and read the stack trace in the terminal |

---

## First-time Android licenses (if needed)

If `flutter doctor` reports missing Android licenses:

```bash
flutter doctor --android-licenses
```

Accept the licenses when prompted.

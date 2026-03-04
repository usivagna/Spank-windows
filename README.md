# spank-windows

Slap your laptop, it yells back. **Windows port** of [taigrr/spank](https://github.com/taigrr/spank).

Uses the Windows Sensor API (COM `ISensorManager`) to detect physical hits on your laptop and plays audio responses. Single binary, no dependencies.

## Requirements

- Windows 10/11 on a laptop with a built-in accelerometer (Surface, Lenovo, HP, Dell, and others with motion sensors)
- The **Windows Sensor Service** must be running (`services.msc` → Sensor Service → Start)

## Install

Download from the [latest release](https://github.com/usivagna/Spank-windows/releases/latest).

Or build from source:

```powershell
go install github.com/usivagna/Spank-windows@latest
```

## Usage

```powershell
# Normal mode — says "ow!" when slapped
spank.exe

# Sexy mode — escalating responses based on slap frequency
spank.exe --sexy

# Halo mode — plays Halo death sounds when slapped
spank.exe --halo

# Custom mode — plays your own MP3 files from a directory
spank.exe --custom C:\path\to\mp3s

# Adjust sensitivity (lower = more sensitive)
spank.exe --min-amplitude 0.1
spank.exe --min-amplitude 0.25
spank.exe --sexy --min-amplitude 0.2
```

### Modes

**Pain mode** (default): Randomly plays from 10 pain/protest audio clips when a slap is detected.

**Sexy mode** (`--sexy`): Tracks slaps within a rolling window. The more you slap, the more intense the audio response. 60 levels of escalation.

**Halo mode** (`--halo`): Randomly plays death sound effects from the Halo video game series when a slap is detected.

**Custom mode** (`--custom`): Randomly plays MP3 files from a custom directory you specify.

### Sensitivity

Control detection sensitivity with `--min-amplitude` (default: 0.3):

- Lower values (e.g., 0.05–0.10): Very sensitive, detects light taps
- Medium values (e.g., 0.15–0.30): Balanced sensitivity
- Higher values (e.g., 0.30–0.50): Only strong impacts trigger sounds

The value represents the minimum acceleration amplitude (in g-force) required to trigger a sound.

## Building from Source

### Prerequisites

- Go 1.23+
- Python 3 (for generating placeholder audio, or supply your own MP3s)

### Steps

```powershell
# Clone
git clone https://github.com/usivagna/Spank-windows.git
cd Spank-windows

# Generate placeholder audio files (or copy real ones from the original repo)
python scripts\generate_audio.py

# Build
go build -o spank.exe .
```

### Cross-compiling from Linux/macOS

```bash
./setup.sh
GOOS=windows GOARCH=amd64 go build -o spank.exe .
```

## Running as a Service

To have spank start automatically at login, create a scheduled task:

```powershell
schtasks /create /tn "Spank" /tr "C:\path\to\spank.exe" /sc onlogon /rl highest
```

To remove:

```powershell
schtasks /delete /tn "Spank" /f
```

## How it works

1. Reads accelerometer data via the Windows Sensor API (`ISensorManager` COM interface)
2. Runs vibration detection (STA/LTA, CUSUM, kurtosis, peak/MAD) — same algorithms as the original
3. When a significant impact is detected, plays an embedded MP3 response
4. 750ms cooldown between responses to prevent rapid-fire

### Differences from the macOS version

| | macOS (original) | Windows (this port) |
|---|---|---|
| Sensor API | IOKit HID (Apple SPU) | Windows Sensor API (COM) |
| Data transport | POSIX shared memory ring buffer | Direct COM polling |
| Privilege | Requires `sudo` | No elevation needed |
| Architecture | Apple Silicon only | x86_64 and ARM64 |
| Accelerometer | Bosch BMI286 IMU | Any Windows-compatible accelerometer |

## Credits

This is a Windows port of [**spank**](https://github.com/taigrr/spank) by [Tai Groot](https://github.com/taigrr).

The vibration detection algorithms (STA/LTA, CUSUM, kurtosis, peak/MAD) are ported from [taigrr/apple-silicon-accelerometer](https://github.com/taigrr/apple-silicon-accelerometer), which itself was ported from [olvvier/apple-silicon-accelerometer](https://github.com/olvvier/apple-silicon-accelerometer).

## License

MIT — see [LICENSE](LICENSE) for details.

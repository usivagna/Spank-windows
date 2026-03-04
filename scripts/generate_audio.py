# Generate placeholder MP3 files for development/testing.
# Run this before building: python3 scripts/generate_audio.py
#
# For production use, replace the generated files with real audio from:
# https://github.com/taigrr/spank (audio/ directory)

import os
import struct

def make_mp3_frame():
    """Create a minimal valid MPEG1 Layer3 frame (silence)."""
    # MPEG1, Layer3, 128kbps, 44100Hz, mono, no padding
    # Frame sync: 0xFFE0 (11 bits) + version(2) + layer(2) + protection(1)
    # MPEG1=11, Layer3=01, no CRC=1 -> 0xFF 0xFB
    # Bitrate index=1001 (128kbps), SampleRate=00 (44100), padding=0, private=0 -> 0x90 0x00
    header = b'\xFF\xFB\x90\x00'
    # Frame size for 128kbps MPEG1 Layer3 at 44100Hz = 417 bytes (with header)
    padding = b'\x00' * 413
    return header + padding

def main():
    frame = make_mp3_frame()
    # Repeat frames to make a short but valid MP3 file
    mp3_data = frame * 20  # ~0.5 seconds of silence

    dirs = {
        'audio/pain': 10,
        'audio/sexy': 60,
        'audio/halo': 9,
    }

    for directory, count in dirs.items():
        os.makedirs(directory, exist_ok=True)
        for i in range(1, count + 1):
            path = os.path.join(directory, f'{i:02d}.mp3')
            with open(path, 'wb') as f:
                f.write(mp3_data)
            print(f'created {path}')

    print('\nPlaceholder audio files created successfully.')
    print('Replace with real audio files for production use.')

if __name__ == '__main__':
    main()

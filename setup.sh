#!/bin/bash
# Setup script for Spank-windows (cross-compilation from Linux/macOS)
# Generates placeholder MP3 files needed for building

set -e

echo "Generating placeholder audio files..."
python3 scripts/generate_audio.py

echo ""
echo "Setup complete! Cross-compile for Windows with:"
echo "  GOOS=windows GOARCH=amd64 go build -o spank.exe ."

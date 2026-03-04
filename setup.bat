@echo off
REM Setup script for Spank-windows
REM Generates placeholder MP3 files needed for building

echo Generating placeholder audio files...
python scripts\generate_audio.py
if %ERRORLEVEL% neq 0 (
    echo.
    echo Python is required to generate placeholder audio files.
    echo Install Python from https://python.org or manually place MP3 files in:
    echo   audio\pain\  ^(10 files^)
    echo   audio\sexy\  ^(60 files^)
    echo   audio\halo\  ^(9 files^)
    exit /b 1
)

echo.
echo Setup complete! You can now build with:
echo   go build -o spank.exe .

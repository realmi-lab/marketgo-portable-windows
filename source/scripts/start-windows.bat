@echo off
setlocal DisableDelayedExpansion
title MarketGO Portable - Python
pushd "%~dp0" || exit /b 1
if not exist "runtime\python.exe" goto missing
if not exist "app\server.py" goto missing
if not exist "web\index.html" goto missing
if not exist "logs" mkdir "logs"
echo Starting MarketGO Portable - Python engine.
echo Python is bundled. No installation is required.
echo Keep this window open. Press Ctrl+C to stop.
echo Local address: http://127.0.0.1:20560/
echo Logs: logs\runtime.log and logs\launcher.log
"%CD%\runtime\python.exe" -I -X utf8 "%CD%\app\server.py" > "logs\launcher.log" 2>&1
set "result=%ERRORLEVEL%"
if not "%result%"=="0" (
  echo MarketGO stopped. Close any older MarketGO execution window.
  echo Details:
  type "logs\launcher.log"
  pause
)
popd
exit /b %result%
:missing
echo Extract the entire ZIP before running start.bat.
echo The runtime, app and web folders must remain beside start.bat.
pause
popd
exit /b 1

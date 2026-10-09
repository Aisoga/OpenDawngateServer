@echo off
rem Starts the game against the local server (re\scripts\start_client.ps1). Start-Server.cmd must be running.
cd /d "%~dp0"
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0re\scripts\start_client.ps1" %*
if errorlevel 1 pause

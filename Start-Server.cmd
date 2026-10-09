@echo off
rem Starts the local Dawngate server (re\scripts\start_server.ps1). Then double-click Start-Client.cmd.
cd /d "%~dp0"
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0re\scripts\start_server.ps1" %*
if errorlevel 1 pause

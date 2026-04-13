@echo off
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0gen-cert.ps1"
pause

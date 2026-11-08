@echo off
cd /d "%~dp0"
start "" "..\..\ringmaster-gui.exe" -config "%~dp0ringmaster.yaml"

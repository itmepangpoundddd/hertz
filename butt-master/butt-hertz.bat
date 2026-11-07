@echo off
setlocal
set "HERE=%~dp0"
if not exist "%HERE%butt.exe" (
  echo butt.exe was not found next to this script.
  echo Build butt first or copy butt.exe into this folder.
  pause
  exit /b 1
)

rem Keep the user's normal butt config untouched. Use a temporary HERTZ preset.
set "CFG=%TEMP%\butt-hertz.buttrc"
if not exist "%CFG%" copy "%HERE%hertz.buttrc" "%CFG%" >nul

start "" "%HERE%butt.exe" -c "%CFG%"

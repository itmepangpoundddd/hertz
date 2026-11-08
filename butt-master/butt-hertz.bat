@echo off
setlocal
set "HERE=%~dp0"
set "APP=%HERE%Hertz-butt.exe"
if not exist "%APP%" set "APP=%HERE%butt.exe"
if not exist "%APP%" (
  echo Hertz-butt.exe was not found next to this script.
  echo Build the app first or copy Hertz-butt.exe into this folder.
  pause
  exit /b 1
)

rem Keep the user's normal config untouched. Use a temporary HERTZ preset.
set "CFG=%TEMP%\hertz-butt.buttrc"
if not exist "%CFG%" copy "%HERE%hertz.buttrc" "%CFG%" >nul

start "" "%APP%" -c "%CFG%"

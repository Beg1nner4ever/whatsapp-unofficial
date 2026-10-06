@echo off
rem Windows launcher for the whatsapp-unofficial binary.
rem launch.ps1 downloads and verifies the binary (SHA-256 against
rem ..\checksums.txt) and prints only its path; this wrapper then runs it with
rem all arguments. Diagnostics go to stderr; stdout belongs to the MCP server.
setlocal EnableExtensions DisableDelayedExpansion

if defined WHATSAPP_UNOFFICIAL_BIN (
  "%WHATSAPP_UNOFFICIAL_BIN%" %*
  exit /b %ERRORLEVEL%
)

set "WAU_EXE="
for /f "usebackq delims=" %%P in (`powershell.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "%~dp0launch.ps1"`) do set "WAU_EXE=%%P"

if not defined WAU_EXE (
  echo whatsapp-unofficial launcher: error: could not prepare the binary, see messages above 1>&2
  exit /b 1
)

"%WAU_EXE%" %*
exit /b %ERRORLEVEL%

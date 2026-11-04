@echo off
setlocal
set "GDTEST_ROOT=%~dp0"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" (
  set "GDTEST_BIN=windows-arm64\gdtest.exe"
) else if /I "%PROCESSOR_ARCHITECTURE%"=="AMD64" (
  set "GDTEST_BIN=windows-x86_64\gdtest.exe"
) else (
  echo gdtest: unsupported Windows architecture %PROCESSOR_ARCHITECTURE% 1>&2
  exit /b 2
)
"%GDTEST_ROOT%bin\%GDTEST_BIN%" %*
exit /b %ERRORLEVEL%

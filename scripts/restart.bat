@echo off
rem 智观 BI · 重启（Windows）—— 转发给 scripts\restart.ps1
rem 双击运行即可；也可在终端传参：restart.bat -SkipFrontend
setlocal
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0restart.ps1" %*
set RC=%ERRORLEVEL%
if "%~1"=="" (
  echo.
  pause
)
exit /b %RC%

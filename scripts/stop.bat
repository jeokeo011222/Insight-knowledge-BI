@echo off
rem 智观 BI · 停止（Windows）—— 转发给 scripts\stop.ps1
rem 双击运行即可；也可在终端传参：stop.bat -IncludeInfra
setlocal
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0stop.ps1" %*
set RC=%ERRORLEVEL%
if "%~1"=="" (
  echo.
  pause
)
exit /b %RC%

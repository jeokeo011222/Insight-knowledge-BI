@echo off
rem 智观 BI · 启动（Windows）—— 转发给 scripts\start.ps1
rem 双击运行即可；也可在终端传参：start.bat -SkipBuild
setlocal
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0start.ps1" %*
set RC=%ERRORLEVEL%
if "%~1"=="" (
  echo.
  pause
)
exit /b %RC%

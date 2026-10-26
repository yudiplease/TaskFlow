@echo off
rem Полная сборка десктоп-версии: exe с иконкой/DPI-манифестом + установщик.
rem Требования: Go, Inno Setup (ISCC), goversioninfo (go install .../goversioninfo@latest)

setlocal
cd /d "%~dp0.."

echo [1/4] Генерация ресурса (иконка, версия, DPI-манифест)...
"%GOPATH%\bin\goversioninfo.exe" -icon=build/icon.ico -application-icon=build/icon.ico -manifest=build/manifest.xml -o cmd/desktop/resource_windows_amd64.syso build/versioninfo.json
if errorlevel 1 goto :err

echo [2/4] Сборка TaskFlow.exe (без окна консоли)...
go build -trimpath -ldflags="-s -w -H windowsgui" -o bin\TaskFlow.exe ./cmd/desktop
if errorlevel 1 goto :err

echo [3/4] Компиляция установщика...
set ISCC=%LOCALAPPDATA%\Programs\InnoSetup\ISCC.exe
if not exist "%ISCC%" set ISCC=%ProgramFiles(x86)%\Inno Setup 7\ISCC.exe
if not exist "%ISCC%" set ISCC=%ProgramFiles(x86)%\Inno Setup 6\ISCC.exe
"%ISCC%" build\installer.iss
if errorlevel 1 goto :err

echo [4/4] Готово: dist\TaskFlow-Setup-1.3.1.exe
exit /b 0

:err
echo СБОЙ СБОРКИ
exit /b 1

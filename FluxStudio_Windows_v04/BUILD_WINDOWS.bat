@echo off
setlocal
cd /d "%~dp0"
where go >nul 2>nul || (echo Go 1.23+ fehlt. & pause & exit /b 1)
where python >nul 2>nul || (echo Python 3 wird nur zum Einbetten des Icons benoetigt. & pause & exit /b 1)
set GOOS=windows
set GOARCH=amd64
set CGO_ENABLED=0
go build -trimpath -ldflags="-H=windowsgui -s -w" -o FluxStudio_unpatched.exe .
if errorlevel 1 goto fail
python embed_icon.py FluxStudio_unpatched.exe FluxStudio_AI.ico FluxStudio.exe
if errorlevel 1 goto fail
del FluxStudio_unpatched.exe
echo Fertig: FluxStudio.exe
pause
exit /b 0
:fail
echo Build fehlgeschlagen.
pause
exit /b 1

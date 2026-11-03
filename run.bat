@echo off
chcp 65001 >nul
cd /d "%~dp0"
echo.
echo   Smart Factory Go Server
echo   ========================
echo.

REM Check credentials
if not exist "credentials.json" (
    echo [!] ไม่พบ credentials.json - จะรันแบบ offline
    echo.
)

REM Build if needed
if not exist "smartfactory.exe" (
    echo [*] กำลัง Build Go binary...
    go build -o smartfactory.exe .
    if errorlevel 1 (
        echo [X] Build ไม่สำเร็จ!
        pause
        exit /b 1
    )
    echo [+] Build สำเร็จ!
)

echo [+] เริ่ม server ที่ http://localhost:8080
echo [i] กด Ctrl+C เพื่อหยุด
echo.

REM Auto-open browser
start "" "http://localhost:8080"

REM Start server
smartfactory.exe
pause


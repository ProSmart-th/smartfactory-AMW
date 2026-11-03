@echo off
chcp 65001 >nul
echo ============================================================
echo   🚀 Smart Factory - One-Click Deploy to GitHub ^& Render
echo ============================================================
echo.

cd /d "%~dp0"

echo [1/3] 🔨 กำลังคอมไพล์โปรแกรมสำหรับ Linux...
set CGO_ENABLED=0
set GOOS=linux
go build -ldflags="-w -s" -o smartfactory .
if %ERRORLEVEL% NEQ 0 (
    echo ❌ คอมไพล์ไม่ผ่าน กรุณาตรวจสอบโค้ด
    pause
    exit /b %ERRORLEVEL%
)
echo ✅ คอมไพล์ Linux binary สำเร็จ!
echo.

echo [2/3] 📦 กำลังบันทึกการแก้ไขลง Git...
git add .
set /p commit_msg="กรอกข้อความการแก้ไข (กด Enter ได้เลย): "
if "%commit_msg%"=="" set commit_msg=Update code and templates %date% %time%
git commit -m "%commit_msg%"
echo.

echo [3/3] 🌐 กำลังส่งขึ้น GitHub ^& Render...
git push origin main
if %ERRORLEVEL% EQU 0 (
    echo.
    echo ============================================================
    echo   🎉 ส่งขึ้น GitHub สำเร็จแล้ว!
    echo   Render.com จะทำการอัปเดตเว็บให้อัตโนมัติในอีก 1-2 นาที
    echo ============================================================
) else (
    echo.
    echo ❌ ส่งไม่สำเร็จ กรุณาตรวจสอบการเชื่อมต่ออินเทอร์เน็ต
)
echo.
pause

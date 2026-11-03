# Smart Factory Go Server - Run Script
# วิธีใช้: ดับเบิ้ลคลิก หรือรันใน PowerShell

$ErrorActionPreference = "Stop"
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $scriptDir

# ตรวจสอบ credentials.json
if (-not (Test-Path "credentials.json")) {
    Write-Host ""
    Write-Host "⚠️  ไม่พบไฟล์ credentials.json" -ForegroundColor Yellow
    Write-Host "📖 กรุณาอ่าน README.md เพื่อสร้าง Service Account Key" -ForegroundColor Cyan
    Write-Host "ℹ️  จะรันแอปด้วยข้อมูล offline (ค่าเป็น 0)" -ForegroundColor Gray
    Write-Host ""
}

# Build ถ้ายังไม่มี binary
if (-not (Test-Path "smartfactory.exe")) {
    Write-Host "🔨 กำลัง Build..." -ForegroundColor Cyan
    go build -o smartfactory.exe .
    if ($LASTEXITCODE -ne 0) {
        Write-Host "❌ Build ไม่สำเร็จ" -ForegroundColor Red
        Read-Host "กด Enter เพื่อปิด"
        exit 1
    }
    Write-Host "✅ Build สำเร็จ" -ForegroundColor Green
}

# กำหนด port
$port = if ($env:PORT) { $env:PORT } else { "8080" }

Write-Host ""
Write-Host "🚀 Smart Factory Server กำลังเริ่มต้น..." -ForegroundColor Green
Write-Host "🌐 เปิดเบราว์เซอร์ที่: http://localhost:$port" -ForegroundColor Cyan
Write-Host "⛔ กด Ctrl+C เพื่อหยุด server" -ForegroundColor Gray
Write-Host ""

# เปิดเบราว์เซอร์อัตโนมัติหลังจาก 2 วิ
Start-Job -ScriptBlock {
    Start-Sleep -Seconds 2
    Start-Process "http://localhost:$using:port"
} | Out-Null

# รัน server
.\smartfactory.exe


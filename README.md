# Smart Factory - Go Web App

แอปพลิเคชันเว็บ Smart Factory ที่เขียนด้วยภาษา **Go** แสดงผล Dashboard และ Analytics ของเครื่องจักร โดยดึงข้อมูลจาก **Google Sheets API** แบบ Real-time

## ⚙️ หน้าตั้งค่าลิงก์ชีท (Settings)

ระบบมีหน้า **"⚙️ ตั้งค่าชีท"** ที่แถบเมนูด้านบน (หรือเข้าทาง `/?page=Settings`):
1. **วางลิงก์แชร์ของแต่ละเครื่องได้โดยตรง** (เช่น ลิงก์ของ `SmartFactory_FH004`, `SmartFactory_LS001` ฯลฯ)
2. วางได้ทั้งลิงก์เต็ม `https://docs.google.com/spreadsheets/d/.../edit` หรือเฉพาะ Spreadsheet ID
3. ระบุชื่อ Tab ได้ (หรือปล่อยว่างเพื่อให้อ่าน Tab แรกอัตโนมัติ)
4. มีปุ่ม **"🔍 ทดสอบ"** ตรวจสอบว่าระบบอ่านไฟล์ชีทนั้นได้ถูกต้องหรือไม่
5. กดปุ่ม **"💾 บันทึกการตั้งค่าทั้งหมด"** ค่าจะถูกบันทึกเก็บไว้ในไฟล์ `config.json` ในเครื่องอัตโนมัติ ไม่ต้องกรอกใหม่ทุกครั้ง

## 📁 โครงสร้างไฟล์

```
smartfactory-go/
├── main.go               # Go backend server
├── config.json           # ⚙️ ไฟล์บันทึกการตั้งค่าลิงก์ชีทแต่ละเครื่อง
├── credentials.json      # 🔑 Service Account Key (สำหรับอ่านชีท)
├── templates/
│   └── index.html        # Frontend HTML + JS (Dashboard & Settings)
├── Dockerfile            # สำหรับ Deploy ขึ้น Google Cloud Run
└── smartfactory.exe      # Binary พร้อมรันทันที
```

## 🔑 ขั้นตอนการตั้งค่า Google Sheets API

### 1. สร้าง Service Account

1. เปิด [Google Cloud Console](https://console.cloud.google.com/)
2. สร้างหรือเลือก Project
3. ไปที่ **APIs & Services > Library** → ค้นหา **Google Sheets API** → กด **Enable**
4. ไปที่ **APIs & Services > Credentials** → **Create Credentials > Service Account**
5. ตั้งชื่อ (เช่น `smartfactory-reader`) → กด **Create and Continue** → **Done**
6. คลิก Service Account ที่สร้าง → ไปที่ tab **Keys** → **Add Key > Create new key > JSON**
7. บันทึกไฟล์ JSON ที่ดาวน์โหลดมาเป็น **`credentials.json`** ใน folder นี้

### 2. แชร์ Google Spreadsheet ให้ Service Account

1. เปิด [Smart Factory Spreadsheet](https://docs.google.com/spreadsheets/d/1I4Qw1hPol8gGLZbSZS3hVRpsG6o--3f2iswfXJh1t5g)
2. คลิก **Share** (มุมบนขวา)
3. ใส่ **email ของ Service Account** (รูปแบบ: `xxx@project.iam.gserviceaccount.com`)
4. ตั้งสิทธิ์เป็น **Viewer** → กด **Send**

## 🚀 วิธีรันแอป

### ตัวเลือก A: รัน Binary โดยตรง

```powershell
# 1. Build
cd "d:\DataP\Smart Factory\smartfactory-go"
go build -o smartfactory.exe .

# 2. รัน
.\smartfactory.exe

# 3. เปิดเบราว์เซอร์
# http://localhost:8080
```

### ตัวเลือก B: รันด้วย `go run`

```powershell
cd "d:\DataP\Smart Factory\smartfactory-go"
go run .
```

### ตัวเลือก C: กำหนด Port อื่น

```powershell
$env:PORT="9090"
.\smartfactory.exe
# http://localhost:9090
```

## 🌐 API Endpoints

| Endpoint | Method | คำอธิบาย |
|---|---|---|
| `/` | GET | SPA Frontend (Dashboard) |
| `/api/fleet` | GET | สถานะภาพรวมทุกเครื่อง |
| `/api/machine/{id}` | GET | ข้อมูล Analytics เครื่อง (เช่น `/api/machine/LS001`) |
| `/api/machine/{id}?from=2026-10-01&to=2026-10-31` | GET | กรองตามช่วงวันที่ |
| `/api/machine/{id}?month=2026-10` | GET | กรองตามเดือน |
| `/events?machine=LS001` | GET (SSE) | Real-time stream ของเครื่องนั้น |
| `/events` | GET (SSE) | Real-time stream ของ Fleet Overview |
| `/health` | GET | Health check |

### ตัวอย่าง API Response

```json
GET /api/machine/LS001?month=2026-10

{
  "id": "LS001",
  "label": "LS-001",
  "powerKwh": "123.45",
  "costBaht": "617.25",
  "daysWorked": "20",
  "targetDays": "27",
  "availability": "78.50",
  "downtime": "21.50",
  "dateFrom": "2026-10-01",
  "dateTo": "2026-10-31",
  "dateLabels": ["10-01","10-02",...],
  "runtimeHourly": [0,0,...,45,60,...],
  "prodDaily": [8.5, 7.2,...],
  "energyDaily": [12.3, 11.1,...]
}
```

## ⚡ Real-time Update

แอปใช้ **Server-Sent Events (SSE)** สำหรับ real-time:
- อัปเดตทุก **30 วินาที** อัตโนมัติ
- ดู live badge (🟢) ที่มุมบนขวาเมื่อเชื่อมต่อสำเร็จ
- Cache อายุ **5 นาที** เพื่อลด API calls ไปยัง Google

## 📊 หน้าที่มีในระบบ

| หน้า | URL | คำอธิบาย |
|---|---|---|
| Factory Overview | `/?page=Home` | สถานะภาพรวมทุกเครื่อง (Running/Stopped) |
| LS001 Analytics | `/?page=LS001` | Dashboard เครื่อง LS001 |
| LS002 Analytics | `/?page=LS002` | Dashboard เครื่อง LS002 |
| TP003 Analytics | `/?page=TP003` | Dashboard เครื่อง TP003 |
| HC003 Analytics | `/?page=HC003` | Dashboard เครื่อง HC003 (CNC Cutter) |
| HC004 Analytics | `/?page=HC004` | Dashboard เครื่อง HC004 (CNC Cutter) |
| FH004 Analytics | `/?page=FH004` | Dashboard เครื่อง FH004 |
| FH005 Analytics | `/?page=FH005` | Dashboard เครื่อง FH005 |

## 🔧 Configuration

แก้ไขค่าใน `main.go`:

```go
const (
    SpreadsheetID         = "1I4Qw1hPol8gGLZbSZS3hVRpsG6o--3f2iswfXJh1t5g" // Sheets ID
    ElectricityRatePerKwh = 5.00   // บาท/kWh
    CacheMaxAge           = 5 * time.Minute  // อายุ cache
)
```

## ❓ Troubleshooting

| ปัญหา | วิธีแก้ |
|---|---|
| `Google Sheets service ไม่พร้อม` | ตรวจสอบว่า `credentials.json` อยู่ใน folder เดียวกับ `main.go` |
| ข้อมูลไม่แสดง / ค่าเป็น 0 ทั้งหมด | ตรวจสอบว่า Service Account ได้รับสิทธิ์ Viewer ใน Spreadsheet แล้ว |
| Port 8080 ถูกใช้งานอยู่ | เปลี่ยน port: `$env:PORT="9090"` |
| `credentials.json` ไม่มี | ดูขั้นตอน "สร้าง Service Account" ด้านบน |


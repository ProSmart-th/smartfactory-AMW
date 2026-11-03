package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	sheets "google.golang.org/api/sheets/v4"
)

// ============================================================
// Constants & Configuration
// ============================================================

const (
	ConfigFile            = "config.json"
	ElectricityRatePerKwh = 5.00
	DefaultCutCycleSec    = 4.92 // วินาทีต่อครั้ง สำหรับเครื่องตัด
	DefaultFoldCycleSec   = 2.00 // วินาทีต่อครั้ง สำหรับเครื่องพับ (FH004, FH005)
	CacheMaxAge           = 5 * time.Minute // cache อายุ 5 นาที
)

var DefaultMachineIDs = []string{"LS001", "LS002", "TP003", "HC003", "HC004", "FH004", "FH005"}
var cutterMachines = map[string]bool{"HC003": true, "HC004": true}
var folderMachines = map[string]bool{"FH004": true, "FH005": true}

// MachineConfig เก็บการตั้งค่าชีทและรอบเวลาของแต่ละเครื่อง
type MachineConfig struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	SheetURL        string  `json:"sheetUrl"`
	SpreadsheetID   string  `json:"spreadsheetId"`
	SheetTab        string  `json:"sheetTab"` // ชนิด/ชื่อ Tab (ถ้าเว้นว่างจะอ่าน Tab แรกอัตโนมัติ)
	CycleTimeSec    float64 `json:"cycleTimeSec,omitempty"` // เวลาต่อครั้ง (วินาที/ครั้ง) สำหรับเครื่องตัดและพับ
	PhaseMultiplier float64 `json:"phaseMultiplier,omitempty"` // ตัวคูณระบบไฟฟ้า (เช่น 3.0 สำหรับเครื่อง 3 เฟสที่วัด 1 เฟส)
}

// UserAccount ข้อมูลผู้ใช้งานสำหรับเข้าระบบตั้งค่า
type UserAccount struct {
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	Role     string `json:"role"`
}

// AppConfig การตั้งค่าหลักทั้งระบบ
type AppConfig struct {
	DefaultSpreadsheetID string                   `json:"defaultSpreadsheetId"`
	ElectricityRate      float64                  `json:"electricityRate,omitempty"` // อัตราค่าไฟฟ้า (บาท/หน่วย) เช่น 5.00
	Machines             map[string]MachineConfig `json:"machines"`
	Users                map[string]UserAccount   `json:"users,omitempty"`
}

// ============================================================
// Data Models
// ============================================================

// MachineData ข้อมูลที่ส่งให้ frontend
type MachineData struct {
	ID                 string    `json:"id"`
	Label              string    `json:"label"`
	Status             string    `json:"status"` // Real-time status (RUNNING, STOPPED)
	PowerKwh           string    `json:"powerKwh"`
	CostBaht           string    `json:"costBaht"`
	DaysWorked         string    `json:"daysWorked"`
	TargetDays         string    `json:"targetDays"`
	TotalCuts          *string   `json:"totalCuts"`
	TotalCount         *string   `json:"totalCount"`
	CounterLabel       string    `json:"counterLabel"`
	CycleTimeSec       float64   `json:"cycleTimeSec"` // วินาทีต่อครั้งที่ใช้คำนวณ
	IsCutter           bool      `json:"isCutter"`
	IsFolder           bool      `json:"isFolder"`
	Availability       string    `json:"availability"`
	Downtime           string    `json:"downtime"`
	ShiftHours         string    `json:"shiftHours"`
	SelectedMonth      string    `json:"selectedMonth"`
	DateFrom           string    `json:"dateFrom"`
	DateTo             string    `json:"dateTo"`
	DateLabels         []string  `json:"dateLabels"`
	RuntimeHourly      []float64 `json:"runtimeHourly"`
	RuntimeToday       []float64 `json:"runtimeToday"`
	ProdDaily          []float64 `json:"prodDaily"`
	ProdDailyFormatted []string  `json:"prodDailyFormatted"`
	CurrentLine        []float64 `json:"currentLine"`
	CurrentTimes       []string  `json:"currentTimes"`
	CurrentHourly      []float64 `json:"currentHourly"`
	CurrentDate           string    `json:"currentDate"`
	EnergyDaily           []float64 `json:"energyDaily"`
	DailyCount            []float64 `json:"dailyCount"`
	ElectricityRate       float64   `json:"electricityRate"`
	PhaseMultiplier       float64   `json:"phaseMultiplier"`
	RuntimeTodayFormatted string    `json:"runtimeTodayFormatted,omitempty"`
	TotalRuntimeFormatted string    `json:"totalRuntimeFormatted,omitempty"`
}

// FleetMachine ข้อมูลสรุปของเครื่องจักรแต่ละเครื่องในภาพรวม
type FleetMachine struct {
	ID                    string  `json:"id"`
	Label                 string  `json:"label"`
	Status                string  `json:"status"`
	PowerKwh              string  `json:"powerKwh"`
	CostBaht              string  `json:"costBaht"`
	DaysWorked            string  `json:"daysWorked"`
	TargetDays            string  `json:"targetDays"`
	TotalCount            *string `json:"totalCount"`
	CounterLabel          string  `json:"counterLabel"`
	Availability          string  `json:"availability"`
	Downtime              string  `json:"downtime"`
	IsCutter              bool    `json:"isCutter"`
	IsFolder              bool    `json:"isFolder"`
	RuntimeTodayFormatted string  `json:"runtimeTodayFormatted,omitempty"`
	TotalRuntimeFormatted string  `json:"totalRuntimeFormatted,omitempty"`
}

// FleetStatus สถานะภาพรวมทุกเครื่อง
type FleetStatus struct {
	Total       int            `json:"total"`
	Running     int            `json:"running"`
	Stopped     int            `json:"stopped"`
	Month       string         `json:"month"`
	DateFrom    string         `json:"dateFrom"`
	DateTo      string         `json:"dateTo"`
	TotalEnergy string         `json:"totalEnergy"`
	TotalCost   string         `json:"totalCost"`
	Fleet       []FleetMachine `json:"fleet"`
}

// CacheEntry สำหรับ cache ข้อมูล
type CacheEntry struct {
	Data      interface{}
	ExpiresAt time.Time
}

// ============================================================
// Global State
// ============================================================

var (
	sheetsService *sheets.Service
	cache         = map[string]CacheEntry{}
	appConfig     AppConfig
)

func getCycleTime(machineID string) float64 {
	if cfg, ok := appConfig.Machines[machineID]; ok && cfg.CycleTimeSec > 0 {
		return cfg.CycleTimeSec
	}
	if folderMachines[machineID] {
		return DefaultFoldCycleSec
	}
	if cutterMachines[machineID] {
		return DefaultCutCycleSec
	}
	return 0
}

func getElectricityRate() float64 {
	if appConfig.ElectricityRate > 0 {
		return appConfig.ElectricityRate
	}
	return ElectricityRatePerKwh
}

func getPhaseMultiplier(machineID string) float64 {
	if m, ok := appConfig.Machines[machineID]; ok && m.PhaseMultiplier > 0 {
		return m.PhaseMultiplier
	}
	return 3.0 // ค่าเริ่มต้นสำหรับเครื่อง 3 เฟสที่วัด 1 เฟส
}

func initConfig() {
	appConfig = AppConfig{
		DefaultSpreadsheetID: "1I4Qw1hPol8gGLZbSZS3hVRpsG6o--3f2iswfXJh1t5g",
		ElectricityRate:      ElectricityRatePerKwh,
		Machines:             make(map[string]MachineConfig),
		Users:                make(map[string]UserAccount),
	}

	// ถ้ามีไฟล์ config.json ให้อ่านมาใช้
	if data, err := os.ReadFile(ConfigFile); err == nil {
		if err := json.Unmarshal(data, &appConfig); err == nil {
			log.Println("✅ โหลดการตั้งค่าจาก config.json สำเร็จ")
			changed := false
			if appConfig.ElectricityRate <= 0 {
				appConfig.ElectricityRate = ElectricityRatePerKwh
				changed = true
			}
			if appConfig.Users == nil {
				appConfig.Users = make(map[string]UserAccount)
				changed = true
			}
			if _, ok := appConfig.Users["admin"]; !ok {
				appConfig.Users["admin"] = UserAccount{
					Username: "admin",
					Password: "admin",
					Role:     "ผู้ดูแลระบบ (Administrator)",
				}
				changed = true
			}
			for id, m := range appConfig.Machines {
				if m.PhaseMultiplier <= 0 {
					m.PhaseMultiplier = 3.0 // ค่าเริ่มต้นไฟ 3 เฟส
					appConfig.Machines[id] = m
					changed = true
				}
				if m.CycleTimeSec <= 0 {
					if folderMachines[id] {
						m.CycleTimeSec = DefaultFoldCycleSec
						appConfig.Machines[id] = m
						changed = true
					} else if cutterMachines[id] {
						m.CycleTimeSec = DefaultCutCycleSec
						appConfig.Machines[id] = m
						changed = true
					}
				}
			}
			if changed {
				saveConfig()
			}
			return
		}
	}

	// ถ้าไม่มี ให้ตั้งค่าเริ่มต้น
	if appConfig.Users == nil {
		appConfig.Users = make(map[string]UserAccount)
	}
	appConfig.Users["admin"] = UserAccount{
		Username: "admin",
		Password: "admin",
		Role:     "ผู้ดูแลระบบ (Administrator)",
	}
	appConfig.ElectricityRate = ElectricityRatePerKwh
	for _, id := range DefaultMachineIDs {
		cycle := 0.0
		if folderMachines[id] {
			cycle = DefaultFoldCycleSec
		} else if cutterMachines[id] {
			cycle = DefaultCutCycleSec
		}
		appConfig.Machines[id] = MachineConfig{
			ID:              id,
			Name:            "SmartFactory_" + id,
			SheetURL:        "",
			SpreadsheetID:   "",
			SheetTab:        "",
			CycleTimeSec:    cycle,
			PhaseMultiplier: 3.0,
		}
	}
	saveConfig()
}

func saveConfig() error {
	data, err := json.MarshalIndent(appConfig, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ConfigFile, data, 0644)
}

func extractSpreadsheetID(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	// ตรวจสอบแบบ URL: /spreadsheets/d/([a-zA-Z0-9-_]+)
	re := regexp.MustCompile(`/spreadsheets/d/([a-zA-Z0-9-_]+)`)
	matches := re.FindStringSubmatch(input)
	if len(matches) > 1 {
		return matches[1]
	}
	// ถ้าเป็น ID ตรงๆ
	if regexp.MustCompile(`^[a-zA-Z0-9-_]{20,100}$`).MatchString(input) {
		return input
	}
	return input
}

func getMachineSpreadsheetID(machineID string) string {
	if m, ok := appConfig.Machines[machineID]; ok && m.SpreadsheetID != "" {
		return m.SpreadsheetID
	}
	return appConfig.DefaultSpreadsheetID
}

func getMachineReadRange(machineID string) string {
	if m, ok := appConfig.Machines[machineID]; ok && m.SheetTab != "" {
		return fmt.Sprintf("'%s'!A:G", m.SheetTab)
	}
	// ถ้าเครื่องนี้มีไฟล์แยกของตัวเอง และไม่ได้ระบุ Tab ให้อ่านชีทแรก
	if m, ok := appConfig.Machines[machineID]; ok && m.SpreadsheetID != "" {
		return "A:G"
	}
	// กรณี fallback ไปยังชีทรวม ให้ใช้ชื่อ machineID เป็นชื่อ Tab
	return fmt.Sprintf("%s!A:G", machineID)
}

func getMachineTabName(machineID string) string {
	if m, ok := appConfig.Machines[machineID]; ok && m.SheetTab != "" {
		return m.SheetTab
	}
	// ถ้าเป็นไฟล์แยกของเครื่อง ให้ดึง tab แรก หรือชื่อ DataLogger
	if m, ok := appConfig.Machines[machineID]; ok && m.SpreadsheetID != "" {
		return ""
	}
	return machineID
}

// fetchSheetRows ดึงข้อมูลจากชีท รองรับทั้งผ่าน Service Account และผ่านลิงก์แชร์สาธารณะ (ไม่ต้องใช้ credentials)
func fetchSheetRows(sheetID, tabName string) ([][]interface{}, error) {
	// วิธีที่ 1: ถ้ามี Service Account ให้ดึงผ่าน Google Sheets API
	if sheetsService != nil {
		readRange := "A:G"
		if tabName != "" {
			readRange = fmt.Sprintf("'%s'!A:G", tabName)
		}
		resp, err := sheetsService.Spreadsheets.Values.Get(sheetID, readRange).Do()
		if err == nil && len(resp.Values) > 0 {
			return resp.Values, nil
		}
	}

	// วิธีที่ 2: ดึงตรงผ่าน Google Sheets Web Export (ไม่ต้องพึ่ง Service Account แค่แชร์เป็น "ทุกคนที่มีลิงก์ดูได้")
	return fetchSheetCSV(sheetID, tabName)
}

func fetchSheetCSV(sheetID, tabName string) ([][]interface{}, error) {
	sheetID = extractSpreadsheetID(sheetID)
	if sheetID == "" {
		return nil, fmt.Errorf("Spreadsheet ID ไม่ถูกต้อง")
	}

	exportURL := fmt.Sprintf("https://docs.google.com/spreadsheets/d/%s/gviz/tq?tqx=out:csv", sheetID)
	if tabName != "" {
		exportURL += "&sheet=" + url.QueryEscape(tabName)
	}

	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Get(exportURL)
	if err != nil {
		return nil, fmt.Errorf("เชื่อมต่อกับ Google ไม่สำเร็จ: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("ชีทนี้ยังไม่ได้เปิดสิทธิ์: กรุณากดปุ่ม 'แชร์' แล้วเปลี่ยนเป็น 'ทุกคนที่มีลิงก์' -> 'มีสิทธิ์ดู'")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Google ตอบกลับรหัส %d", resp.StatusCode)
	}

	reader := csv.NewReader(resp.Body)
	reader.LazyQuotes = true
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("อ่านข้อมูล CSV ไม่สำเร็จ: %w", err)
	}

	var result [][]interface{}
	for _, record := range records {
		var row []interface{}
		for _, cell := range record {
			row = append(row, cell)
		}
		result = append(result, row)
	}
	return result, nil
}

// ============================================================
// Main Entry Point
// ============================================================

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// เริ่มต้นการตั้งค่า
	initConfig()

	// เริ่มต้น Google Sheets service
	if err := initSheetsService(); err != nil {
		log.Printf("⚠️  Google Sheets service ไม่พร้อม: %v", err)
		log.Println("ℹ️  จะใช้ข้อมูล mock สำหรับการแสดงผล")
	}

	// Routes
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/api/fleet", handleFleet)
	mux.HandleFunc("/api/machine/", handleMachine)
	mux.HandleFunc("/api/config", handleConfig)
	mux.HandleFunc("/api/config/test", handleTestSheet)
	mux.HandleFunc("/api/credentials", handleCredentials)
	mux.HandleFunc("/api/auth/login", handleLogin)
	mux.HandleFunc("/api/auth/users", handleUsers)
	mux.HandleFunc("/events", handleSSE) // Server-Sent Events
	mux.HandleFunc("/health", handleHealth)

	// Static files
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))

	log.Printf("🚀 Smart Factory Go Server เริ่มที่ http://localhost:%s", port)
	log.Fatal(http.ListenAndServe(":"+port, corsMiddleware(mux)))
}

// ============================================================
// Google Sheets Service Initialization
// ============================================================

func initSheetsService() error {
	ctx := context.Background()

	// ลองอ่าน service account key จากไฟล์ credentials.json
	credFile := "credentials.json"
	if _, err := os.Stat(credFile); err == nil {
		data, err := os.ReadFile(credFile)
		if err != nil {
			return fmt.Errorf("อ่าน credentials.json ไม่ได้: %w", err)
		}
		// JWTConfigFromJSON สำหรับ Service Account key file
		conf, err := google.JWTConfigFromJSON(data, sheets.SpreadsheetsReadonlyScope)
		if err != nil {
			// ลอง CredentialsFromJSONWithParams สำหรับ OAuth2 client
			creds, err2 := google.CredentialsFromJSONWithParams(ctx, data, google.CredentialsParams{
				Scopes: []string{sheets.SpreadsheetsReadonlyScope},
			})
			if err2 != nil {
				return fmt.Errorf("parse credentials ไม่ได้ (JWT: %v, OAuth: %v)", err, err2)
			}
			svc, err3 := sheets.NewService(ctx, option.WithCredentials(creds))
			if err3 != nil {
				return fmt.Errorf("สร้าง Sheets service ไม่ได้: %w", err3)
			}
			sheetsService = svc
			log.Println("✅ Google Sheets API พร้อมใช้งาน (OAuth2 credentials.json)")
			return nil
		}
		client := conf.Client(ctx)
		svc, err := sheets.NewService(ctx, option.WithHTTPClient(client))
		if err != nil {
			return fmt.Errorf("สร้าง Sheets service ไม่ได้: %w", err)
		}
		sheetsService = svc
		log.Println("✅ Google Sheets API พร้อมใช้งาน (Service Account)")
		return nil
	}

	// ลอง Application Default Credentials (gcloud auth)
	creds, err := google.FindDefaultCredentials(ctx, sheets.SpreadsheetsReadonlyScope)
	if err != nil {
		return fmt.Errorf("ไม่พบ credentials: %w", err)
	}
	svc, err := sheets.NewService(ctx, option.WithCredentials(creds))
	if err != nil {
		return fmt.Errorf("สร้าง Sheets service ไม่ได้: %w", err)
	}
	sheetsService = svc
	log.Println("✅ Google Sheets API พร้อมใช้งาน (Application Default Credentials)")
	return nil
}

// ============================================================
// HTTP Handlers
// ============================================================

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		safeConfig := appConfig
		safeUsers := make(map[string]UserAccount)
		for k, u := range appConfig.Users {
			safeUsers[k] = UserAccount{Username: u.Username, Role: u.Role}
		}
		safeConfig.Users = safeUsers
		json.NewEncoder(w).Encode(safeConfig)
		return
	}

	if r.Method == http.MethodPost {
		var incoming AppConfig
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}

		if incoming.ElectricityRate > 0 {
			appConfig.ElectricityRate = incoming.ElectricityRate
		}

		if incoming.DefaultSpreadsheetID != "" {
			appConfig.DefaultSpreadsheetID = extractSpreadsheetID(incoming.DefaultSpreadsheetID)
		}

		for id, m := range incoming.Machines {
			extractedID := extractSpreadsheetID(m.SheetURL)
			if extractedID == "" {
				extractedID = extractSpreadsheetID(m.SpreadsheetID)
			}
			m.ID = id
			m.SpreadsheetID = extractedID
			if m.Name == "" {
				m.Name = "SmartFactory_" + id
			}
			if m.CycleTimeSec <= 0 {
				if existing, ok := appConfig.Machines[id]; ok && existing.CycleTimeSec > 0 {
					m.CycleTimeSec = existing.CycleTimeSec
				} else if folderMachines[id] {
					m.CycleTimeSec = DefaultFoldCycleSec
				} else if cutterMachines[id] {
					m.CycleTimeSec = DefaultCutCycleSec
				}
			}
			if m.PhaseMultiplier <= 0 {
				if existing, ok := appConfig.Machines[id]; ok && existing.PhaseMultiplier > 0 {
					m.PhaseMultiplier = existing.PhaseMultiplier
				} else {
					m.PhaseMultiplier = 3.0
				}
			}
			appConfig.Machines[id] = m
		}

		if err := saveConfig(); err != nil {
			http.Error(w, `{"error":"failed to save config"}`, http.StatusInternalServerError)
			return
		}

		// เคลียร์แคชทั้งหมดเพื่อให้โหลดข้อมูลใหม่ทันที
		cache = map[string]CacheEntry{}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "success",
			"message": "บันทึกการตั้งค่าเรียบร้อยแล้ว",
		})
		return
	}

	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

// ============================================================
// Auth & User Management Handlers
// ============================================================

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type UserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	req.Password = strings.TrimSpace(req.Password)

	user, ok := appConfig.Users[req.Username]
	if !ok || user.Password != req.Password {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "ชื่อผู้ใช้หรือรหัสผ่านไม่ถูกต้อง กรุณาลองใหม่อีกครั้ง",
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"username": user.Username,
		"role":     user.Role,
	})
}

func handleUsers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		var list []UserAccount
		for _, u := range appConfig.Users {
			list = append(list, UserAccount{
				Username: u.Username,
				Role:     u.Role,
			})
		}
		sort.Slice(list, func(i, j int) bool {
			return list[i].Username < list[j].Username
		})
		json.NewEncoder(w).Encode(list)

	case http.MethodPost:
		var req UserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}

		req.Username = strings.TrimSpace(req.Username)
		req.Password = strings.TrimSpace(req.Password)
		req.Role = strings.TrimSpace(req.Role)

		if req.Username == "" {
			http.Error(w, `{"error":"กรุณาระบุชื่อผู้ใช้งาน"}`, http.StatusBadRequest)
			return
		}
		if req.Role == "" {
			req.Role = "ผู้ดูแลระบบ (Administrator)"
		}

		if appConfig.Users == nil {
			appConfig.Users = make(map[string]UserAccount)
		}

		existing, exists := appConfig.Users[req.Username]
		if exists {
			// แก้ไขผู้ใช้เดิม
			if req.Password != "" {
				existing.Password = req.Password
			}
			existing.Role = req.Role
			appConfig.Users[req.Username] = existing
		} else {
			// เพิ่มผู้ใช้ใหม่
			if req.Password == "" {
				http.Error(w, `{"error":"กรุณากำหนดรหัสผ่านสำหรับผู้ใช้ใหม่"}`, http.StatusBadRequest)
				return
			}
			appConfig.Users[req.Username] = UserAccount{
				Username: req.Username,
				Password: req.Password,
				Role:     req.Role,
			}
		}

		if err := saveConfig(); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"บันทึกไฟล์ไม่สำเร็จ: %v"}`, err), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "บันทึกข้อมูลผู้ใช้งานเรียบร้อยแล้ว",
		})

	case http.MethodDelete:
		username := strings.TrimSpace(r.URL.Query().Get("username"))
		if username == "" {
			http.Error(w, `{"error":"กรุณาระบุชื่อผู้ใช้ที่ต้องการลบ"}`, http.StatusBadRequest)
			return
		}
		if username == "admin" && len(appConfig.Users) <= 1 {
			http.Error(w, `{"error":"ไม่สามารถลบบัญชี admin หลักได้ เนื่องจากเป็นผู้ดูแลระบบคนสุดท้าย"}`, http.StatusBadRequest)
			return
		}
		if _, exists := appConfig.Users[username]; !exists {
			http.Error(w, `{"error":"ไม่พบบัญชีผู้ใช้งานนี้"}`, http.StatusNotFound)
			return
		}

		delete(appConfig.Users, username)
		if err := saveConfig(); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"บันทึกไฟล์ไม่สำเร็จ: %v"}`, err), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "ลบบัญชีผู้ใช้งานสำเร็จ",
		})

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func getServiceAccountEmail() string {
	if data, err := os.ReadFile("credentials.json"); err == nil {
		var sa struct {
			ClientEmail string `json:"client_email"`
		}
		if err := json.Unmarshal(data, &sa); err == nil && sa.ClientEmail != "" {
			return sa.ClientEmail
		}
	}
	return ""
}

func handleCredentials(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodGet {
		email := getServiceAccountEmail()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"installed":   sheetsService != nil,
			"clientEmail": email,
		})
		return
	}

	if r.Method == http.MethodPost {
		// รองรับการส่ง JSON ของ credentials
		var req struct {
			CredentialsRaw string `json:"credentialsRaw"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}

		rawBytes := []byte(strings.TrimSpace(req.CredentialsRaw))
		// ตรวจสอบความถูกต้องของ JSON เบื้องต้น
		var sa struct {
			Type        string `json:"type"`
			ClientEmail string `json:"client_email"`
		}
		if err := json.Unmarshal(rawBytes, &sa); err != nil || sa.ClientEmail == "" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "ไฟล์หรือข้อความ JSON ไม่ถูกต้อง (ต้องเป็น Service Account Key ที่มี client_email)",
			})
			return
		}

		// บันทึกลง credentials.json
		if err := os.WriteFile("credentials.json", rawBytes, 0600); err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "บันทึกไฟล์ credentials.json ไม่สำเร็จ: " + err.Error(),
			})
			return
		}

		// เริ่มต้น Sheets Service ใหม่ทันที
		if err := initSheetsService(); err != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "สร้าง Sheets Service ไม่สำเร็จ: " + err.Error(),
			})
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":     true,
			"clientEmail": sa.ClientEmail,
			"message":     "ติดตั้ง credentials.json สำเร็จเรียบร้อยแล้ว!",
		})
		return
	}

	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

func handleTestSheet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SheetURL string `json:"sheetUrl"`
		SheetTab string `json:"sheetTab"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}

	sheetID := extractSpreadsheetID(req.SheetURL)
	if sheetID == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "กรุณาระบุลิงก์แชร์ของ Google Sheets",
		})
		return
	}

	// ทดสอบอ่านข้อมูล
	rows, err := fetchSheetRows(sheetID, req.SheetTab)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	rowCount := len(rows)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"sheetId":  sheetID,
		"rowCount": rowCount,
		"message":  fmt.Sprintf("เชื่อมต่อชีทสำเร็จ อ่านได้ %d แถว", rowCount),
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := "ok"
	sheetsOK := sheetsService != nil
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       status,
		"sheets_ready": sheetsOK,
		"time":         time.Now().Format(time.RFC3339),
	})
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	// รองรับ ?page=LS001 เป็นต้น
	page := r.URL.Query().Get("page")
	if page == "" {
		page = "Home"
	}
	http.ServeFile(w, r, "./templates/index.html")
}

func handleFleet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	monthStr := r.URL.Query().Get("month")
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")

	if monthStr != "" && fromStr == "" {
		fromStr, toStr = monthToRange(monthStr)
	}
	if fromStr == "" {
		monthStr = time.Now().Format("2006-01")
		fromStr, toStr = monthToRange(monthStr)
	} else if monthStr == "" && len(fromStr) >= 7 {
		monthStr = fromStr[:7]
	}

	// ตรวจ cache
	cacheKey := fmt.Sprintf("fleet_%s_%s", fromStr, toStr)
	if cached, ok := getCache(cacheKey); ok {
		json.NewEncoder(w).Encode(cached)
		return
	}

	fleet := getFleetStatus(fromStr, toStr, monthStr)
	setCache(cacheKey, fleet, CacheMaxAge)
	json.NewEncoder(w).Encode(fleet)
}

func handleMachine(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Parse /api/machine/{id} หรือ /api/machine/{id}/cycletime
	path := strings.TrimPrefix(r.URL.Path, "/api/machine/")
	parts := strings.SplitN(path, "/", 2)
	machineID := strings.ToUpper(parts[0])

	// ตรวจว่า machine ID ถูกต้อง
	valid := false
	for _, id := range DefaultMachineIDs {
		if id == machineID {
			valid = true
			break
		}
	}
	if !valid {
		http.Error(w, `{"error":"invalid machine id"}`, http.StatusBadRequest)
		return
	}

	// เมนูตั้งค่า Cycle Time เฉพาะเครื่อง
	if r.Method == http.MethodPost && len(parts) > 1 && parts[1] == "cycletime" {
		var req struct {
			CycleTimeSec float64 `json:"cycleTimeSec"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CycleTimeSec <= 0 {
			http.Error(w, `{"error":"invalid cycle time"}`, http.StatusBadRequest)
			return
		}
		mCfg := appConfig.Machines[machineID]
		mCfg.ID = machineID
		mCfg.CycleTimeSec = roundF(req.CycleTimeSec, 2)
		appConfig.Machines[machineID] = mCfg
		if err := saveConfig(); err != nil {
			http.Error(w, `{"error":"failed to save config"}`, http.StatusInternalServerError)
			return
		}
		// ล้างแคชทั้งหมดเพื่อให้คำนวณใหม่ทันที
		cache = map[string]CacheEntry{}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":      true,
			"machineId":    machineID,
			"cycleTimeSec": mCfg.CycleTimeSec,
			"message":      "บันทึกเวลาต่อครั้งเรียบร้อยแล้ว",
		})
		return
	}

	q := r.URL.Query()
	fromStr := q.Get("from")
	toStr := q.Get("to")
	monthStr := q.Get("month")

	// ถ้าระบุ month ให้แปลงเป็น from/to
	if monthStr != "" && fromStr == "" {
		fromStr, toStr = monthToRange(monthStr)
	}

	// cache key รวม date range
	cacheKey := fmt.Sprintf("machine_%s_%s_%s", machineID, fromStr, toStr)
	if cached, ok := getCache(cacheKey); ok {
		json.NewEncoder(w).Encode(cached)
		return
	}

	data := getMachineData(machineID, fromStr, toStr)
	setCache(cacheKey, data, CacheMaxAge)
	json.NewEncoder(w).Encode(data)
}

// handleSSE - Server-Sent Events สำหรับ real-time update
func handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	machineID := r.URL.Query().Get("machine")
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	monthStr := r.URL.Query().Get("month")
	if monthStr != "" && fromStr == "" {
		fromStr, toStr = monthToRange(monthStr)
	}

	ticker := time.NewTicker(30 * time.Second) // ส่งข้อมูลใหม่ทุก 30 วิ
	defer ticker.Stop()

	ctx := r.Context()

	// ส่งข้อมูลทันทีที่ client เชื่อมต่อ
	sendSSEData(w, flusher, machineID, fromStr, toStr, monthStr)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// ล้าง cache เพื่อดึงข้อมูลใหม่
			if machineID != "" && machineID != "Home" {
				cacheKey := fmt.Sprintf("machine_%s_%s_%s", machineID, fromStr, toStr)
				deleteCache(cacheKey)
			} else {
				cacheKey := fmt.Sprintf("fleet_%s_%s", fromStr, toStr)
				deleteCache(cacheKey)
			}
			sendSSEData(w, flusher, machineID, fromStr, toStr, monthStr)
		}
	}
}

func sendSSEData(w http.ResponseWriter, flusher http.Flusher, machineID, fromStr, toStr, monthStr string) {
	var data interface{}
	if machineID != "" && machineID != "Home" {
		data = getMachineData(machineID, fromStr, toStr)
	} else {
		data = getFleetStatus(fromStr, toStr, monthStr)
	}

	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
}

// ============================================================
// Cache Helpers
// ============================================================

func getCache(key string) (interface{}, bool) {
	if entry, ok := cache[key]; ok {
		if time.Now().Before(entry.ExpiresAt) {
			return entry.Data, true
		}
		delete(cache, key)
	}
	return nil, false
}

func setCache(key string, data interface{}, dur time.Duration) {
	cache[key] = CacheEntry{Data: data, ExpiresAt: time.Now().Add(dur)}
}

func deleteCache(key string) {
	delete(cache, key)
}

// ============================================================
// Business Logic - Fleet Status
// ============================================================

func getFleetStatus(optFrom, optTo, optMonth string) FleetStatus {
	if optMonth != "" && optFrom == "" {
		optFrom, optTo = monthToRange(optMonth)
	}
	if optFrom == "" {
		optMonth = time.Now().Format("2006-01")
		optFrom, optTo = monthToRange(optMonth)
	} else if optMonth == "" && len(optFrom) >= 7 {
		optMonth = optFrom[:7]
	}

	fleet := FleetStatus{
		Total:    len(DefaultMachineIDs),
		Month:    optMonth,
		DateFrom: optFrom,
		DateTo:   optTo,
		Fleet:    make([]FleetMachine, len(DefaultMachineIDs)),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var totalEnergy, totalCost float64

	for idx, id := range DefaultMachineIDs {
		wg.Add(1)
		go func(i int, machineID string) {
			defer wg.Done()

			data := getMachineData(machineID, optFrom, optTo)
			status := data.Status
			if status == "" {
				status = "STOPPED"
			}

			label := data.Label
			if label == "" {
				label = machineID
			}

			fm := FleetMachine{
				ID:                    machineID,
				Label:                 label,
				Status:                status,
				PowerKwh:              data.PowerKwh,
				CostBaht:              data.CostBaht,
				DaysWorked:            data.DaysWorked,
				TargetDays:            data.TargetDays,
				TotalCount:            data.TotalCount,
				CounterLabel:          data.CounterLabel,
				Availability:          data.Availability,
				Downtime:              data.Downtime,
				IsCutter:              data.IsCutter,
				IsFolder:              data.IsFolder,
				RuntimeTodayFormatted: data.RuntimeTodayFormatted,
				TotalRuntimeFormatted: data.TotalRuntimeFormatted,
			}

			eVal, _ := strconv.ParseFloat(strings.ReplaceAll(data.PowerKwh, ",", ""), 64)
			cVal, _ := strconv.ParseFloat(strings.ReplaceAll(data.CostBaht, ",", ""), 64)

			mu.Lock()
			fleet.Fleet[i] = fm
			if status == "RUNNING" {
				fleet.Running++
			} else {
				fleet.Stopped++
			}
			totalEnergy += eVal
			totalCost += cVal
			mu.Unlock()
		}(idx, id)
	}

	wg.Wait()
	fleet.TotalEnergy = fmt.Sprintf("%.2f", totalEnergy)
	fleet.TotalCost = fmt.Sprintf("%.2f", totalCost)
	return fleet
}

func getLastMachineStatus(machineID string) string {
	targetSheetID := getMachineSpreadsheetID(machineID)
	tabName := getMachineTabName(machineID)

	rows, err := fetchSheetRows(targetSheetID, tabName)
	if err != nil {
		log.Printf("getLastMachineStatus[%s] error on sheet %s: %v", machineID, targetSheetID, err)
		return "STOPPED"
	}
	if len(rows) < 2 {
		return "STOPPED"
	}
	lastRow := rows[len(rows)-1]
	rawStatus := cellStr(lastRow, 2)
	current := cellFloat(lastRow, 4)

	if strings.ToUpper(rawStatus) == "RUN" || strings.ToUpper(rawStatus) == "RUNNING" || current > 0.5 {
		return "RUNNING"
	}
	return "STOPPED"
}

// ============================================================
// Business Logic - Machine Data
// ============================================================

func getMachineData(machineID, optFrom, optTo string) MachineData {
	startDate, endDate := parseDateRange(optFrom, optTo)

	// ดึงข้อมูลจาก Google Sheets (รองรับไฟล์แยกตามเครื่อง ทั้งแบบ Service Account และแบบแชร์ลิงก์)
	targetSheetID := getMachineSpreadsheetID(machineID)
	tabName := getMachineTabName(machineID)

	rows, err := fetchSheetRows(targetSheetID, tabName)
	if err != nil {
		log.Printf("getMachineData[%s] error on sheet %s: %v", machineID, targetSheetID, err)
		return getEmptyMachineData(machineID, startDate, endDate)
	}

	if len(rows) <= 1 {
		return getEmptyMachineData(machineID, startDate, endDate)
	}

	return processSheetData(machineID, rows[1:], startDate, endDate)
}

// processSheetData ประมวลผลข้อมูลจาก Google Sheets
func processSheetData(machineID string, rows [][]interface{}, startDate, endDate time.Time) MachineData {
	isCutter := cutterMachines[machineID]
	isFolder := folderMachines[machineID]
	isStrokeMachine := isCutter || isFolder
	cycleTimeSec := getCycleTime(machineID)
	phaseMultiplier := getPhaseMultiplier(machineID)
	electricityRate := getElectricityRate()
	daysInMonth := daysIn(startDate.Year(), startDate.Month())

	// accumulators
	dailyRunMinutes := map[int]float64{}
	dailyCuts := map[int]float64{}
	dailyRunSeconds := map[int]float64{}
	dailyEnergyStart := map[int]float64{}
	dailyEnergyEnd := map[int]float64{}
	dailyEnergySum := map[int]float64{}

	hourlyRunMinutes := make([]float64, 24)
	hourlyRunSeconds := make([]float64, 24)
	todayHourly := make([]float64, 24)

	// หาแถวของวันล่าสุดในข้อมูล (สำหรับแสดง Runtime วันนี้)
	var latestDay int
	now := time.Now()
	if now.Year() == startDate.Year() && now.Month() == startDate.Month() {
		latestDay = now.Day()
	} else {
		for _, r := range rows {
			if rd := parseDate(cellStr(r, 1)); rd != nil && !rd.Before(startDate) && !rd.After(endDate) {
				if rd.Day() > latestDay {
					latestDay = rd.Day()
				}
			}
		}
	}

	// เก็บข้อมูลกระแสไฟฟ้า Current ของวันนี้ 08:00 - 17:00 น.
	type currentPoint struct {
		timeStr string
		val     float64
	}
	var todayCurrentPoints []currentPoint
	var dayHourCurrentSum [32][24]float64
	var dayHourCurrentCount [32][24]int

	var totalRunMinutes, totalStopMinutes float64
	var totalCuts, totalRunSeconds float64

	for _, row := range rows {
		rawDateTime := cellStr(row, 1)
		rowDate := parseDate(rawDateTime)
		if rowDate == nil {
			continue
		}

		// กรองเฉพาะช่วงเวลาที่ต้องการ
		if rowDate.Before(startDate) || rowDate.After(endDate) {
			continue
		}

		rawStatus := cellStr(row, 2)
		current := cellFloat(row, 4)
		power := cellFloat(row, 5)
		energy := cellFloat(row, 6)

		dayKey := rowDate.Day()
		hourKey := rowDate.Hour()
		minuteKey := rowDate.Minute()

		// เฉพาะช่วง 08:00 - 17:00 น.
		isWorkingHours := (hourKey >= 8 && hourKey < 17) || (hourKey == 17 && minuteKey == 0)

		// เก็บกระแสไฟฟ้ารายชั่วโมงของทั้งเดือน 08:00 - 17:00 น.
		if isWorkingHours && current >= 0 {
			dayHourCurrentSum[dayKey][hourKey] += current
			dayHourCurrentCount[dayKey][hourKey]++
		}

		// เก็บกระแสไฟฟ้าของวันนี้เฉพาะช่วง 08:00 - 17:00 น.
		if dayKey == latestDay && isWorkingHours {
			todayCurrentPoints = append(todayCurrentPoints, currentPoint{
				timeStr: fmt.Sprintf("%02d:%02d", hourKey, minuteKey),
				val:     roundF(current, 2),
			})
		}

		if isStrokeMachine {
			cuts := parseFloat(rawStatus)
			runSec := cuts * cycleTimeSec
			if isWorkingHours && cuts > 0 {
				totalCuts += cuts
				dailyCuts[dayKey] += cuts
				dailyRunSeconds[dayKey] += runSec
				hourlyRunSeconds[hourKey] += runSec
				totalRunSeconds += runSec
				if dayKey == latestDay {
					todayHourly[hourKey] += roundF(runSec/60, 2)
				}
			}
		} else {
			cleanStatus := strings.ToUpper(strings.TrimSpace(rawStatus))
			isRunning := false
			if cleanStatus == "RUN" || cleanStatus == "RUNNING" {
				isRunning = true
			} else if cleanStatus == "STOP" || cleanStatus == "STOPPED" {
				isRunning = false
			} else if current > 5.0 {
				isRunning = true
			}

			if isRunning {
				hourlyRunMinutes[hourKey]++
			}

			if isWorkingHours {
				if isRunning {
					totalRunMinutes++
					dailyRunMinutes[dayKey]++
					if dayKey == latestDay {
						todayHourly[hourKey]++
					}
				} else {
					totalStopMinutes++
				}
			}
		}

		// Energy tracking
		if energy > 0 {
			if _, exists := dailyEnergyStart[dayKey]; !exists {
				dailyEnergyStart[dayKey] = energy
			}
			dailyEnergyEnd[dayKey] = energy
			dailyEnergySum[dayKey] += energy
		} else if power > 0 {
			dailyEnergySum[dayKey] += power / 60.0
		}
	}

	// เตรียม daily arrays
	monthPrefix := fmt.Sprintf("%02d", startDate.Month())
	var dateLabels []string
	var prodDaily []float64
	var prodDailyFormatted []string
	var energyDaily []float64
	var dailyCount []float64
	var totalEnergyKwh float64
	var daysWorkedCount int

	for day := 1; day <= daysInMonth; day++ {
		dayStr := fmt.Sprintf("%02d", day)
		dateLabels = append(dateLabels, monthPrefix+"-"+dayStr)

		var prodHours float64
		var dayFmt string

		if isStrokeMachine {
			daySec := dailyRunSeconds[day]
			cutsInDay := dailyCuts[day]
			dailyCount = append(dailyCount, cutsInDay)
			prodHours = roundF(daySec/3600, 4)
			if cutsInDay > 0 {
				daysWorkedCount++
			}
			hh := int(daySec) / 3600
			mm := (int(daySec) % 3600) / 60
			ss := int(math.Round(math.Mod(daySec, 60)))
			dayFmt = fmt.Sprintf("%02d:%02d:%02d", hh, mm, ss)
		} else {
			runMin := dailyRunMinutes[day]
			dailyCount = append(dailyCount, 0)
			prodHours = roundF(runMin/60, 2)
			if prodHours > 0.1 {
				daysWorkedCount++
			}
			hh := int(runMin) / 60
			mm := int(math.Round(math.Mod(runMin, 60)))
			dayFmt = fmt.Sprintf("%02d:%02d:00", hh, mm)
		}

		prodDaily = append(prodDaily, prodHours)
		prodDailyFormatted = append(prodDailyFormatted, dayFmt)

		// Energy รายวัน (คูณด้วย phaseMultiplier เช่น 3.0 สำหรับเครื่อง 3 เฟส)
		dayEnergy := 0.0
		if eEnd, ok := dailyEnergyEnd[day]; ok {
			if eStart, ok2 := dailyEnergyStart[day]; ok2 && eEnd >= eStart {
				dayEnergy = eEnd - eStart
			}
		}
		if dayEnergy == 0 {
			dayEnergy = dailyEnergySum[day]
		}
		dayEnergy = roundF(dayEnergy*phaseMultiplier, 2)
		energyDaily = append(energyDaily, dayEnergy)
		totalEnergyKwh += dayEnergy
	}

	// Runtime hourly (เฉลี่ยต่อวันทำงาน)
	runtimeHourly := make([]float64, 24)
	divisor := float64(max(1, daysWorkedCount))
	for h := 0; h < 24; h++ {
		if isStrokeMachine {
			runtimeHourly[h] = roundF((hourlyRunSeconds[h]/60)/divisor, 2)
		} else {
			runtimeHourly[h] = math.Round(hourlyRunMinutes[h] / divisor)
		}
	}

	// Current hourly (เฉลี่ยกระแสไฟฟ้าทั้งเดือนในแต่ละชั่วโมง 08:00 - 17:00 น.)
	currentHourly := make([]float64, 24)
	for h := 8; h <= 17; h++ {
		var sumForHour float64
		var countForHour int
		for d := 1; d <= daysInMonth; d++ {
			isWorkDay := false
			if isStrokeMachine {
				isWorkDay = dailyCuts[d] > 0
			} else {
				isWorkDay = (dailyRunMinutes[d] / 60.0) > 0.1
			}

			if (daysWorkedCount == 0 || isWorkDay) && dayHourCurrentCount[d][h] > 0 {
				avgDayHour := dayHourCurrentSum[d][h] / float64(dayHourCurrentCount[d][h])
				sumForHour += avgDayHour
				countForHour++
			}
		}
		if countForHour > 0 {
			currentHourly[h] = roundF(sumForHour/float64(countForHour), 2)
		} else {
			currentHourly[h] = 0.0
		}
	}

	// Availability & Downtime
	availability, downtime := calcAvailability(isStrokeMachine, totalCuts, totalRunSeconds, totalRunMinutes, totalStopMinutes, daysWorkedCount)

	// Target work days (จันทร์-เสาร์)
	targetDays := calcTargetDays(startDate.Year(), startDate.Month())

	totalEnergyKwh = roundF(totalEnergyKwh, 2)
	costBaht := roundF(totalEnergyKwh*electricityRate, 2)

	// Format machine label (LS001 -> LS-001)
	re := regexp.MustCompile(`^([A-Z]+)(\d+)$`)
	label := re.ReplaceAllString(machineID, "$1-$2")

	var totalCutsStr *string
	var counterLabel string
	if isStrokeMachine {
		s := fmt.Sprintf("%.0f", totalCuts)
		totalCutsStr = &s
		if isFolder {
			counterLabel = "จำนวนพับสะสม"
		} else {
			counterLabel = "จำนวนตัดสะสม"
		}
	}

	// จัดเตรียมข้อมูลกราฟ Current ของวันนี้ 08:00 - 17:00 น.
	var currentSamples []float64
	var currentTimes []string

	if len(todayCurrentPoints) > 0 {
		step := max(1, len(todayCurrentPoints)/60)
		for idx := 0; idx < len(todayCurrentPoints); idx += step {
			currentSamples = append(currentSamples, todayCurrentPoints[idx].val)
			currentTimes = append(currentTimes, todayCurrentPoints[idx].timeStr)
		}
		// เพิ่มจุดสุดท้ายถ้ายังไม่รวม
		lastIdx := len(todayCurrentPoints) - 1
		if len(currentTimes) > 0 && currentTimes[len(currentTimes)-1] != todayCurrentPoints[lastIdx].timeStr {
			currentSamples = append(currentSamples, todayCurrentPoints[lastIdx].val)
			currentTimes = append(currentTimes, todayCurrentPoints[lastIdx].timeStr)
		}
	} else {
		// หากยังไม่มีข้อมูลของวันนี้ ให้สร้างจุดเริ่มต้นตามชั่วโมง 08:00 - 17:00 น.
		for h := 8; h <= 17; h++ {
			currentSamples = append(currentSamples, 0)
			currentTimes = append(currentTimes, fmt.Sprintf("%02d:00", h))
		}
	}

	selMonth := fmt.Sprintf("%04d-%02d", startDate.Year(), startDate.Month())
	curDateStr := fmt.Sprintf("%04d-%02d-%02d", startDate.Year(), startDate.Month(), latestDay)

	// แปลงเวลาทำงานวันนี้และรวมทั้งเดือนเป็นรูปแบบ HH:MM:SS
	var todayRunFormatted string
	if isStrokeMachine {
		s := int(math.Round(dailyRunSeconds[latestDay]))
		todayRunFormatted = fmt.Sprintf("%02d:%02d:%02d", s/3600, (s%3600)/60, s%60)
	} else {
		m := int(math.Round(dailyRunMinutes[latestDay]))
		todayRunFormatted = fmt.Sprintf("%02d:%02d:00", m/60, m%60)
	}

	var totalRunFormatted string
	if isStrokeMachine {
		s := int(math.Round(totalRunSeconds))
		totalRunFormatted = fmt.Sprintf("%02d:%02d:%02d", s/3600, (s%3600)/60, s%60)
	} else {
		m := int(math.Round(totalRunMinutes))
		totalRunFormatted = fmt.Sprintf("%02d:%02d:00", m/60, m%60)
	}

	// สถานะเครื่องจักร: วัดจากเวลาทำงาน (Runtime)
	status := "STOPPED"
	isCurrentMonth := (now.Year() == startDate.Year() && now.Month() == startDate.Month())

	if isCurrentMonth {
		// เดือนปัจจุบัน: วัดสถานะจากเวลาทำงานของวันนี้ (Today's Runtime)
		if isStrokeMachine {
			// เครื่องตัด/พับ: วันนี้มีการตัด/พับจริง และมีเวลาทำงาน
			if dailyRunSeconds[latestDay] > 0 || dailyCuts[latestDay] > 0 {
				status = "RUNNING"
			}
		} else {
			// เครื่องเลเซอร์/ปั๊ม: วันนี้มีเวลาเดินเครื่องจริง
			if dailyRunMinutes[latestDay] > 0 {
				status = "RUNNING"
			}
		}
	} else {
		// เดือนในอดีต: วัดสถานะจากเวลาทำงานของเดือนนั้น
		if daysWorkedCount > 0 || (isStrokeMachine && totalRunSeconds > 0) || (!isStrokeMachine && totalRunMinutes > 0) {
			status = "RUNNING"
		}
	}

	return MachineData{
		ID:                    machineID,
		Label:                 label,
		Status:                status,
		PowerKwh:              fmt.Sprintf("%.2f", totalEnergyKwh),
		CostBaht:              fmt.Sprintf("%.2f", costBaht),
		DaysWorked:            strconv.Itoa(daysWorkedCount),
		TargetDays:            strconv.Itoa(targetDays),
		TotalCuts:             totalCutsStr,
		TotalCount:            totalCutsStr,
		CounterLabel:          counterLabel,
		CycleTimeSec:          cycleTimeSec,
		IsCutter:              isCutter,
		IsFolder:              isFolder,
		Availability:          fmt.Sprintf("%.2f", availability),
		Downtime:              fmt.Sprintf("%.2f", downtime),
		ShiftHours:            "08:00 - 17:00 น.",
		SelectedMonth:         selMonth,
		DateFrom:              formatDate(startDate),
		DateTo:                formatDate(endDate),
		DateLabels:            dateLabels,
		RuntimeHourly:         runtimeHourly,
		RuntimeToday:          todayHourly,
		RuntimeTodayFormatted: todayRunFormatted,
		TotalRuntimeFormatted: totalRunFormatted,
		ProdDaily:             prodDaily,
		ProdDailyFormatted:    prodDailyFormatted,
		CurrentLine:           currentSamples,
		CurrentTimes:          currentTimes,
		CurrentHourly:         currentHourly,
		CurrentDate:           curDateStr,
		EnergyDaily:           energyDaily,
		DailyCount:            dailyCount,
		ElectricityRate:       electricityRate,
		PhaseMultiplier:       phaseMultiplier,
	}
}

func getEmptyMachineData(machineID string, startDate, endDate time.Time) MachineData {
	isCutter := cutterMachines[machineID]
	isFolder := folderMachines[machineID]
	isStrokeMachine := isCutter || isFolder
	daysInMonth := daysIn(startDate.Year(), startDate.Month())
	monthPrefix := fmt.Sprintf("%02d", startDate.Month())
	targetDays := calcTargetDays(startDate.Year(), startDate.Month())

	var dateLabels []string
	var prodDaily []float64
	var prodDailyFormatted []string
	var energyDaily []float64

	for day := 1; day <= daysInMonth; day++ {
		dateLabels = append(dateLabels, fmt.Sprintf("%s-%02d", monthPrefix, day))
		prodDaily = append(prodDaily, 0)
		prodDailyFormatted = append(prodDailyFormatted, "00:00:00")
		energyDaily = append(energyDaily, 0)
	}

	re := regexp.MustCompile(`^([A-Z]+)(\d+)$`)
	label := re.ReplaceAllString(machineID, "$1-$2")

	var totalCutsStr *string
	var counterLabel string
	if isStrokeMachine {
		s := "0"
		totalCutsStr = &s
		if isFolder {
			counterLabel = "จำนวนพับสะสม"
		} else {
			counterLabel = "จำนวนตัดสะสม"
		}
	}

	selMonth := fmt.Sprintf("%04d-%02d", startDate.Year(), startDate.Month())

	var emptyCurrent []float64
	var emptyTimes []string
	for h := 8; h <= 17; h++ {
		emptyCurrent = append(emptyCurrent, 0)
		emptyTimes = append(emptyTimes, fmt.Sprintf("%02d:00", h))
	}

	return MachineData{
		ID:                 machineID,
		Label:              label,
		Status:             "STOPPED",
		PowerKwh:           "0.00",
		CostBaht:           "0.00",
		DaysWorked:         "0",
		TargetDays:         strconv.Itoa(targetDays),
		TotalCuts:          totalCutsStr,
		TotalCount:         totalCutsStr,
		CounterLabel:       counterLabel,
		CycleTimeSec:       getCycleTime(machineID),
		IsCutter:           isCutter,
		IsFolder:           isFolder,
		Availability:       "0.00",
		Downtime:           "100.00",
		ShiftHours:         "08:00 - 17:00 น.",
		SelectedMonth:      selMonth,
		DateFrom:           formatDate(startDate),
		DateTo:             formatDate(endDate),
		DateLabels:         dateLabels,
		RuntimeHourly:         make([]float64, 24),
		RuntimeToday:          make([]float64, 24),
		RuntimeTodayFormatted: "00:00:00",
		TotalRuntimeFormatted: "00:00:00",
		ProdDaily:             prodDaily,
		ProdDailyFormatted: prodDailyFormatted,
		CurrentLine:        emptyCurrent,
		CurrentTimes:       emptyTimes,
		CurrentHourly:      make([]float64, 24),
		CurrentDate:        formatDate(startDate),
		EnergyDaily:        energyDaily,
		DailyCount:         make([]float64, daysInMonth),
		ElectricityRate:    getElectricityRate(),
		PhaseMultiplier:    getPhaseMultiplier(machineID),
	}
}

// ============================================================
// Helper Functions
// ============================================================

func parseDateRange(fromStr, toStr string) (time.Time, time.Time) {
	now := time.Now()
	loc := now.Location()

	if fromStr != "" && toStr != "" {
		from, err1 := time.ParseInLocation("2006-01-02", fromStr, loc)
		to, err2 := time.ParseInLocation("2006-01-02", toStr, loc)
		if err1 == nil && err2 == nil {
			to = time.Date(to.Year(), to.Month(), to.Day(), 23, 59, 59, 0, loc)
			return from, to
		}
	}

	// default: เดือนปัจจุบัน
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	end := time.Date(now.Year(), now.Month()+1, 0, 23, 59, 59, 0, loc)
	return start, end
}

func monthToRange(monthStr string) (string, string) {
	// monthStr = "2026-10"
	t, err := time.Parse("2006-01", monthStr)
	if err != nil {
		return "", ""
	}
	firstDay := fmt.Sprintf("%d-%02d-01", t.Year(), t.Month())
	lastDay := fmt.Sprintf("%d-%02d-%02d", t.Year(), t.Month(), daysIn(t.Year(), t.Month()))
	return firstDay, lastDay
}

var datePatterns = []string{
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006/01/02 15:04:05",
	"2/1/2006 15:04:05",
	"2/1/2006 15:04",
	"01/02/2006 15:04:05",
	"2006-01-02T15:04:05Z07:00",
}

func parseDate(val string) *time.Time {
	val = strings.TrimSpace(val)
	if val == "" {
		return nil
	}
	// Remove extra commas
	val = strings.ReplaceAll(val, ", ", " ")
	val = strings.ReplaceAll(val, ",", " ")

	loc := time.Local
	for _, pattern := range datePatterns {
		if t, err := time.ParseInLocation(pattern, val, loc); err == nil {
			return &t
		}
	}
	return nil
}

func formatDate(t time.Time) string {
	return fmt.Sprintf("%04d-%02d-%02d", t.Year(), t.Month(), t.Day())
}

func daysIn(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.Local).Day()
}

func calcTargetDays(year int, month time.Month) int {
	days := daysIn(year, month)
	target := 0
	for d := 1; d <= days; d++ {
		if time.Date(year, month, d, 0, 0, 0, 0, time.Local).Weekday() != time.Sunday {
			target++
		}
	}
	if target == 0 {
		return 23
	}
	return target
}

func calcAvailability(isStrokeMachine bool, totalCuts, totalRunSeconds, totalRunMinutes, totalStopMinutes float64, daysWorked int) (float64, float64) {
	if daysWorked == 0 {
		return 0.00, 100.00
	}

	plannedHours := float64(daysWorked) * 8.0
	var totalRunHours float64

	if isStrokeMachine {
		if totalCuts == 0 {
			return 0.00, 100.00
		}
		totalRunHours = totalRunSeconds / 3600.0
	} else {
		if totalRunMinutes == 0 {
			return 0.00, 100.00
		}
		totalRunHours = totalRunMinutes / 60.0
	}

	avail := roundF((totalRunHours/plannedHours)*100.0, 2)
	if avail > 100.0 {
		avail = 100.0
	}
	downtime := roundF(100.0-avail, 2)
	return avail, downtime
}

func cellStr(row []interface{}, idx int) string {
	if idx >= len(row) {
		return ""
	}
	return fmt.Sprintf("%v", row[idx])
}

func cellFloat(row []interface{}, idx int) float64 {
	return parseFloat(cellStr(row, idx))
}

func parseFloat(s string) float64 {
	s = strings.TrimSpace(s)
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func roundF(v float64, decimals int) float64 {
	pow := math.Pow(10, float64(decimals))
	return math.Round(v*pow) / pow
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

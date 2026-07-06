<#
.SYNOPSIS
  ดึง Firebase App Check "debug token" จากแอปคนขับที่รันบนเครื่องจริง (debug build)
  แล้วเอาไปใส่ allowlist ที่ Firebase Console → App Check → Manage debug tokens

.DESCRIPTION
  แก้ปัญหา login ล้มด้วย "Firebase App Check token is invalid"
  โค้ดใน lib/main.dart ใช้ AndroidProvider.debug เมื่อเป็น debug build (flutter run)
  provider จะพิมพ์ debug secret ลง logcat ทุกครั้งที่เปิดแอป — สคริปต์นี้ relaunch แอป
  แล้วดักบรรทัดนั้นให้อัตโนมัติ

.PARAMETER Package
  applicationId ของ flavor ที่รันอยู่
    prod (จอขึ้น "LogiTrack")     → com.wrt.logitrack   (ค่า default)
    dev  (จอขึ้น "LogiTrack DEV") → com.example.logi_track_driver_app

.EXAMPLE
  ./appcheck-debug-token.ps1
  ./appcheck-debug-token.ps1 -Package com.example.logi_track_driver_app
#>
param(
  [string]$Package = "com.wrt.logitrack"
)

$ErrorActionPreference = "Stop"

function Resolve-Adb {
  $onPath = Get-Command adb -ErrorAction SilentlyContinue
  if ($onPath) { return $onPath.Source }
  $candidates = @(
    "$env:LOCALAPPDATA\Android\Sdk\platform-tools\adb.exe",
    "$env:ANDROID_HOME\platform-tools\adb.exe",
    "$env:ANDROID_SDK_ROOT\platform-tools\adb.exe"
  )
  foreach ($c in $candidates) { if ($c -and (Test-Path $c)) { return $c } }
  throw "หา adb ไม่เจอ — เปิด Android SDK platform-tools หรือใส่ adb ใน PATH"
}

$adb = Resolve-Adb
Write-Host "adb: $adb" -ForegroundColor DarkGray

# ต้องมีเครื่องต่ออยู่ 1 เครื่อง
$devices = & $adb devices | Select-String -Pattern "\tdevice$"
if (-not $devices) {
  Write-Host "ไม่พบเครื่องที่ authorize แล้ว" -ForegroundColor Red
  Write-Host "  1) เสียบ USB + เปิด USB debugging"
  Write-Host "  2) บนมือถือกด Allow เมื่อขึ้น 'Allow USB debugging?'"
  Write-Host "  3) เช็คด้วย: `"$adb`" devices"
  exit 1
}

Write-Host "Package: $Package" -ForegroundColor Cyan
Write-Host "กำลัง relaunch แอปเพื่อให้ App Check พิมพ์ token ใหม่..." -ForegroundColor Yellow

& $adb logcat -c                                   # เคลียร์ buffer
& $adb shell am force-stop $Package                # ปิดแอป
Start-Sleep -Milliseconds 500
& $adb shell monkey -p $Package -c android.intent.category.LAUNCHER 1 | Out-Null  # เปิดแอป

Write-Host "รอ log (สูงสุด ~25 วิ)..." -ForegroundColor Yellow

$token = $null
$deadline = (Get-Date).AddSeconds(25)
while ((Get-Date) -lt $deadline -and -not $token) {
  Start-Sleep -Milliseconds 800
  $dump = & $adb logcat -d 2>$null
  $line = $dump | Select-String -Pattern "debug secret" | Select-Object -Last 1
  if ($line) {
    $m = [regex]::Match($line.ToString(), "[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}")
    if ($m.Success) { $token = $m.Value }
  }
}

Write-Host ""
if ($token) {
  Write-Host "===================== DEBUG TOKEN =====================" -ForegroundColor Green
  Write-Host $token -ForegroundColor Green
  Write-Host "=======================================================" -ForegroundColor Green
  Write-Host ""
  Write-Host "เอาไปใส่ที่: Firebase Console (โปรเจกต์ prod) -> App Check ->"
  Write-Host "  เลือกแอป Android ($Package) -> เมนู ... -> Manage debug tokens -> Add"
  # คัดลอกเข้าคลิปบอร์ดให้เลย
  try { $token | Set-Clipboard; Write-Host "(คัดลอกเข้าคลิปบอร์ดแล้ว)" -ForegroundColor DarkGray } catch {}
} else {
  Write-Host "ยังไม่เจอ token ใน log" -ForegroundColor Red
  Write-Host "ลองเอง: `"$adb`" logcat | Select-String 'debug secret'"
  Write-Host "แล้วเปิดแอปใหม่ / กดปุ่ม login หนึ่งครั้ง"
  Write-Host "ถ้าเป็น release build (ไม่ใช่ flutter run) จะไม่มี debug token — ต้องใช้ Play Integrity แทน"
}

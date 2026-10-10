// Image preview and ZIP download text shared by the accounting pages and the driver monitor
// (`accounting.preview.*`, `accounting.error.*`). It sits in the base dictionary so a route outside the
// accounting group never loads the `accounting` namespace for it (developer-spec.md §10.11, TW4).
const imagePreview: Record<string, string> = {
    "accounting.preview.zoomIn": "ขยาย",
    "accounting.preview.zoomOut": "ย่อ",
    "accounting.preview.resetZoom": "รีเซ็ตการซูม",
    "accounting.preview.previous": "ก่อนหน้า",
    "accounting.preview.next": "ถัดไป",
    "accounting.preview.notPreviewable": "ไม่สามารถแสดงตัวอย่างไฟล์นี้ในหน้านี้ได้ กรุณาเปิดในแท็บใหม่",
    "accounting.preview.print": "พิมพ์",
    "accounting.preview.downloadZip": "ดาวน์โหลด ZIP",
    "accounting.preview.downloadZipLoading": "กำลังสร้าง ZIP…",

    "accounting.error.zipFailed": "สร้าง ZIP ไม่ได้ ไม่สามารถดาวน์โหลดรูปได้",
    "accounting.error.partialDownload": "บันทึก ZIP แล้ว {added} รูป ไม่สามารถดึงได้ {failed} รูป (เครือข่ายหรือ CORS ของ Storage)",
    "accounting.error.corsHint": "ถ้าดาวน์โหลดไม่ได้แต่เห็นรูปในแอป ให้ตั้งค่า CORS ของ Storage bucket ให้ตรงกับโดเมนเว็บ ดู docs/FIREBASE_STORAGE_CORS.md",
};

export default imagePreview;

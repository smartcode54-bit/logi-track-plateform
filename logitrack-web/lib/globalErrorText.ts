/**
 * The words of app/global-error.tsx (UI3D), the page shown when the root layout itself crashed. Nothing above it is
 * left running (no language provider, no dictionary load), so its text lives here, in both languages, and the page
 * shows both at once. Small on purpose: the base dictionaries never enter a route's initial JS (bundle gate).
 */
export interface GlobalErrorText {
    title: string;
    body: string;
    retry: string;
    home: string;
    reference: string;
}

export const GLOBAL_ERROR_TEXT: Readonly<Record<"en" | "th", GlobalErrorText>> = {
    en: {
        title: "LogiTrack Pro stopped working",
        body: "Something went wrong while loading the console. Try again; if it keeps happening, tell support what you were doing and quote the reference below.",
        retry: "Try again",
        home: "Go to the start page",
        reference: "Reference for support:",
    },
    th: {
        title: "LogiTrack Pro หยุดทำงาน",
        body: "เกิดข้อผิดพลาดระหว่างโหลดคอนโซล ลองอีกครั้ง หากยังเกิดซ้ำ ให้แจ้งฝ่ายช่วยเหลือว่าคุณกำลังทำอะไรอยู่ พร้อมรหัสอ้างอิงด้านล่าง",
        retry: "ลองอีกครั้ง",
        home: "ไปหน้าแรก",
        reference: "รหัสอ้างอิงสำหรับฝ่ายช่วยเหลือ:",
    },
};


/** Safe text for projection and support refusals, also usable without a locale provider. */
export const SUPPORT_ERROR_TEXT = {
    en: {
        "apiError.header_not_allowed": "This request is not allowed here.",
        "apiError.support_read_only": "Support view is read-only. Leave support view before making changes.",
        "apiError.support_view_unavailable": "Not yet available in support view.",
        "apiError.support_access_required": "The organisation must enable support access before this action is available.",
        "apiError.support_projection_unavailable": "Protected data could not be loaded safely. Please try again.",
        "apiError.masked_query_not_allowed": "Search or sorting by masked data is unavailable. Use visible fields instead.",
        "apiError.support_access_masked": "Search or sorting by masked data is unavailable. Use visible fields instead.",
    },
    th: {
        "apiError.header_not_allowed": "ไม่อนุญาตคำขอนี้ในช่องทางนี้",
        "apiError.support_read_only": "มุมมองช่วยเหลืออ่านได้อย่างเดียว ออกจากมุมมองช่วยเหลือก่อนแก้ไขข้อมูล",
        "apiError.support_view_unavailable": "ยังไม่พร้อมใช้งานในมุมมองช่วยเหลือ",
        "apiError.support_access_required": "องค์กรต้องเปิดสิทธิ์การเข้าถึงเพื่อช่วยเหลือก่อนจึงจะดำเนินการนี้ได้",
        "apiError.support_projection_unavailable": "ไม่สามารถโหลดข้อมูลที่ปกป้องไว้อย่างปลอดภัยได้ กรุณาลองอีกครั้ง",
        "apiError.masked_query_not_allowed": "ไม่สามารถค้นหาหรือเรียงตามข้อมูลที่ปกปิดได้ กรุณาใช้ข้อมูลที่แสดงอยู่",
        "apiError.support_access_masked": "ไม่สามารถค้นหาหรือเรียงตามข้อมูลที่ปกปิดได้ กรุณาใช้ข้อมูลที่แสดงอยู่",
    },
} as const;

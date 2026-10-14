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

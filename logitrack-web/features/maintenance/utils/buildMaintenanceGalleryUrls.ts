/** ลำดับรูปสำหรับพรีวิว: รูปแอดมินก่อน แล้วค่อยใบเสร็จคนขับ (ไม่ซ้ำ) */
export function buildMaintenanceGalleryUrls(record: {
    images?: string[] | undefined;
    invoiceUrl?: string | null | undefined;
}): string[] {
    const seen = new Set<string>();
    const out: string[] = [];
    for (const u of record.images ?? []) {
        if (typeof u === "string" && u.trim().length > 0 && !seen.has(u)) {
            seen.add(u);
            out.push(u);
        }
    }
    const inv = record.invoiceUrl?.trim();
    if (inv && !seen.has(inv)) {
        seen.add(inv);
        out.push(inv);
    }
    return out;
}

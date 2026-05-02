/** ลำดับรูปสำหรับพรีวิว: รูปแอดมินก่อน แล้วค่อยใบเสร็จคนขับ (invoiceUrl + receipts จากแอป) (ไม่ซ้ำ) */
export function buildMaintenanceGalleryUrls(record: {
    images?: string[] | undefined;
    invoiceUrl?: string | null | undefined;
    receipts?: string[] | undefined;
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
    for (const u of record.receipts ?? []) {
        const t = typeof u === "string" ? u.trim() : "";
        if (t.length > 0 && !seen.has(t)) {
            seen.add(t);
            out.push(t);
        }
    }
    return out;
}

/** เฉพาะใบเสร็จจากแอปคนขับ: invoiceUrl + receipts[] (ไม่ซ้ำ) */
export function buildDriverReceiptUrls(record: {
    invoiceUrl?: string | null | undefined;
    receipts?: string[] | undefined;
}): string[] {
    const seen = new Set<string>();
    const out: string[] = [];
    const inv = record.invoiceUrl?.trim();
    if (inv && inv.length > 0 && !seen.has(inv)) {
        seen.add(inv);
        out.push(inv);
    }
    for (const u of record.receipts ?? []) {
        const t = typeof u === "string" ? u.trim() : "";
        if (t.length > 0 && !seen.has(t)) {
            seen.add(t);
            out.push(t);
        }
    }
    return out;
}

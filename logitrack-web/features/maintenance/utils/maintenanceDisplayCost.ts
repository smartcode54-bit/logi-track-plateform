/**
 * ยอดที่ใช้แสดงผล / รวมในสถิติ — ลำดับความสำคัญ:
 * 1) totalCost ถ้ามีและ > 0
 * 2) costLabor + costParts ถ้ารวมแล้ว > 0
 * 3) invoiceAmount จากใบเสร็จที่คนขับส่ง (แอปมือถือ)
 */
export function maintenanceDisplayCost(record: {
    totalCost?: number | null;
    costLabor?: number | null;
    costParts?: number | null;
    invoiceAmount?: number | null;
}): number {
    const tc = record.totalCost;
    if (typeof tc === "number" && !Number.isNaN(tc) && tc > 0) return tc;

    const labor = typeof record.costLabor === "number" && !Number.isNaN(record.costLabor) ? record.costLabor : 0;
    const parts = typeof record.costParts === "number" && !Number.isNaN(record.costParts) ? record.costParts : 0;
    const sumParts = labor + parts;
    if (sumParts > 0) return sumParts;

    const inv = record.invoiceAmount;
    if (typeof inv === "number" && !Number.isNaN(inv) && inv > 0) return inv;

    return 0;
}

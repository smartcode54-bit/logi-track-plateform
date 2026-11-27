import type { ImportColumnSpec } from "@/lib/importColumns";

/**
 * Column lookup for the Job Assign import. Order matters: a field listed earlier takes its column
 * first, so the narrow headers (Actual pickup, Driver Phone) are claimed before the loose terms
 * ("pickup", "driver", "name", "id") could reach them.
 */
export const JOB_ASSIGN_IMPORT_COLUMNS = [
    ["taskType", ["fm/lh", "fmlh", "ชนิดงาน"]],
    ["actual", ["actual", "รับงานจริง", "รับจริง"]],
    ["date", ["วันแผน", "plan", "date", "วัน"]],
    ["note", ["note", "หมายเหตุ"]],
    ["sourceHub", ["pickup location", "จุดรับงาน", "source", "ต้นทาง", "hub", "pickup"]],
    ["destination", ["destination", "ปลายทาง", "soc"]],
    ["customer", ["customer", "ลูกค้า", "บริษัท"]],
    ["jobCategory", ["job category", "jobcategory", "หลัก/เสริม"]],
    ["truckType", ["truck type", "ประเภทรถ", "platetype"]],
    ["licensePlate", ["license", "ทะเบียน"]],
    ["shipmentId", ["shipment", "เลขงาน", "id"]],
    ["driverPhone", ["phone", "tel", "เบอร์"]],
    ["driverName", ["driver", "คนขับ", "name"]],
] as const satisfies ImportColumnSpec<string>;

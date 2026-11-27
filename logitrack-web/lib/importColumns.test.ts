import { describe, expect, it } from "vitest";
import { cellText, resolveImportColumns, type ImportColumnSpec } from "./importColumns";
import { JOB_ASSIGN_IMPORT_COLUMNS } from "@/app/app/job-assign/importColumns";

// Header row of the Job Assign template (the file that put date serials into จุดรับงาน).
const JOB_ASSIGN_HEADERS = [
    "FM/LH (ชนิดงาน)",
    "Date (วันแผนงาน)",
    "Actual pickup (วันเวลารับงานจริง)",
    "Pickup Location (จุดรับงาน)",
    "Destination (ปลายทาง)",
    "Customer (ลูกค้า: รหัส/ชื่อ)",
    "Job Category (หลัก/เสริม)",
    "Truck Type (ประเภทรถ)",
    "Shipment ID (เลขงาน)",
    "License Plate (ทะเบียน)",
    "Driver Name (คนขับ)",
    "Driver Phone (เบอร์)",
    "Note (หมายเหตุ)",
];

describe("resolveImportColumns", () => {
    it("maps the Job Assign template without two fields sharing a column", () => {
        const cols = resolveImportColumns(JOB_ASSIGN_HEADERS, JOB_ASSIGN_IMPORT_COLUMNS);
        expect(cols).toEqual({
            taskType: 0,
            date: 1,
            actual: 2,
            sourceHub: 3,
            destination: 4,
            customer: 5,
            jobCategory: 6,
            truckType: 7,
            shipmentId: 8,
            licensePlate: 9,
            driverName: 10,
            driverPhone: 11,
            note: 12,
        });
    });

    it("finds the pickup place even when the actual-pickup column comes first", () => {
        const cols = resolveImportColumns(["Actual pickup", "Pickup"], JOB_ASSIGN_IMPORT_COLUMNS);
        expect(cols.actual).toBe(0);
        expect(cols.sourceHub).toBe(1);
    });

    it("leaves a field out when no column is left for it", () => {
        const cols = resolveImportColumns(["Actual pickup"], JOB_ASSIGN_IMPORT_COLUMNS);
        expect(cols.actual).toBe(0);
        expect(cols.sourceHub).toBeUndefined();
    });

    it("tries terms in order before falling back to a looser one", () => {
        const spec: ImportColumnSpec<"a"> = [["a", ["exact", "ex"]]];
        expect(resolveImportColumns(["ex", "exact"], spec).a).toBe(1);
    });

    it("tolerates blank and non-string header cells", () => {
        const spec: ImportColumnSpec<"a"> = [["a", ["x"]]];
        expect(resolveImportColumns([undefined, 5, null, "X"], spec).a).toBe(3);
    });
});

describe("cellText", () => {
    it("turns numbers into text and blanks into undefined", () => {
        expect(cellText(46354.375)).toBe("46354.375");
        expect(cellText("  สาทร05 ")).toBe("สาทร05");
        expect(cellText("   ")).toBeUndefined();
        expect(cellText(null)).toBeUndefined();
    });
});

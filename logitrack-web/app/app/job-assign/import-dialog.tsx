"use client";

import { useState, useRef, useEffect } from "react";
import * as XLSX from "xlsx";
import * as XLSXStyle from "xlsx-js-style";
import { Upload, X, FileSpreadsheet, Check, AlertCircle, Download } from "lucide-react";
import { format } from "date-fns";
import { collection, writeBatch, doc } from "firebase/firestore";
import { db } from "@/firebase/client";
import { useLanguage } from "@/context/language";
import { COLLECTIONS } from "@/lib/collections";
import { taskService, TaskTruck } from "@/features/tasks/services/taskService";
import { taskTruckTypeFromTruckDoc } from "@/lib/truckType";
import { jobCategoryFromCell } from "@/lib/jobCategory";
import { getCustomers, CustomerData } from "@/features/customers/api/customers";

const normalizePlate = (plate: unknown) => String(plate ?? "").toUpperCase().replace(/[\s-]/g, "");

import { Button } from "@/components/ui/button";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
    DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Progress } from "@/components/ui/progress";

interface ImportDialogProps {
    onSuccess: () => void;
}

type JobType = "FIRST_MILE" | "LINE_HAUL";

interface ImportRow {
    id: number;
    taskType: JobType;
    date: Date;                 // plan date (ADR 0027)
    actualPickupAt?: Date;      // วันเวลารับงานจริง (ADR 0028)
    sourceHub?: string;
    destination?: string;
    truckType?: string;
    truckId?: string;
    jobCategory?: "PRIMARY" | "SUPPLEMENTARY";
    taskId?: string;
    licensePlate?: string;
    driverName?: string;
    driverPhone?: string;
    billingCustomerId?: string;
    billingCustomerName?: string;
    billingCustomerCode?: string;
    plateUnknown?: boolean;
    isValid: boolean;
    invalidReason?: string;
}

const isExampleNote = (note: unknown) => {
    const s = String(note ?? "").toLowerCase();
    return s.includes("ตัวอย่าง") || s.includes("example") || s.includes("ลบ") || s.includes("delete");
};

/** FM/LH cell → task type. "LH"/"line" → LINE_HAUL; else FIRST_MILE. */
const parseTaskType = (cell: unknown): JobType => {
    const s = String(cell ?? "").trim().toUpperCase();
    return s.includes("LH") || s.includes("LINE") ? "LINE_HAUL" : "FIRST_MILE";
};

/** Parse a date or date-time cell (Excel serial or string) to a Date. */
const parseCellDate = (raw: unknown, withTime: boolean): Date | undefined => {
    if (raw == null || raw === "") return undefined;
    if (typeof raw === "number") {
        const dc = XLSX.SSF.parse_date_code(raw);
        if (!dc) return undefined;
        return new Date(dc.y, dc.m - 1, dc.d, withTime ? (dc.H ?? 0) : 0, withTime ? (dc.M ?? 0) : 0, 0);
    }
    const d = new Date(raw as string);
    return isNaN(d.getTime()) ? undefined : d;
};

export function JobImportDialog({ onSuccess }: ImportDialogProps) {
    const { t } = useLanguage();
    const [open, setOpen] = useState(false);
    const [file, setFile] = useState<File | null>(null);
    const [data, setData] = useState<ImportRow[]>([]);
    const [uploading, setUploading] = useState(false);
    const [progress, setProgress] = useState(0);
    const [error, setError] = useState<string | null>(null);
    const fileInputRef = useRef<HTMLInputElement>(null);
    const trucksRef = useRef<TaskTruck[]>([]);
    const hubsRef = useRef<Record<string, any>[]>([]);
    const customersRef = useRef<CustomerData[]>([]);
    const customersByIdRef = useRef<Map<string, CustomerData>>(new Map());
    const [customers, setCustomers] = useState<CustomerData[]>([]);

    useEffect(() => {
        if (!open) return;
        taskService.fetchTrucks().then((f) => { trucksRef.current = f; }).catch((e) => console.error("trucks", e));
        taskService.fetchHubs().then((h) => { hubsRef.current = h; }).catch((e) => console.error("hubs", e));
        getCustomers().then((cs) => {
            customersRef.current = cs;
            customersByIdRef.current = new Map(cs.map((c) => [c.id, c]));
            setCustomers(cs);
        }).catch((e) => console.error("customers", e));
    }, [open]);

    const resolveCustomer = (cell: unknown): CustomerData | undefined => {
        const s = String(cell ?? "").trim();
        if (!s) return undefined;
        const up = s.toUpperCase();
        const list = customersRef.current;
        return (
            list.find((c) => (c.code ?? "").toUpperCase() === up) ||
            list.find((c) => (c.name ?? "").toUpperCase() === up) ||
            list.find((c) => (c.code ?? "").toUpperCase().includes(up) || (c.name ?? "").toUpperCase().includes(up))
        );
    };

    const recompute = (row: ImportRow): ImportRow => {
        const isValid = !!(row.destination && row.sourceHub) && !row.plateUnknown && !!row.jobCategory && !!row.billingCustomerId;
        const invalidReason = row.plateUnknown
            ? t("firstMile.import.plateNotInFleet")
            : !row.jobCategory
                ? t("firstMile.import.jobCategoryUnknown")
                : !row.billingCustomerId
                    ? t("firstMile.import.customerMissing", "เลือกลูกค้า")
                    : undefined;
        return { ...row, isValid, invalidReason };
    };

    const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
        const f = e.target.files?.[0];
        if (f) { setFile(f); parseExcel(f); }
    };

    const parseExcel = async (file: File) => {
        try {
            const buf = await file.arrayBuffer();
            const wb = XLSX.read(buf);
            const sheet = wb.Sheets[wb.SheetNames[0]];
            const jsonData = XLSX.utils.sheet_to_json(sheet, { header: 1 }) as any[][];
            const headers = (jsonData[0] as string[]).map((h) => h?.toString().toLowerCase().trim());
            const rows = jsonData.slice(1);

            const parsed = rows.map((row: any, index): ImportRow | null => {
                if (!row || row.length === 0) return null;
                const getValue = (terms: string[]) => {
                    const idx = headers.findIndex((h) => terms.some((ph) => h?.includes(ph)));
                    return idx !== -1 ? row[idx] : undefined;
                };
                if (isExampleNote(getValue(["note", "หมายเหตุ"]))) return null;

                const taskType = parseTaskType(getValue(["fm/lh", "fmlh"]));
                const rawDate = getValue(["date", "วันแผน", "วัน"]);
                const actualRaw = getValue(["actual", "รับงานจริง", "รับจริง"]);
                const sourceHub = getValue(["source", "hub", "ต้นทาง", "pickup", "จุดรับงาน", "pickup location"]);
                const destination = getValue(["destination", "soc", "ปลายทาง"]);
                const customerCell = getValue(["customer", "ลูกค้า", "บริษัท"]);
                const sheetTruckType = getValue(["truck type", "ประเภทรถ", "platetype"]);
                const shipmentId = getValue(["shipment", "id", "เลขงาน"]);
                const licensePlate = getValue(["license", "ทะเบียน"]);
                const driverName = getValue(["driver", "name", "คนขับ"]);
                const driverPhone = getValue(["phone", "tel", "เบอร์"]);
                const jobCategory = jobCategoryFromCell(getValue(["job category", "หลัก/เสริม", "jobcategory"]));

                const plateKey = normalizePlate(licensePlate);
                const matchedTruck = plateKey ? trucksRef.current.find((tr) => normalizePlate(tr.licensePlate) === plateKey) : undefined;
                const truckType = matchedTruck ? taskTruckTypeFromTruckDoc(matchedTruck.type) : (sheetTruckType ? String(sheetTruckType).toUpperCase() : undefined);

                const planDate = parseCellDate(rawDate, false) ?? new Date();

                // SOC normalization applies to a FIRST_MILE destination (which is a SOC). A LINE_HAUL
                // destination is a hub and is left as typed.
                let dest = destination;
                if (destination && taskType === "FIRST_MILE") {
                    const dStr = String(destination).toUpperCase();
                    if (dStr.includes("E") || dStr.includes("BUEROI")) dest = "SOC-E";
                    else if (dStr.includes("N") || dStr.includes("WANG")) dest = "SOC-N";
                    else if (dStr.includes("W") || dStr.includes("SAMUT")) dest = "SOC-W";
                }

                const customer = resolveCustomer(customerCell);

                return recompute({
                    id: index,
                    taskType,
                    date: planDate,
                    actualPickupAt: parseCellDate(actualRaw, true),
                    sourceHub,
                    destination: dest,
                    truckType,
                    truckId: matchedTruck?.id,
                    jobCategory,
                    taskId: shipmentId ? String(shipmentId) : undefined,
                    licensePlate: matchedTruck?.licensePlate ?? licensePlate,
                    driverName,
                    driverPhone,
                    billingCustomerId: customer?.id,
                    billingCustomerName: customer?.name,
                    billingCustomerCode: customer?.code,
                    plateUnknown: !!plateKey && !matchedTruck,
                    isValid: false,
                });
            }).filter((r): r is ImportRow => r !== null);

            setData(parsed);
            setError(null);
        } catch (err) {
            console.error(err);
            setError(t("firstMile.import.parseError"));
        }
    };

    const setRowType = (rowId: number, taskType: JobType) =>
        setData((prev) => prev.map((r) => r.id === rowId ? recompute({ ...r, taskType }) : r));
    const setRowCustomer = (rowId: number, customerId: string) => {
        const c = customersByIdRef.current.get(customerId);
        setData((prev) => prev.map((r) => r.id === rowId ? recompute({ ...r, billingCustomerId: c?.id, billingCustomerName: c?.name, billingCustomerCode: c?.code }) : r));
    };
    const setRowDate = (rowId: number, value: string) => {
        const d = new Date(`${value}T00:00:00`);
        if (isNaN(d.getTime())) return;
        setData((prev) => prev.map((r) => r.id === rowId ? recompute({ ...r, date: d }) : r));
    };

    const handleDownloadTemplate = () => {
        const headers = [
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
        const example = [
            "FM",
            format(new Date(), "dd/MM/yyyy"),
            `${format(new Date(), "dd/MM/yyyy")} 15:30`,
            "SPX ตัวอย่าง",
            "SOCE",
            "CJSF",
            "หลัก",
            "6WH",
            "",
            "70-1234",
            "สมชาย ใจดี",
            "0812345678",
            "*ตัวอย่าง — ลบแถวนี้ก่อน import",
        ];
        const ws = XLSXStyle.utils.aoa_to_sheet([headers, example]);
        const yellow = { fill: { patternType: "solid", fgColor: { rgb: "FFF3CD" } } };
        for (let c = 0; c < headers.length; c++) {
            const addr = XLSXStyle.utils.encode_cell({ r: 1, c });
            if (ws[addr]) ws[addr].s = yellow as any;
        }
        const noteAddr = XLSXStyle.utils.encode_cell({ r: 1, c: headers.length - 1 });
        if (ws[noteAddr]) ws[noteAddr].s = { fill: { patternType: "solid", fgColor: { rgb: "FFE08A" } }, font: { bold: true, color: { rgb: "B00020" } } } as any;
        const wb = XLSXStyle.utils.book_new();
        XLSXStyle.utils.book_append_sheet(wb, ws, "Template");
        XLSXStyle.writeFile(wb, "JobAssign_Template.xlsx");
    };

    const buildHubLinks = (row: ImportRow) => {
        const src = hubsRef.current.find((h) => String(h["Hub Code"] ?? "").trim() === String(row.sourceHub ?? "").trim());
        const dst = hubsRef.current.find((h) => String(h["Hub Code"] ?? "").trim() === String(row.destination ?? "").trim());
        const srcC = src?.linkedCustomerId ? customersByIdRef.current.get(src.linkedCustomerId) : undefined;
        const dstC = dst?.linkedCustomerId ? customersByIdRef.current.get(dst.linkedCustomerId) : undefined;
        return {
            ...(src?.linkedCustomerId ? { sourceHubLinkedCustomerId: src.linkedCustomerId } : {}),
            ...(srcC?.name ? { sourceHubLinkedCustomerName: srcC.name } : {}),
            ...(srcC?.code ? { sourceHubLinkedCustomerCode: srcC.code } : {}),
            ...(src?.customerLinkKind ? { sourceHubCustomerLinkKind: src.customerLinkKind } : {}),
            ...(dst?.linkedCustomerId ? { destinationLinkedCustomerId: dst.linkedCustomerId } : {}),
            ...(dstC?.name ? { destinationLinkedCustomerName: dstC.name } : {}),
            ...(dstC?.code ? { destinationLinkedCustomerCode: dstC.code } : {}),
            ...(dst?.customerLinkKind ? { destinationCustomerLinkKind: dst.customerLinkKind } : {}),
        };
    };

    const handleUpload = async () => {
        const validRows = data.filter((r) => r.isValid);
        if (validRows.length === 0) return;
        setUploading(true);
        setProgress(5);
        try {
            // Human taskId per row (FM/LH-ddMMyyyy-NNN) where the sheet gave none — count per day once.
            const dayCounters = new Map<string, number>();
            const taskIdByRow = new Map<number, string>();
            for (const row of validRows) {
                if (row.taskId && row.taskId.trim()) { taskIdByRow.set(row.id, row.taskId.trim()); continue; }
                const dateStr = format(row.date, "ddMMyyyy");
                if (!dayCounters.has(dateStr)) {
                    const s = new Date(row.date); s.setHours(0, 0, 0, 0);
                    const e = new Date(row.date); e.setHours(23, 59, 59, 999);
                    dayCounters.set(dateStr, await taskService.countTasksForDay(s, e));
                }
                const n = (dayCounters.get(dateStr) ?? 0) + 1;
                dayCounters.set(dateStr, n);
                const prefix = row.taskType === "LINE_HAUL" ? "LH" : "FM";
                taskIdByRow.set(row.id, `${prefix}-${dateStr}-${String(n).padStart(3, "0")}`);
            }
            setProgress(15);

            const batchSize = 400;
            const totalBatches = Math.ceil(validRows.length / batchSize);
            for (let i = 0; i < totalBatches; i++) {
                const batch = writeBatch(db);
                const chunk = validRows.slice(i * batchSize, (i + 1) * batchSize);
                chunk.forEach((row) => {
                    const docRef = doc(collection(db, COLLECTIONS.TASKS));
                    const dateStr = format(row.date, "ddMMyyyy");
                    batch.set(docRef, {
                        date: row.date,
                        dateStr,
                        // Plan has no time; carry it from the actual pickup (ADR 0028), default 00:00.
                        time: row.actualPickupAt ? format(row.actualPickupAt, "HH:mm") : "00:00",
                        ...(row.actualPickupAt ? { actualPickupAt: row.actualPickupAt } : {}),
                        sourceHub: row.sourceHub,
                        destination: row.destination,
                        jobCategory: row.jobCategory,
                        truckType: row.truckType || "",
                        ...(row.truckId ? { truckId: row.truckId } : {}),
                        taskId: taskIdByRow.get(row.id) || "",
                        licensePlate: row.licensePlate || "",
                        driverName: row.driverName || "",
                        driverPhone: row.driverPhone || "",
                        billingCustomerId: row.billingCustomerId,
                        billingCustomerName: row.billingCustomerName ?? "",
                        billingCustomerCode: row.billingCustomerCode ?? "",
                        ...buildHubLinks(row),
                        status: "Pending",
                        taskType: row.taskType,
                        isMultiDelivery: false,
                        createdAt: new Date(),
                        updatedAt: new Date(),
                    });
                });
                await batch.commit();
                setProgress(15 + Math.round(((i + 1) / totalBatches) * 85));
            }

            setUploading(false);
            setOpen(false);
            onSuccess();
            setData([]);
            setFile(null);
        } catch (err) {
            console.error(err);
            setUploading(false);
            setError(t("firstMile.import.pushError"));
        }
    };

    const validCount = data.filter((d) => d.isValid).length;

    return (
        <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger asChild>
                <Button variant="outline" className="gap-2">
                    <FileSpreadsheet className="h-4 w-4" />
                    {t("jobAssign.import.button", "นำเข้าจากไฟล์")}
                </Button>
            </DialogTrigger>
            <DialogContent className="max-w-5xl max-h-[85vh] flex flex-col">
                <DialogHeader>
                    <DialogTitle>{t("jobAssign.import.title", "นำเข้างาน (FM + LH ไฟล์เดียว)")}</DialogTitle>
                    <DialogDescription>{t("jobAssign.import.description", "ไฟล์เดียว ใส่คอลัมน์ FM/LH ต่อแถวเพื่อแยกประเภทงาน")}</DialogDescription>
                </DialogHeader>

                <div className="flex-1 overflow-hidden flex flex-col gap-4">
                    {!file ? (
                        <div className="flex flex-col gap-4 h-full">
                            <div className="flex justify-between items-center">
                                <span className="text-sm text-muted-foreground">{t("firstMile.import.templateHint")}</span>
                                <Button variant="outline" size="sm" onClick={handleDownloadTemplate} className="gap-2">
                                    <Download className="h-4 w-4" />
                                    {t("firstMile.import.downloadTemplate")}
                                </Button>
                            </div>
                            <div
                                className="border-2 border-dashed rounded-lg p-12 flex flex-col items-center justify-center text-muted-foreground hover:bg-muted/50 transition-colors cursor-pointer"
                                onClick={() => fileInputRef.current?.click()}
                            >
                                <Upload className="h-12 w-12 mb-4 text-gray-400" />
                                <p className="font-medium text-lg">{t("firstMile.import.clickUpload")}</p>
                                <p className="text-sm">{t("firstMile.import.formatsSupported")}</p>
                                <input ref={fileInputRef} type="file" accept=".xlsx, .xls" className="hidden" onChange={handleFileChange} />
                            </div>
                        </div>
                    ) : (
                        <div className="flex flex-col gap-4 h-full">
                            <div className="flex items-center justify-between bg-muted/30 p-3 rounded-md border">
                                <div className="flex items-center gap-3">
                                    <div className="bg-green-100 p-2 rounded"><FileSpreadsheet className="h-5 w-5 text-green-600" /></div>
                                    <div>
                                        <p className="font-medium text-sm">{file.name}</p>
                                        <p className="text-xs text-muted-foreground">{data.length} {t("firstMile.import.recordsFound")}</p>
                                    </div>
                                </div>
                                <Button variant="ghost" size="icon" onClick={() => { setFile(null); setData([]); }}><X className="h-4 w-4" /></Button>
                            </div>

                            {error && (
                                <Alert variant="destructive">
                                    <AlertCircle className="h-4 w-4" />
                                    <AlertTitle>{t("firstMile.import.error")}</AlertTitle>
                                    <AlertDescription>{error}</AlertDescription>
                                </Alert>
                            )}

                            <div className="border rounded-md flex-1 overflow-hidden">
                                <ScrollArea className="h-[420px]">
                                    <Table>
                                        <TableHeader>
                                            <TableRow className="bg-muted/50">
                                                <TableHead>{t("firstMile.import.table.row")}</TableHead>
                                                <TableHead>{t("jobAssign.table.jobType", "ชนิดงาน")}</TableHead>
                                                <TableHead>{t("firstMile.task.date", "วันแผนงาน")}</TableHead>
                                                <TableHead>{t("jobAssign.table.actualPickup", "วันรับงานจริง")}</TableHead>
                                                <TableHead>{t("firstMile.task.customer", "ลูกค้า")}</TableHead>
                                                <TableHead>{t("firstMile.import.table.source")}</TableHead>
                                                <TableHead>{t("firstMile.import.table.dest")}</TableHead>
                                                <TableHead>{t("firstMile.import.table.jobCategory")}</TableHead>
                                                <TableHead>{t("firstMile.import.table.truckType")}</TableHead>
                                                <TableHead>{t("firstMile.import.table.status")}</TableHead>
                                            </TableRow>
                                        </TableHeader>
                                        <TableBody>
                                            {data.map((row) => (
                                                <TableRow key={row.id} className={!row.isValid ? "bg-red-50" : ""}>
                                                    <TableCell className="font-mono text-xs">{row.id + 1}</TableCell>
                                                    <TableCell>
                                                        <select
                                                            className="h-8 text-xs rounded border bg-background px-2"
                                                            value={row.taskType}
                                                            onChange={(e) => setRowType(row.id, e.target.value as JobType)}
                                                        >
                                                            <option value="FIRST_MILE">FM</option>
                                                            <option value="LINE_HAUL">LH</option>
                                                        </select>
                                                    </TableCell>
                                                    <TableCell>
                                                        <Input type="date" className="h-8 text-xs w-36" value={format(row.date, "yyyy-MM-dd")} onChange={(e) => setRowDate(row.id, e.target.value)} />
                                                    </TableCell>
                                                    <TableCell className="text-xs whitespace-nowrap">
                                                        {row.actualPickupAt ? format(row.actualPickupAt, "dd/MM/yy HH:mm") : <span className="text-muted-foreground">-</span>}
                                                    </TableCell>
                                                    <TableCell>
                                                        <select
                                                            className={`h-8 text-xs rounded border bg-background px-2 min-w-[140px] ${!row.billingCustomerId ? "border-red-400" : ""}`}
                                                            value={row.billingCustomerId ?? ""}
                                                            onChange={(e) => setRowCustomer(row.id, e.target.value)}
                                                        >
                                                            <option value="">{t("firstMile.task.selectCustomer", "เลือกลูกค้า")}</option>
                                                            {customers.map((c) => (
                                                                <option key={c.id} value={c.id}>{c.code ? `${c.code} — ${c.name}` : c.name}</option>
                                                            ))}
                                                        </select>
                                                    </TableCell>
                                                    <TableCell>{row.sourceHub}</TableCell>
                                                    <TableCell>
                                                        <span className={row.destination ? "text-green-600 font-medium" : "text-red-500"}>
                                                            {row.destination || t("firstMile.import.unknown")}
                                                        </span>
                                                    </TableCell>
                                                    <TableCell>
                                                        {row.jobCategory
                                                            ? t(`firstMile.task.jobCategory.${row.jobCategory === "SUPPLEMENTARY" ? "supplementary" : "primary"}`)
                                                            : <span className="text-red-500">{t("firstMile.import.unknown")}</span>}
                                                    </TableCell>
                                                    <TableCell>{row.truckType}</TableCell>
                                                    <TableCell>
                                                        {row.isValid ? <Check className="h-4 w-4 text-green-500" /> : <span className="text-xs text-red-500 font-medium">{row.invalidReason || t("firstMile.import.invalid")}</span>}
                                                    </TableCell>
                                                </TableRow>
                                            ))}
                                        </TableBody>
                                    </Table>
                                </ScrollArea>
                            </div>
                        </div>
                    )}
                </div>

                <DialogFooter className="mt-4">
                    {uploading ? (
                        <div className="w-full space-y-2">
                            <div className="flex justify-between text-xs">
                                <span>{t("firstMile.import.uploading")}</span>
                                <span>{progress}%</span>
                            </div>
                            <Progress value={progress} />
                        </div>
                    ) : (
                        <>
                            <Button variant="outline" onClick={() => setOpen(false)}>{t("firstMile.import.cancel")}</Button>
                            <Button onClick={handleUpload} disabled={!file || validCount === 0 || uploading}>
                                {t("firstMile.import.upload")} {validCount} {t("firstMile.import.records")}
                            </Button>
                        </>
                    )}
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

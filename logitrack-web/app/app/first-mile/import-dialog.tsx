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

/** Plates are typed inconsistently (spaces, dashes, case) — compare on a normalized form. */
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
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Progress } from "@/components/ui/progress";

interface ImportDialogProps {
    onSuccess: () => void;
}

interface ImportRow {
    id: number;
    date: Date;
    sourceHub?: string;
    destination?: string;
    time?: string;
    truckType?: string;
    truckId?: string;
    jobCategory?: "PRIMARY" | "SUPPLEMENTARY";
    taskId?: string;
    licensePlate?: string;
    driverName?: string;
    driverPhone?: string;
    // Explicit billing customer (ADR 0027) — chosen from the file's Customer column, editable inline.
    billingCustomerId?: string;
    billingCustomerName?: string;
    billingCustomerCode?: string;
    plateUnknown?: boolean;
    isValid: boolean;
    invalidReason?: string;
}

/** A row whose Note cell carries the "delete before import" example marker is skipped by the parser. */
const isExampleNote = (note: unknown) => {
    const s = String(note ?? "").toLowerCase();
    return s.includes("ตัวอย่าง") || s.includes("example") || s.includes("ลบ") || s.includes("delete");
};

export function FirstMileImportDialog({ onSuccess }: ImportDialogProps) {
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
    // State copy for the render (dropdown options); the refs are read only in handlers/parse.
    const [customers, setCustomers] = useState<CustomerData[]>([]);

    useEffect(() => {
        if (!open) return;
        taskService.fetchTrucks().then((fleet) => { trucksRef.current = fleet; }).catch((err) => console.error("Failed to load trucks for import", err));
        taskService.fetchHubs().then((hubs) => { hubsRef.current = hubs; }).catch((err) => console.error("Failed to load hubs for import", err));
        getCustomers().then((cs) => {
            customersRef.current = cs;
            customersByIdRef.current = new Map(cs.map((c) => [c.id, c]));
            setCustomers(cs);
        }).catch((err) => console.error("Failed to load customers for import", err));
    }, [open]);

    /** Resolve a Customer cell (code or name, case-insensitive) to a customer doc. */
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
        const selectedFile = e.target.files?.[0];
        if (selectedFile) {
            setFile(selectedFile);
            parseExcel(selectedFile);
        }
    };

    const parseExcel = async (file: File) => {
        try {
            const buf = await file.arrayBuffer();
            const workbook = XLSX.read(buf);
            const sheet = workbook.Sheets[workbook.SheetNames[0]];
            const jsonData = XLSX.utils.sheet_to_json(sheet, { header: 1 }) as any[][];
            const headers = (jsonData[0] as string[]).map((h) => h?.toString().toLowerCase().trim());
            const rows = jsonData.slice(1);

            const parsedRows = rows.map((row: any, index): ImportRow | null => {
                if (!row || row.length === 0) return null;
                const getValue = (terms: string[]) => {
                    const idx = headers.findIndex((h) => terms.some((ph) => h?.includes(ph)));
                    return idx !== -1 ? row[idx] : undefined;
                };

                // Skip the template's highlighted example row (marked "delete before import").
                if (isExampleNote(getValue(["note", "หมายเหตุ"]))) return null;

                const rawDate = getValue(["date", "วัน"]);
                const sourceHub = getValue(["source", "hub", "ต้นทาง", "pickup", "จุดรับงาน", "pickup location"]);
                const destination = getValue(["destination", "soc", "ปลายทาง"]);
                const time = getValue(["time", "เวลา"]);
                const customerCell = getValue(["customer", "ลูกค้า", "บริษัท"]);
                const sheetTruckType = getValue(["truck type", "ประเภทรถ", "platetype"]);
                const shipmentId = getValue(["shipment", "id", "เลขงาน"]);
                const licensePlate = getValue(["license", "ทะเบียน"]);
                const driverName = getValue(["driver", "name", "คนขับ"]);
                const driverPhone = getValue(["phone", "tel", "เบอร์"]);
                const jobCategory = jobCategoryFromCell(getValue(["job category", "ประเภทงาน", "jobcategory"]));

                const plateKey = normalizePlate(licensePlate);
                const matchedTruck = plateKey
                    ? trucksRef.current.find((truck) => normalizePlate(truck.licensePlate) === plateKey)
                    : undefined;
                const truckType = matchedTruck
                    ? taskTruckTypeFromTruckDoc(matchedTruck.type)
                    : (sheetTruckType ? String(sheetTruckType).toUpperCase() : undefined);

                let formattedDate = new Date();
                if (rawDate) {
                    if (typeof rawDate === "number") {
                        const dc = XLSX.SSF.parse_date_code(rawDate);
                        formattedDate = new Date(dc.y, dc.m - 1, dc.d);
                    } else {
                        const d = new Date(rawDate);
                        if (!isNaN(d.getTime())) formattedDate = d;
                    }
                }

                let matchedSOC = destination;
                if (destination) {
                    const dStr = String(destination).toUpperCase();
                    if (dStr.includes("E") || dStr.includes("BUEROI")) matchedSOC = "SOC-E";
                    else if (dStr.includes("N") || dStr.includes("WANG")) matchedSOC = "SOC-N";
                    else if (dStr.includes("W") || dStr.includes("SAMUT")) matchedSOC = "SOC-W";
                }

                const customer = resolveCustomer(customerCell);

                return recompute({
                    id: index,
                    date: formattedDate,
                    sourceHub,
                    destination: matchedSOC,
                    time,
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

            setData(parsedRows);
            setError(null);
        } catch (err) {
            console.error(err);
            setError(t("firstMile.import.parseError"));
        }
    };

    const setRowCustomer = (rowId: number, customerId: string) => {
        const c = customersByIdRef.current.get(customerId);
        setData((prev) => prev.map((r) => r.id === rowId
            ? recompute({ ...r, billingCustomerId: c?.id, billingCustomerName: c?.name, billingCustomerCode: c?.code })
            : r));
    };
    const setRowDate = (rowId: number, value: string) => {
        const d = new Date(`${value}T00:00:00`);
        if (isNaN(d.getTime())) return;
        setData((prev) => prev.map((r) => r.id === rowId ? recompute({ ...r, date: d }) : r));
    };

    const handleDownloadTemplate = () => {
        const headers = [
            "Date (วัน)",
            "Pickup Location (จุดรับงาน)",
            "Destination (ปลายทาง)",
            "Time (เวลา)",
            "Customer (ลูกค้า: รหัส/ชื่อ)",
            "Job Category (ประเภทงาน: หลัก/เสริม)",
            "Truck Type (ประเภทรถ)",
            "Shipment ID (เลขงาน)",
            "License Plate (ทะเบียน)",
            "Driver Name (คนขับ)",
            "Driver Phone (เบอร์)",
            "Note (หมายเหตุ)",
        ];
        const example = [
            format(new Date(), "dd/MM/yyyy"),
            "SPX ตัวอย่าง",
            "SOCE",
            "15:00",
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
        XLSXStyle.writeFile(wb, "FirstMileTask_Template.xlsx");
    };

    /** Resolve hub-linked customer fields (for customer-scope filtering) from the hubs master. */
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
            // Assign a human taskId (FM-ddMMyyyy-NNN) where the sheet gave none. Count per day once,
            // then increment locally (a batch write won't be visible to countTasksForDay mid-run).
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
                taskIdByRow.set(row.id, `FM-${dateStr}-${String(n).padStart(3, "0")}`);
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
                        sourceHub: row.sourceHub,
                        destination: row.destination,
                        time: row.time || "",
                        jobCategory: row.jobCategory,
                        truckType: row.truckType || "",
                        ...(row.truckId ? { truckId: row.truckId } : {}),
                        taskId: taskIdByRow.get(row.id) || "",
                        licensePlate: row.licensePlate || "",
                        driverName: row.driverName || "",
                        driverPhone: row.driverPhone || "",
                        // Explicit billing customer (ADR 0027) — authoritative for billing.
                        billingCustomerId: row.billingCustomerId,
                        billingCustomerName: row.billingCustomerName ?? "",
                        billingCustomerCode: row.billingCustomerCode ?? "",
                        // Hub-linked customer (used by customer-scope filtering) resolved from masters.
                        ...buildHubLinks(row),
                        status: "Pending",
                        taskType: "FIRST_MILE",
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
                    {t("firstMile.import.button")}
                </Button>
            </DialogTrigger>
            <DialogContent className="max-w-5xl max-h-[85vh] flex flex-col">
                <DialogHeader>
                    <DialogTitle>{t("firstMile.import.title")}</DialogTitle>
                    <DialogDescription>{t("firstMile.import.description")}</DialogDescription>
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
                                    <div className="bg-green-100 p-2 rounded">
                                        <FileSpreadsheet className="h-5 w-5 text-green-600" />
                                    </div>
                                    <div>
                                        <p className="font-medium text-sm">{file.name}</p>
                                        <p className="text-xs text-muted-foreground">{data.length} {t("firstMile.import.recordsFound")}</p>
                                    </div>
                                </div>
                                <Button variant="ghost" size="icon" onClick={() => { setFile(null); setData([]); }}>
                                    <X className="h-4 w-4" />
                                </Button>
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
                                                <TableHead>{t("firstMile.import.table.date")}</TableHead>
                                                <TableHead>{t("firstMile.task.customer", "ลูกค้า")}</TableHead>
                                                <TableHead>{t("firstMile.import.table.source")}</TableHead>
                                                <TableHead>{t("firstMile.import.table.dest")}</TableHead>
                                                <TableHead>{t("firstMile.import.table.time")}</TableHead>
                                                <TableHead>{t("firstMile.import.table.jobCategory")}</TableHead>
                                                <TableHead>{t("firstMile.import.table.truckType")}</TableHead>
                                                <TableHead>{t("firstMile.import.table.driver")}</TableHead>
                                                <TableHead>{t("firstMile.import.table.status")}</TableHead>
                                            </TableRow>
                                        </TableHeader>
                                        <TableBody>
                                            {data.map((row) => (
                                                <TableRow key={row.id} className={!row.isValid ? "bg-red-50" : ""}>
                                                    <TableCell className="font-mono text-xs">{row.id + 1}</TableCell>
                                                    <TableCell>
                                                        <Input
                                                            type="date"
                                                            className="h-8 text-xs w-36"
                                                            value={format(row.date, "yyyy-MM-dd")}
                                                            onChange={(e) => setRowDate(row.id, e.target.value)}
                                                        />
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
                                                    <TableCell>{row.time}</TableCell>
                                                    <TableCell>
                                                        {row.jobCategory
                                                            ? t(`firstMile.task.jobCategory.${row.jobCategory === "SUPPLEMENTARY" ? "supplementary" : "primary"}`)
                                                            : <span className="text-red-500">{t("firstMile.import.unknown")}</span>}
                                                    </TableCell>
                                                    <TableCell>{row.truckType}</TableCell>
                                                    <TableCell>{row.driverName}</TableCell>
                                                    <TableCell>
                                                        {row.isValid ? (
                                                            <Check className="h-4 w-4 text-green-500" />
                                                        ) : (
                                                            <span className="text-xs text-red-500 font-medium">{row.invalidReason || t("firstMile.import.invalid")}</span>
                                                        )}
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

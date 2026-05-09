"use client";

import { useState, useRef } from "react";
import * as XLSX from "xlsx";
import { Upload, X, FileSpreadsheet, Check, AlertCircle, Download } from "lucide-react";
import { collection, writeBatch, doc } from "firebase/firestore";
import { db } from "@/firebase/client";
import { useLanguage } from "@/context/language";
import type { CustomerLinkKind, StationType } from "@/validate/hubSchema";
import { COLLECTIONS } from "@/lib/collections";
import { getAllCustomersForCodeLookup } from "@/features/customers/api/customers";

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

const STATION_TYPES: StationType[] = ["HUB", "SOC"];

function normalizeStationType(value: unknown): StationType {
    const v = String(value ?? "").toUpperCase().trim();
    if (v === "SOC") return "SOC";
    return "HUB";
}

function parseCustomerLinkKind(value: unknown): CustomerLinkKind {
    const v = String(value ?? "").toLowerCase().trim();
    if (v === "partner" || v === "p" || v === "subcontractor") return "partner";
    return "customer";
}

function parseNum(val: unknown): number | undefined {
    if (val == null || val === "") return undefined;
    const n = Number(val);
    return Number.isFinite(n) ? n : undefined;
}

/** หัวคอลัมน์จาก Excel: ตัด BOM, ช่องว่างแปลกๆ, วงเล็บแบบเต็มความกว้าง → เปรียบเทียบแบบ tolerant */
function normalizeHeaderCell(h: unknown): string {
    return String(h ?? "")
        .replace(/^\uFEFF/, "")
        .replace(/\u00A0/g, " ")
        .replace(/[\u2000-\u200B\u202F\u205F\u3000]/g, " ")
        .replace(/\uFF08/g, "(")
        .replace(/\uFF09/g, ")")
        .toLowerCase()
        .replace(/\s+/g, " ")
        .trim();
}

/** จับคู่หัวคอลัมน์กับ keyword แบบไม่ strict ต่อช่องว่าง (ลดปัญหา Excel / สำเนาจากเว็บ) */
function headerMatches(headerNorm: string, keywordRaw: string): boolean {
    const k = normalizeHeaderCell(keywordRaw);
    if (!k) return false;
    if (headerNorm.includes(k)) return true;
    if (k.length < 2) return false;
    const hc = headerNorm.replace(/\s/g, "");
    const kc = k.replace(/\s/g, "");
    return kc.length >= 2 && hc.includes(kc);
}

interface ParsedRow {
    id: number;
    firestoreDocId: string;
    source_id: string;
    source_name_en: string;
    source_name_th: string | undefined;
    hasThaiColumn: boolean;
    latitude: number | undefined;
    longitude: number | undefined;
    station_type: StationType;
    /** รหัสลูกค้าใน Excel (สำหรับแสดงใน preview) */
    customerCodeCell: string;
    linkedCustomerId: string | undefined;
    customerLinkKind: CustomerLinkKind;
    isValid: boolean;
    invalidDetail?: string;
}

interface PickupImportDialogProps {
    onSuccess: () => void;
}

export function PickupLocationImportDialog({ onSuccess }: PickupImportDialogProps) {
    const { t } = useLanguage();
    const [open, setOpen] = useState(false);
    const [file, setFile] = useState<File | null>(null);
    const [data, setData] = useState<ParsedRow[]>([]);
    const [uploading, setUploading] = useState(false);
    const [progress, setProgress] = useState(0);
    const [error, setError] = useState<string | null>(null);
    const fileInputRef = useRef<HTMLInputElement>(null);

    const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
        const selectedFile = e.target.files?.[0];
        if (selectedFile) {
            setFile(selectedFile);
            parseExcel(selectedFile);
        }
    };

    const parseExcel = async (file: File) => {
        try {
            const buffer = await file.arrayBuffer();
            const workbook = XLSX.read(buffer);
            const sheetName = workbook.SheetNames[0];
            const sheet = workbook.Sheets[sheetName];
            const jsonData = XLSX.utils.sheet_to_json(sheet, { header: 1 }) as unknown[][];
            if (!jsonData.length || !Array.isArray(jsonData[0])) {
                setData([]);
                setError(t("firstMile.sourcesImport.emptyOrInvalidSheet"));
                return;
            }

            let customers: Awaited<ReturnType<typeof getAllCustomersForCodeLookup>>;
            try {
                customers = await getAllCustomersForCodeLookup();
            } catch (ce) {
                console.error("[PickupImport] load customers", ce);
                setData([]);
                setError(t("firstMile.hub.loadCustomersFailed"));
                return;
            }
            const codeToId = new Map<string, string>();
            for (const c of customers) {
                const k = String(c.code ?? "").trim().toUpperCase().replace(/\s+/g, " ").trim();
                if (k && !codeToId.has(k)) codeToId.set(k, c.id);
                const kns = k.replace(/\s/g, "");
                if (kns && kns !== k && !codeToId.has(kns)) codeToId.set(kns, c.id);
            }

            const headers = (jsonData[0] as unknown[]).map((h) => normalizeHeaderCell(h));
            const rows = jsonData.slice(1) as unknown[][];

            const getCol = (keywords: string[]) => {
                const idx = headers.findIndex((h) => keywords.some((kw) => headerMatches(h, kw)));
                return idx !== -1 ? idx : -1;
            };

            const colFirestoreDocId = getCol([
                "firestore doc",
                "doc id",
                "document id",
                "hub document",
            ]);
            const colSourceId = getCol([
                "pdp id",
                "source_id",
                "source id",
                "pickup id",
                "station id",
                "รหัสจุด",
                "รหัสสถานี",
            ]);
            const colName = getCol([
                "pdp name",
                "point name",
                "name",
                "spx",
                "ชื่อ",
                "source_name",
                "name (spx)",
            ]);
            const colThai = getCol([
                "ชื่อ (ไทย)",
                "name (thai)",
                "name(thai)",
                "name thai",
                "thai",
                "ชื่อไทย",
                "(ไทย)",
                "source_name_th",
            ]);
            const colLat = getCol(["lat", "latitude", "ละติจูด"]);
            const colLng = getCol(["lng", "long", "longitude", "ลองจิจูด"]);
            const colType = getCol(["station", "type", "ประเภท", "station_type"]);
            const colLinkedId = getCol([
                "linked customer id",
                "linkedcustomerid",
                "linked_customer_id",
                "customer doc id",
            ]);
            const colCustomerCode = getCol([
                "customer code",
                "รหัสลูกค้า",
                "customer_code",
                "cust code",
            ]);
            const colLinkKind = getCol([
                "link kind",
                "customerlinkkind",
                "customer_link_kind",
                "link type",
                "ประเภทการผูก",
                "การผูก",
            ]);

            let displayIndex = 0;
            const parsed: ParsedRow[] = rows.flatMap((row) => {
                if (!row || row.length === 0) return [];
                const get = (col: number) => (col >= 0 ? row[col] : undefined);
                const id = displayIndex++;

                const firestoreDocId =
                    colFirestoreDocId >= 0 ? String(get(colFirestoreDocId) ?? "").trim() : "";
                const source_id = String(get(colSourceId) ?? "").trim();
                const source_name_en = String(get(colName) ?? "").trim();
                const hasThaiColumn = colThai >= 0;
                const source_name_th = hasThaiColumn ? String(get(colThai) ?? "").trim() : undefined;
                const latitude = parseNum(get(colLat));
                const longitude = parseNum(get(colLng));
                const station_type = normalizeStationType(get(colType));

                const linkedRaw = colLinkedId >= 0 ? String(get(colLinkedId) ?? "").trim() : "";
                const codeRaw = colCustomerCode >= 0 ? String(get(colCustomerCode) ?? "").trim() : "";
                const customerCodeCell = colCustomerCode >= 0 ? codeRaw : "";
                const codeNorm = codeRaw.toUpperCase().replace(/\s+/g, " ").trim();
                const codeNoSpace = codeNorm.replace(/\s/g, "");

                let linkedCustomerId: string | undefined;
                if (linkedRaw) linkedCustomerId = linkedRaw;
                else if (codeNorm) {
                    linkedCustomerId = codeToId.get(codeNorm) ?? codeToId.get(codeNoSpace);
                }

                const customerLinkKind =
                    colLinkKind >= 0 ? parseCustomerLinkKind(get(colLinkKind)) : "customer";

                let invalidDetail: string | undefined;
                if (codeNorm && !linkedRaw && !linkedCustomerId) {
                    invalidDetail = t("firstMile.sourcesImport.invalidCustomerCode");
                }

                const isValid =
                    source_id.length > 0 && source_name_en.length > 0 && invalidDetail == null;

                const one: ParsedRow = {
                    id,
                    firestoreDocId,
                    source_id,
                    source_name_en,
                    source_name_th,
                    hasThaiColumn,
                    latitude,
                    longitude,
                    station_type,
                    customerCodeCell,
                    linkedCustomerId,
                    customerLinkKind,
                    isValid,
                    invalidDetail,
                };
                return [one];
            });

            setData(parsed);
            setError(null);
        } catch (err) {
            console.error(err);
            setError(t("firstMile.sourcesImport.parseError"));
        }
    };

    const handleDownloadTemplate = () => {
        const headers = [
            t("firstMile.sources.export.firestoreDocId"),
            t("firstMile.sourcesImport.table.sourceId"),
            t("firstMile.sourcesImport.table.nameSPX"),
            t("firstMile.sourcesImport.table.nameThai"),
            "Latitude",
            "Longitude",
            t("firstMile.sourcesImport.table.stationType"),
            t("firstMile.sources.export.customerCode"),
        ];
        const exampleRow = ["", "ALANG-A", "Wang Thong Lang A", "", "13.7563", "100.5018", "HUB", "SPX"];
        const ws = XLSX.utils.aoa_to_sheet([headers, exampleRow]);
        const wb = XLSX.utils.book_new();
        XLSX.utils.book_append_sheet(wb, ws, "Pickup Locations");
        XLSX.writeFile(wb, "PickupLocation_Template.xlsx");
    };

    const handleUpload = async () => {
        const validRows = data.filter((r) => r.isValid);
        if (validRows.length === 0) return;
        setUploading(true);
        setProgress(10);

        try {
            const batchSize = 500;
            const totalBatches = Math.ceil(validRows.length / batchSize);

            for (let i = 0; i < totalBatches; i++) {
                const batch = writeBatch(db);
                const chunk = validRows.slice(i * batchSize, (i + 1) * batchSize);

                chunk.forEach((row) => {
                    const isUpdate = row.firestoreDocId.length > 0;
                    const docRef = isUpdate
                        ? doc(db, COLLECTIONS.HUBS, row.firestoreDocId)
                        : doc(collection(db, COLLECTIONS.HUBS));

                    const payload: Record<string, unknown> = {
                        source_id: row.source_id,
                        source_name_en: row.source_name_en,
                        latitude: row.latitude ?? null,
                        longitude: row.longitude ?? null,
                        station_type: row.station_type,
                        updatedAt: new Date(),
                    };

                    if (row.hasThaiColumn) {
                        payload.source_name_th = row.source_name_th || null;
                    }

                    if (row.linkedCustomerId) {
                        payload.linkedCustomerId = row.linkedCustomerId;
                        payload.customerLinkKind = row.customerLinkKind;
                    }

                    if (isUpdate) {
                        batch.set(docRef, payload, { merge: true });
                    } else {
                        batch.set(docRef, {
                            ...payload,
                            createdAt: new Date(),
                        });
                    }
                });

                await batch.commit();
                setProgress(10 + Math.round(((i + 1) / totalBatches) * 90));
            }

            setUploading(false);
            setOpen(false);
            onSuccess();
            setData([]);
            setFile(null);
        } catch (err) {
            console.error(err);
            setUploading(false);
            setError(t("firstMile.sourcesImport.pushError"));
        }
    };

    const validCount = data.filter((d) => d.isValid).length;

    return (
        <Dialog
            open={open}
            onOpenChange={(next) => {
                setOpen(next);
                if (next) {
                    queueMicrotask(() => {
                        const t = document.activeElement;
                        if (
                            t instanceof HTMLElement &&
                            t.closest("[data-radix-popper-content-wrapper]")
                        ) {
                            t.blur();
                        }
                    });
                }
            }}
        >
            <DialogTrigger asChild>
                <Button variant="outline" className="gap-2">
                    <FileSpreadsheet className="h-4 w-4" />
                    {t("firstMile.sourcesImport.button")}
                </Button>
            </DialogTrigger>
            <DialogContent className="max-w-4xl max-h-[85vh] flex flex-col">
                <DialogHeader>
                    <DialogTitle>{t("firstMile.sourcesImport.title")}</DialogTitle>
                    <DialogDescription>{t("firstMile.sourcesImport.description")}</DialogDescription>
                </DialogHeader>

                <div className="flex-1 overflow-hidden flex flex-col gap-4">
                    {!file ? (
                        <div className="flex flex-col gap-4 h-full">
                            <div className="flex justify-between items-center">
                                <span className="text-sm text-muted-foreground">
                                    {t("firstMile.sourcesImport.templateHint")}
                                </span>
                                <Button
                                    variant="outline"
                                    size="sm"
                                    onClick={handleDownloadTemplate}
                                    className="gap-2"
                                >
                                    <Download className="h-4 w-4" />
                                    {t("firstMile.sourcesImport.downloadTemplate")}
                                </Button>
                            </div>

                            <div
                                className="border-2 border-dashed rounded-lg p-12 flex flex-col items-center justify-center text-muted-foreground hover:bg-muted/50 transition-colors cursor-pointer"
                                onClick={() => fileInputRef.current?.click()}
                            >
                                <Upload className="h-12 w-12 mb-4 text-muted-foreground" />
                                <p className="font-medium text-lg">{t("firstMile.sourcesImport.clickUpload")}</p>
                                <p className="text-sm">{t("firstMile.sourcesImport.formatsSupported")}</p>
                                <input
                                    ref={fileInputRef}
                                    type="file"
                                    accept=".xlsx,.xls"
                                    className="hidden"
                                    onChange={handleFileChange}
                                />
                            </div>
                        </div>
                    ) : (
                        <div className="flex flex-col gap-4 h-full min-h-0">
                            <div className="flex items-center justify-between bg-muted/30 p-3 rounded-md border">
                                <div className="flex items-center gap-3">
                                    <div className="bg-green-100 dark:bg-green-900/30 p-2 rounded">
                                        <FileSpreadsheet className="h-5 w-5 text-green-600 dark:text-green-400" />
                                    </div>
                                    <div>
                                        <p className="font-medium text-sm">{file.name}</p>
                                        <p className="text-xs text-muted-foreground">
                                            {data.length} {t("firstMile.sourcesImport.recordsFound")} • {validCount}{" "}
                                            valid
                                        </p>
                                    </div>
                                </div>
                                <Button
                                    variant="ghost"
                                    size="icon"
                                    onClick={() => {
                                        setFile(null);
                                        setData([]);
                                        setError(null);
                                    }}
                                >
                                    <X className="h-4 w-4" />
                                </Button>
                            </div>

                            {error && (
                                <Alert variant="destructive">
                                    <AlertCircle className="h-4 w-4" />
                                    <AlertTitle>{t("firstMile.sourcesImport.error")}</AlertTitle>
                                    <AlertDescription>{error}</AlertDescription>
                                </Alert>
                            )}

                            <div className="border rounded-md flex-1 min-h-0 overflow-hidden">
                                <ScrollArea className="h-[320px] w-full">
                                    <div className="min-w-[760px] p-1">
                                        <Table>
                                            <TableHeader>
                                                <TableRow className="bg-muted/50">
                                                    <TableHead className="w-10">
                                                        {t("firstMile.sourcesImport.table.row")}
                                                    </TableHead>
                                                    <TableHead className="w-[100px]">
                                                        {t("firstMile.sourcesImport.table.docId")}
                                                    </TableHead>
                                                    <TableHead>{t("firstMile.sourcesImport.table.sourceId")}</TableHead>
                                                    <TableHead>{t("firstMile.sourcesImport.table.nameSPX")}</TableHead>
                                                    <TableHead>{t("firstMile.sourcesImport.table.nameThai")}</TableHead>
                                                    <TableHead>{t("firstMile.sourcesImport.table.latitude")}</TableHead>
                                                    <TableHead>{t("firstMile.sourcesImport.table.longitude")}</TableHead>
                                                    <TableHead>{t("firstMile.sourcesImport.table.stationType")}</TableHead>
                                                    <TableHead>{t("firstMile.sourcesImport.table.customerCode")}</TableHead>
                                                    <TableHead className="w-20">
                                                        {t("firstMile.sourcesImport.table.status")}
                                                    </TableHead>
                                                </TableRow>
                                            </TableHeader>
                                            <TableBody>
                                                {data.map((row) => (
                                                    <TableRow
                                                        key={row.id}
                                                        className={!row.isValid ? "bg-destructive/5" : ""}
                                                    >
                                                        <TableCell className="font-mono text-xs">{row.id + 1}</TableCell>
                                                        <TableCell className="font-mono text-xs truncate max-w-[100px]">
                                                            {row.firestoreDocId || "—"}
                                                        </TableCell>
                                                        <TableCell className="font-medium">
                                                            {row.source_id || "—"}
                                                        </TableCell>
                                                        <TableCell>{row.source_name_en || "—"}</TableCell>
                                                        <TableCell className="text-muted-foreground text-xs">
                                                            {row.source_name_th ?? "—"}
                                                        </TableCell>
                                                        <TableCell className="text-muted-foreground">
                                                            {row.latitude != null ? row.latitude.toFixed(5) : "—"}
                                                        </TableCell>
                                                        <TableCell className="text-muted-foreground">
                                                            {row.longitude != null ? row.longitude.toFixed(5) : "—"}
                                                        </TableCell>
                                                        <TableCell>{row.station_type}</TableCell>
                                                        <TableCell className="font-mono text-xs">
                                                            {row.customerCodeCell || "—"}
                                                        </TableCell>
                                                        <TableCell>
                                                            {row.isValid ? (
                                                                <Check className="h-4 w-4 text-green-500" />
                                                            ) : (
                                                                <span className="text-xs text-destructive font-medium">
                                                                    {row.invalidDetail ?? t("firstMile.sourcesImport.invalid")}
                                                                </span>
                                                            )}
                                                        </TableCell>
                                                    </TableRow>
                                                ))}
                                            </TableBody>
                                        </Table>
                                    </div>
                                </ScrollArea>
                            </div>
                        </div>
                    )}
                </div>

                <DialogFooter className="mt-4">
                    {uploading ? (
                        <div className="w-full space-y-2">
                            <div className="flex justify-between text-xs">
                                <span>{t("firstMile.sourcesImport.uploading")}</span>
                                <span>{progress}%</span>
                            </div>
                            <Progress value={progress} />
                        </div>
                    ) : (
                        <>
                            <Button variant="outline" onClick={() => setOpen(false)}>
                                {t("firstMile.sourcesImport.cancel")}
                            </Button>
                            <Button
                                onClick={handleUpload}
                                disabled={!file || data.length === 0 || validCount === 0 || uploading}
                            >
                                {t("firstMile.sourcesImport.upload")} {validCount}{" "}
                                {t("firstMile.sourcesImport.records")}
                            </Button>
                        </>
                    )}
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

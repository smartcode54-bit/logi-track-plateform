"use client";

import { useState, useRef, useEffect, useMemo } from "react";
import * as XLSX from "xlsx";
import { Upload, FileSpreadsheet, AlertCircle } from "lucide-react";
import { useLanguage } from "@/context/language";
import {
    batchCreateTollExpenseImports,
    getDriversWithTruckAssignments,
    getTrucksForFilter,
    type DriverWithTruckAssignment,
    type TollImportRowInput,
    type TruckOption,
} from "../api/billing";

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
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { Label } from "@/components/ui/label";
import { Progress } from "@/components/ui/progress";

function cellToText(v: unknown): string {
    if (v == null) return "";
    if (typeof v === "string" || typeof v === "number" || typeof v === "boolean") return String(v);
    if (v instanceof Date) return v.toISOString();
    if (typeof v === "object") {
        const obj = v as Record<string, unknown>;
        if (typeof obj.w === "string") return obj.w;
        if (typeof obj.text === "string") return obj.text;
        if (typeof obj.v === "string" || typeof obj.v === "number" || typeof obj.v === "boolean") {
            return String(obj.v);
        }
    }
    return String(v);
}

function normalizeHeaderCell(h: unknown): string {
    return cellToText(h)
        .replace(/^\uFEFF/, "")
        .replace(/\u00A0/g, " ")
        .replace(/[\u2000-\u200B\u202F\u205F\u3000]/g, " ")
        .replace(/\uFF08/g, "(")
        .replace(/\uFF09/g, ")")
        .toLowerCase()
        .replace(/\s+/g, " ")
        .trim();
}

function headerMatches(headerNorm: string, keywordRaw: string): boolean {
    const k = normalizeHeaderCell(keywordRaw);
    if (!k) return false;
    if (headerNorm.includes(k)) return true;
    if (k.length < 2) return false;
    const hc = headerNorm.replace(/\s/g, "");
    const kc = k.replace(/\s/g, "");
    return kc.length >= 2 && hc.includes(kc);
}

function parseNum(val: unknown): number | undefined {
    if (val == null || val === "") return undefined;
    if (typeof val === "number" && Number.isFinite(val)) return val;
    const s = cellToText(val).replace(/,/g, "").trim();
    if (!s) return undefined;
    const n = Number(s);
    return Number.isFinite(n) ? n : undefined;
}

/** Excel UTC date from serial day number (sheet cell). */
function excelSerialToDate(serial: number): Date {
    const ms = (serial - 25569) * 86400 * 1000;
    return new Date(ms);
}

function parseTollDate(val: unknown): Date | undefined {
    if (val == null || val === "") return undefined;
    if (val instanceof Date && !Number.isNaN(val.getTime())) return val;
    if (typeof val === "number" && Number.isFinite(val)) {
        if (val > 20000 && val < 100000) return excelSerialToDate(val);
        if (val > 1e12) return new Date(val);
    }
    const s = cellToText(val).trim();
    if (!s) return undefined;
    // Parse D/M/Y with optional time first to avoid locale-dependent Date.parse behavior.
    const m = s.match(
        /^(\d{1,2})[/.-](\d{1,2})[/.-](\d{2,4})(?:\s+(\d{1,2})(?::(\d{1,2}))?(?::(\d{1,2}))?)?/
    );
    if (m) {
        const a = Number(m[1]);
        const b = Number(m[2]);
        const y = Number(m[3].length === 2 ? `20${m[3]}` : m[3]);
        const hh = Number(m[4] ?? 0);
        const mm = Number(m[5] ?? 0);
        const ss = Number(m[6] ?? 0);
        if (a > 12) return new Date(y, b - 1, a, hh, mm, ss);
        if (b > 12) return new Date(y, a - 1, b, hh, mm, ss);
        return new Date(y, b - 1, a, hh, mm, ss);
    }
    const iso = Date.parse(s);
    if (!Number.isNaN(iso)) return new Date(iso);
    return undefined;
}

/** Keep rows whose type column indicates toll / pass-through (ค่าผ่านทาง). */
function isTollPassType(typeCell: unknown): boolean {
    const raw = cellToText(typeCell)
        .replace(/\u00A0/g, " ")
        .replace(/[\u2000-\u200B\u202F\u205F\u3000]/g, " ")
        .trim();
    if (!raw) return false;
    const lower = raw.toLowerCase();
    const checks: string[] = [
        "ผ่านทาง",
        "ทางด่วน",
        "expressway",
        "easy pass",
        "mflow",
        "easy-pass",
    ];
    for (const c of checks) {
        if (c === "ผ่านทาง" || c === "ทางด่วน") {
            if (raw.includes(c)) return true;
        } else if (lower.includes(c)) return true;
    }
    if (lower === "toll" || lower.endsWith(" toll") || lower.startsWith("toll ")) return true;
    return false;
}

export interface TollExpenseImportDialogProps {
    onSuccess: () => void;
    canImport: boolean;
}

export function TollExpenseImportDialog({ onSuccess, canImport }: TollExpenseImportDialogProps) {
    const { t } = useLanguage();
    const [open, setOpen] = useState(false);
    const [file, setFile] = useState<File | null>(null);
    const [error, setError] = useState<string | null>(null);
    const [parsedTollRows, setParsedTollRows] = useState<TollImportRowInput[]>([]);
    const [skippedNonToll, setSkippedNonToll] = useState(0);
    const [emptyRows, setEmptyRows] = useState(0);
    const [trucks, setTrucks] = useState<TruckOption[]>([]);
    const [drivers, setDrivers] = useState<DriverWithTruckAssignment[]>([]);
    const [truckId, setTruckId] = useState<string>("");
    const [driverId, setDriverId] = useState<string>("");
    const [uploading, setUploading] = useState(false);
    const [progress, setProgress] = useState(0);
    const fileInputRef = useRef<HTMLInputElement>(null);

    useEffect(() => {
        if (!open) return;
        Promise.all([getTrucksForFilter(), getDriversWithTruckAssignments()]).then(([tlist, dlist]) => {
            setTrucks(tlist.sort((a, b) => a.licensePlate.localeCompare(b.licensePlate)));
            setDrivers(dlist);
        });
    }, [open]);

    const driversOnTruck = useMemo(() => {
        if (!truckId) return [];
        return drivers.filter((d) => d.truckId === truckId);
    }, [drivers, truckId]);

    useEffect(() => {
        if (!truckId) {
            setDriverId("");
            return;
        }
        const onTruck = drivers.filter((d) => d.truckId === truckId);
        if (onTruck.length === 1) {
            setDriverId(onTruck[0].filterId);
            return;
        }
        if (onTruck.length === 0) {
            setDriverId("");
            return;
        }
        setDriverId((prev) => (onTruck.some((d) => d.filterId === prev) ? prev : ""));
    }, [truckId, drivers]);

    const parseSpreadsheet = async (f: File) => {
        setError(null);
        try {
            const buffer = await f.arrayBuffer();
            const workbook = XLSX.read(buffer, { type: "array", cellDates: true });
            const sheetName = workbook.SheetNames[0];
            const sheet = workbook.Sheets[sheetName];
            const jsonData = XLSX.utils.sheet_to_json(sheet, { header: 1 }) as unknown[][];
            if (!jsonData.length || !Array.isArray(jsonData[0])) {
                setParsedTollRows([]);
                setSkippedNonToll(0);
                setEmptyRows(0);
                setError(t("accounting.tollImport.emptyOrInvalidSheet"));
                return;
            }

            const rowAsNormalizedHeader = (idx: number): string[] =>
                ((jsonData[idx] ?? []) as unknown[]).map((h) => normalizeHeaderCell(h));
            const getColFromHeaders = (headers: string[], keywords: string[]) => {
                const idx = headers.findIndex((h) => keywords.some((kw) => headerMatches(h, kw)));
                return idx !== -1 ? idx : -1;
            };

            const seqKeywords = ["ลำดับ", "seq", "no.", "no", "#", "order", "index"];
            const locKeywords = ["สถานที่", "location", "place", "station", "จุด", "ทางเข้า", "ด่าน"];
            const laneKeywords = ["เครื่อง", "เลน", "lane", "machine", "channel", "ช่อง"];
            const dateKeywords = [
                "วันที่",
                "date",
                "วัน",
                "วันเวลา",
                "datetime",
                "date time",
                "transaction date",
                "วันที่ทำรายการ",
                "เวลาทำรายการ",
            ];
            const typeKeywords = [
                "ประเภท",
                "type",
                "category",
                "ชนิด",
                "transaction type",
                "ประเภทรายการ",
            ];
            const amountKeywords = [
                "จำนวนเงิน",
                "amount",
                "บาท",
                "baht",
                "total",
                "ค่าธรรมเนียม",
                "ค่าผ่านทาง",
                "ยอดเงิน",
                "ค่าบริการ",
            ];

            // Some exports include title/blank rows before header.
            let headerRowIndex = 0;
            let colDate = -1;
            let colType = -1;
            let colAmt = -1;
            let colSeq = -1;
            let colLoc = -1;
            let colLane = -1;
            const probeRows = Math.min(20, jsonData.length);
            for (let i = 0; i < probeRows; i++) {
                const hdr = rowAsNormalizedHeader(i);
                const d = getColFromHeaders(hdr, dateKeywords);
                const ty = getColFromHeaders(hdr, typeKeywords);
                const am = getColFromHeaders(hdr, amountKeywords);
                if (d !== -1 && ty !== -1 && am !== -1) {
                    // Guard against broad keyword collisions, e.g. "วันที่เกิดรายการ" matching type.
                    if (d === ty || ty === am || d === am) continue;
                    headerRowIndex = i;
                    colDate = d;
                    colType = ty;
                    colAmt = am;
                    colSeq = getColFromHeaders(hdr, seqKeywords);
                    colLoc = getColFromHeaders(hdr, locKeywords);
                    colLane = getColFromHeaders(hdr, laneKeywords);
                    break;
                }
            }

            // Explicit fallback for common card history layout:
            // ลำดับ | สถานที่ | เครื่อง/เลน | วันที่เกิดรายการ | ประเภท | จำนวนเงิน
            if (colDate === -1 || colType === -1 || colAmt === -1) {
                for (let i = 0; i < probeRows; i++) {
                    const hdr = rowAsNormalizedHeader(i);
                    const looksLikeCardHistory =
                        hdr.length >= 6
                        && headerMatches(hdr[0] ?? "", "ลำดับ")
                        && (headerMatches(hdr[1] ?? "", "สถานที่") || headerMatches(hdr[1] ?? "", "location"))
                        && (headerMatches(hdr[3] ?? "", "วันที่เกิดรายการ") || headerMatches(hdr[3] ?? "", "date"))
                        && (headerMatches(hdr[4] ?? "", "ประเภท") || headerMatches(hdr[4] ?? "", "type"))
                        && (headerMatches(hdr[5] ?? "", "จำนวนเงิน") || headerMatches(hdr[5] ?? "", "amount"));
                    if (looksLikeCardHistory) {
                        headerRowIndex = i;
                        colSeq = colSeq === -1 ? 0 : colSeq;
                        colLoc = colLoc === -1 ? 1 : colLoc;
                        colLane = colLane === -1 ? 2 : colLane;
                        colDate = 3;
                        colType = 4;
                        colAmt = 5;
                        break;
                    }
                }
            }

            // Fallback: infer likely columns from row patterns if header names are unusual.
            if (colDate === -1 || colAmt === -1 || colType === -1) {
                const sampleRows = jsonData.slice(Math.max(headerRowIndex, 0), Math.min(jsonData.length, 120)) as unknown[][];
                const maxCols = sampleRows.reduce((m, r) => Math.max(m, Array.isArray(r) ? r.length : 0), 0);
                let bestDateCol = -1;
                let bestAmtCol = -1;
                let bestTypeCol = -1;
                let bestDateScore = 0;
                let bestAmtScore = 0;
                let bestTypeScore = 0;
                for (let c = 0; c < maxCols; c++) {
                    let dateScore = 0;
                    let amtScore = 0;
                    let typeScore = 0;
                    for (const row of sampleRows) {
                        const cell = row?.[c];
                        if (parseTollDate(cell)) dateScore++;
                        const n = parseNum(cell);
                        if (n != null && Number.isFinite(n) && n >= 0) amtScore++;
                        if (isTollPassType(cell)) typeScore++;
                    }
                    if (dateScore > bestDateScore) {
                        bestDateScore = dateScore;
                        bestDateCol = c;
                    }
                    if (amtScore > bestAmtScore) {
                        bestAmtScore = amtScore;
                        bestAmtCol = c;
                    }
                    if (typeScore > bestTypeScore) {
                        bestTypeScore = typeScore;
                        bestTypeCol = c;
                    }
                }
                if (colDate === -1 && bestDateScore >= 2) colDate = bestDateCol;
                if (colAmt === -1 && bestAmtScore >= 2) colAmt = bestAmtCol;
                if (colType === -1 && bestTypeScore >= 1) colType = bestTypeCol;

                // Last-resort fallback for common export layout with 6 columns:
                // [ลำดับ, สถานที่, เครื่อง/เลน, วันที่, ประเภท, จำนวนเงิน]
                if ((colDate === -1 || colType === -1 || colAmt === -1) && maxCols >= 6) {
                    if (colSeq === -1) colSeq = 0;
                    if (colLoc === -1) colLoc = 1;
                    if (colLane === -1) colLane = 2;
                    if (colDate === -1) colDate = 3;
                    if (colType === -1) colType = 4;
                    if (colAmt === -1) colAmt = 5;
                }
            }

            const rows = jsonData.slice(headerRowIndex + 1) as unknown[][];

            if (colType === -1 || colAmt === -1 || colDate === -1) {
                setParsedTollRows([]);
                setSkippedNonToll(0);
                setEmptyRows(0);
                setError(t("accounting.tollImport.missingColumns"));
                return;
            }

            let skipped = 0;
            let empty = 0;
            const tollRows: TollImportRowInput[] = [];

            for (let ri = 0; ri < rows.length; ri++) {
                const row = rows[ri];
                if (!row || row.length === 0) {
                    empty++;
                    continue;
                }
                const get = (col: number) => (col >= 0 ? row[col] : undefined);
                const typeCell = get(colType);
                const amtRaw = get(colAmt);
                const dateRaw = get(colDate);

                if (
                    (typeCell == null || cellToText(typeCell).trim() === "") &&
                    (amtRaw == null || cellToText(amtRaw).trim() === "") &&
                    (dateRaw == null || cellToText(dateRaw).trim() === "")
                ) {
                    empty++;
                    continue;
                }

                if (!isTollPassType(typeCell)) {
                    skipped++;
                    continue;
                }

                const amount = parseNum(amtRaw);
                const date = parseTollDate(dateRaw);
                if (amount == null || date == null) {
                    skipped++;
                    continue;
                }

                const seq = colSeq >= 0 ? parseNum(get(colSeq)) : undefined;
                const loc = colLoc >= 0 ? cellToText(get(colLoc)).trim() : "";
                const lane = colLane >= 0 ? cellToText(get(colLane)).trim() : "";
                const typeStr = cellToText(typeCell).trim();

                tollRows.push({
                    tollImportSequence: seq != null ? Math.floor(seq) : ri + 1,
                    tollLocation: loc || undefined,
                    tollLane: lane || undefined,
                    tollSourceType: typeStr || undefined,
                    date,
                    amount,
                });
            }

            setParsedTollRows(tollRows);
            setSkippedNonToll(skipped);
            setEmptyRows(empty);
        } catch (e) {
            console.error("[TollImport] parse", e);
            setParsedTollRows([]);
            setError(t("accounting.tollImport.parseError"));
        }
    };

    const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
        const selected = e.target.files?.[0];
        if (selected) {
            setFile(selected);
            void parseSpreadsheet(selected);
        }
    };

    const handleImport = async () => {
        if (!truckId || !driverId || parsedTollRows.length === 0) return;
        setUploading(true);
        setProgress(0);
        try {
            await batchCreateTollExpenseImports(parsedTollRows, driverId, truckId, "PENDING");
            setProgress(100);
            setOpen(false);
            setFile(null);
            setParsedTollRows([]);
            setSkippedNonToll(0);
            setEmptyRows(0);
            setTruckId("");
            setDriverId("");
            if (fileInputRef.current) fileInputRef.current.value = "";
            onSuccess();
        } catch (err) {
            console.error("[TollImport] batch", err);
            setError(t("accounting.tollImport.pushError"));
        } finally {
            setUploading(false);
        }
    };

    const ready =
        canImport &&
        parsedTollRows.length > 0 &&
        truckId &&
        driverId &&
        !uploading;

    return (
        <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger asChild>
                <Button variant="outline" size="sm" type="button">
                    <Upload className="h-4 w-4 mr-2" />
                    {t("accounting.tollImport.button")}
                </Button>
            </DialogTrigger>
            <DialogContent className="max-w-2xl max-h-[90vh] flex flex-col">
                <DialogHeader>
                    <DialogTitle>{t("accounting.tollImport.title")}</DialogTitle>
                    <DialogDescription>{t("accounting.tollImport.description")}</DialogDescription>
                </DialogHeader>

                <div className="space-y-4 flex-1 min-h-0 overflow-hidden flex flex-col">
                    <div>
                        <input
                            ref={fileInputRef}
                            type="file"
                            accept=".xlsx,.xls,.csv"
                            className="hidden"
                            onChange={handleFileChange}
                        />
                        <Button type="button" variant="secondary" onClick={() => fileInputRef.current?.click()}>
                            <FileSpreadsheet className="h-4 w-4 mr-2" />
                            {t("accounting.tollImport.clickUpload")}
                        </Button>
                        <p className="text-xs text-muted-foreground mt-2">{t("accounting.tollImport.formatsSupported")}</p>
                        {file && (
                            <p className="text-sm mt-1">
                                {file.name} — {parsedTollRows.length} {t("accounting.tollImport.tollRows")}
                            </p>
                        )}
                    </div>

                    {error && (
                        <Alert variant="destructive">
                            <AlertCircle className="h-4 w-4" />
                            <AlertTitle>{t("accounting.tollImport.error")}</AlertTitle>
                            <AlertDescription>{error}</AlertDescription>
                        </Alert>
                    )}

                    <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                        <div className="space-y-2">
                            <Label>{t("accounting.tollImport.selectTruck")}</Label>
                            <Select value={truckId} onValueChange={setTruckId}>
                                <SelectTrigger>
                                    <SelectValue placeholder={t("accounting.tollImport.selectTruckPlaceholder")} />
                                </SelectTrigger>
                                <SelectContent className="z-1005" position="popper">
                                    {trucks.map((tr) => (
                                        <SelectItem key={tr.id} value={tr.id}>
                                            {tr.licensePlate}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                        <div className="space-y-2">
                            <Label>{t("accounting.tollImport.selectDriver")}</Label>
                            <Select value={driverId} onValueChange={setDriverId} disabled={!truckId}>
                                <SelectTrigger>
                                    <SelectValue placeholder={t("accounting.tollImport.selectDriverPlaceholder")} />
                                </SelectTrigger>
                                <SelectContent className="z-1005" position="popper">
                                    {(driversOnTruck.length > 0 ? driversOnTruck : drivers).map((dr) => (
                                        <SelectItem key={dr.filterId} value={dr.filterId}>
                                            {dr.name}
                                            {driversOnTruck.length === 0 && dr.truckId
                                                ? ` · ${trucks.find((x) => x.id === dr.truckId)?.licensePlate ?? ""}`
                                                : ""}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                            {truckId && driversOnTruck.length === 0 && (
                                <p className="text-xs text-amber-700 dark:text-amber-400">
                                    {t("accounting.tollImport.noDriverOnTruck")}
                                </p>
                            )}
                            {truckId && driversOnTruck.length > 1 && (
                                <p className="text-xs text-muted-foreground">{t("accounting.tollImport.pickDriverHint")}</p>
                            )}
                        </div>
                    </div>

                    <p className="text-sm text-muted-foreground">
                        {t("accounting.tollImport.stats", {
                            toll: String(parsedTollRows.length),
                            skipped: String(skippedNonToll),
                            empty: String(emptyRows),
                        })}
                    </p>

                    {parsedTollRows.length > 0 && (
                        <ScrollArea className="h-[220px] border rounded-md">
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead className="w-12">#</TableHead>
                                        <TableHead>{t("accounting.tollImport.preview.date")}</TableHead>
                                        <TableHead>{t("accounting.tollImport.preview.amount")}</TableHead>
                                        <TableHead>{t("accounting.tollImport.preview.location")}</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {parsedTollRows.slice(0, 50).map((r, i) => (
                                        <TableRow key={i}>
                                            <TableCell>{r.tollImportSequence ?? i + 1}</TableCell>
                                            <TableCell className="whitespace-nowrap text-xs">
                                                {r.date.toLocaleDateString()}
                                            </TableCell>
                                            <TableCell>฿{r.amount.toLocaleString()}</TableCell>
                                            <TableCell className="max-w-[200px] truncate text-muted-foreground text-xs">
                                                {[r.tollLocation, r.tollLane].filter(Boolean).join(" · ") || "—"}
                                            </TableCell>
                                        </TableRow>
                                    ))}
                                </TableBody>
                            </Table>
                            {parsedTollRows.length > 50 && (
                                <p className="p-2 text-xs text-center text-muted-foreground">
                                    {t("accounting.tollImport.previewTruncated", { n: String(parsedTollRows.length) })}
                                </p>
                            )}
                        </ScrollArea>
                    )}

                    {uploading && (
                        <div className="space-y-2">
                            <Progress value={progress} />
                            <p className="text-xs text-muted-foreground">{t("accounting.tollImport.uploading")}</p>
                        </div>
                    )}
                </div>

                <DialogFooter>
                    <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                        {t("accounting.tollImport.cancel")}
                    </Button>
                    <Button type="button" onClick={handleImport} disabled={!ready}>
                        {t("accounting.tollImport.upload")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

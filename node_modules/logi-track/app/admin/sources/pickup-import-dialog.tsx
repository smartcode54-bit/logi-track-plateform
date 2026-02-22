"use client";

import { useState, useRef } from "react";
import * as XLSX from "xlsx";
import { Upload, X, FileSpreadsheet, Check, AlertCircle, Download } from "lucide-react";
import { collection, writeBatch, doc } from "firebase/firestore";
import { db } from "@/firebase/client";
import { useLanguage } from "@/context/language";
import type { StationType } from "@/validate/hubSchema";

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

function parseNum(val: unknown): number | undefined {
    if (val == null || val === "") return undefined;
    const n = Number(val);
    return Number.isFinite(n) ? n : undefined;
}

interface ParsedRow {
    id: number;
    source_id: string;
    source_name_en: string;
    latitude: number | undefined;
    longitude: number | undefined;
    station_type: StationType;
    isValid: boolean;
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

            const headers = (jsonData[0] as unknown[]).map((h) =>
                String(h ?? "")
                    .toLowerCase()
                    .trim()
            );
            const rows = jsonData.slice(1) as unknown[][];

            const getCol = (keywords: string[]) => {
                const idx = headers.findIndex((h) =>
                    keywords.some((k) => (h as string).includes(k))
                );
                return idx !== -1 ? idx : -1;
            };

            const colSourceId = getCol(["source", "id", "รหัส", "source_id", "source id"]);
            const colName = getCol(["name", "spx", "ชื่อ", "source_name", "name (spx)"]);
            const colLat = getCol(["lat", "latitude", "ละติจูด"]);
            const colLng = getCol(["lng", "long", "longitude", "ลองจิจูด"]);
            const colType = getCol(["station", "type", "ประเภท", "station_type"]);

            const parsed: ParsedRow[] = rows
                .map((row, index) => {
                    if (!row || row.length === 0) return null;
                    const get = (col: number) => (col >= 0 ? row[col] : undefined);
                    const source_id = String(get(colSourceId) ?? "").trim();
                    const source_name_en = String(get(colName) ?? "").trim();
                    const latitude = parseNum(get(colLat));
                    const longitude = parseNum(get(colLng));
                    const station_type = normalizeStationType(get(colType));
                    const isValid =
                        source_id.length > 0 && source_name_en.length > 0;
                    return {
                        id: index,
                        source_id,
                        source_name_en,
                        latitude,
                        longitude,
                        station_type,
                        isValid,
                    };
                })
                .filter((r): r is ParsedRow => r != null);

            setData(parsed);
            setError(null);
        } catch (err) {
            console.error(err);
            setError(t("firstMile.sourcesImport.parseError"));
        }
    };

    const handleDownloadTemplate = () => {
        const headers = [
            "Source ID (รหัสจุดรับงาน)",
            "Name SPX (ชื่อ SPX)",
            "Latitude (ละติจูด)",
            "Longitude (ลองจิจูด)",
            "Station Type (HUB/SOC)",
        ];
        const exampleRow = ["ALANG-A", "Wang Thong Lang A", "13.7563", "100.5018", "HUB"];
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
                const chunk = validRows.slice(
                    i * batchSize,
                    (i + 1) * batchSize
                );

                chunk.forEach((row) => {
                    const docRef = doc(collection(db, "hubs"));
                    batch.set(docRef, {
                        source_id: row.source_id,
                        source_name_en: row.source_name_en,
                        latitude: row.latitude ?? null,
                        longitude: row.longitude ?? null,
                        station_type: row.station_type,
                        createdAt: new Date(),
                        updatedAt: new Date(),
                    });
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
        <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger asChild>
                <Button variant="outline" className="gap-2">
                    <FileSpreadsheet className="h-4 w-4" />
                    {t("firstMile.sourcesImport.button")}
                </Button>
            </DialogTrigger>
            <DialogContent className="max-w-4xl max-h-[85vh] flex flex-col">
                <DialogHeader>
                    <DialogTitle>{t("firstMile.sourcesImport.title")}</DialogTitle>
                    <DialogDescription>
                        {t("firstMile.sourcesImport.description")}
                    </DialogDescription>
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
                                <p className="font-medium text-lg">
                                    {t("firstMile.sourcesImport.clickUpload")}
                                </p>
                                <p className="text-sm">
                                    {t("firstMile.sourcesImport.formatsSupported")}
                                </p>
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
                                            {data.length}{" "}
                                            {t("firstMile.sourcesImport.recordsFound")} •{" "}
                                            {validCount} valid
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
                                    <AlertTitle>
                                        {t("firstMile.sourcesImport.error")}
                                    </AlertTitle>
                                    <AlertDescription>{error}</AlertDescription>
                                </Alert>
                            )}

                            <div className="border rounded-md flex-1 min-h-0 overflow-hidden">
                                <ScrollArea className="h-[320px]">
                                    <Table>
                                        <TableHeader>
                                            <TableRow className="bg-muted/50">
                                                <TableHead className="w-12">
                                                    {t("firstMile.sourcesImport.table.row")}
                                                </TableHead>
                                                <TableHead>
                                                    {t("firstMile.sourcesImport.table.sourceId")}
                                                </TableHead>
                                                <TableHead>
                                                    {t("firstMile.sourcesImport.table.nameSPX")}
                                                </TableHead>
                                                <TableHead>
                                                    {t("firstMile.sourcesImport.table.latitude")}
                                                </TableHead>
                                                <TableHead>
                                                    {t("firstMile.sourcesImport.table.longitude")}
                                                </TableHead>
                                                <TableHead>
                                                    {t("firstMile.sourcesImport.table.stationType")}
                                                </TableHead>
                                                <TableHead className="w-20">
                                                    {t("firstMile.sourcesImport.table.status")}
                                                </TableHead>
                                            </TableRow>
                                        </TableHeader>
                                        <TableBody>
                                            {data.map((row) => (
                                                <TableRow
                                                    key={row.id}
                                                    className={
                                                        !row.isValid
                                                            ? "bg-destructive/5"
                                                            : ""
                                                    }
                                                >
                                                    <TableCell className="font-mono text-xs">
                                                        {row.id + 1}
                                                    </TableCell>
                                                    <TableCell className="font-medium">
                                                        {row.source_id || "—"}
                                                    </TableCell>
                                                    <TableCell>
                                                        {row.source_name_en || "—"}
                                                    </TableCell>
                                                    <TableCell className="text-muted-foreground">
                                                        {row.latitude != null
                                                            ? row.latitude.toFixed(5)
                                                            : "—"}
                                                    </TableCell>
                                                    <TableCell className="text-muted-foreground">
                                                        {row.longitude != null
                                                            ? row.longitude.toFixed(5)
                                                            : "—"}
                                                    </TableCell>
                                                    <TableCell>
                                                        {row.station_type}
                                                    </TableCell>
                                                    <TableCell>
                                                        {row.isValid ? (
                                                            <Check className="h-4 w-4 text-green-500" />
                                                        ) : (
                                                            <span className="text-xs text-destructive font-medium">
                                                                {t("firstMile.sourcesImport.invalid")}
                                                            </span>
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
                                <span>{t("firstMile.sourcesImport.uploading")}</span>
                                <span>{progress}%</span>
                            </div>
                            <Progress value={progress} />
                        </div>
                    ) : (
                        <>
                            <Button
                                variant="outline"
                                onClick={() => setOpen(false)}
                            >
                                {t("firstMile.sourcesImport.cancel")}
                            </Button>
                            <Button
                                onClick={handleUpload}
                                disabled={
                                    !file ||
                                    data.length === 0 ||
                                    validCount === 0 ||
                                    uploading
                                }
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

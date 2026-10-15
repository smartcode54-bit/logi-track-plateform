"use client";

import { useState, useRef } from "react";
import { Button } from "@/components/ui/button";
import {
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import {
    Tooltip,
    TooltipContent,
    TooltipProvider,
    TooltipTrigger,
} from "@/components/ui/tooltip";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { AlertCircle, CheckCircle, Download } from "lucide-react";
import { useLanguage } from "@/context/language";
import type { LazyDialogControl } from "@/components/lazy-dialog";
import * as XLSX from "xlsx";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Badge } from "@/components/ui/badge";
import { toast } from "sonner";
import { useTruckWrites } from "../api/useTrucks";
import { apiErrorText } from "@/lib/apiError";

interface ImportedTruck {
    licensePlate: string;
    province: string;
    type: string;
    brand: string;
    model: string;
    truckStatus: string;
    ownershipType: string;
    color: string;
    year: string;
    vin: string;
    engineNumber: string;
    fuelType: string;
    registrationDate: string; // ISO String
    maxLoadWeight: number;
    isValid: boolean;
    errors: string[];
}

/**
 * The dialog body (`DialogContent`). Pages render it inside `LazyDialog`, through `next/dynamic`, so
 * this module and xlsx load on the first open, not with the page (developer-spec.md §10.11).
 */
export function TruckImportDialog({ setOpen }: LazyDialogControl) {
    const { t } = useLanguage();
    const [file, setFile] = useState<File | null>(null);
    const [importedData, setImportedData] = useState<ImportedTruck[]>([]);
    /** The sheet rows as parsed: Go validates and maps them itself (`POST /v1/trucks/import`, T25). */
    const [rawRows, setRawRows] = useState<Record<string, unknown>[]>([]);
    const { importRows } = useTruckWrites();
    const isImporting = importRows.isPending;
    const fileInputRef = useRef<HTMLInputElement>(null);

    const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
        const selectedFile = e.target.files?.[0];
        if (selectedFile) {
            setFile(selectedFile);
            parseFile(selectedFile);
        }
    };

    const parseFile = async (file: File) => {
        const reader = new FileReader();
        reader.onload = (e) => {
            try {
                const data = e.target?.result;
                const workbook = XLSX.read(data, { type: "binary" });
                const sheetName = workbook.SheetNames[0];
                const sheet = workbook.Sheets[sheetName];
                const jsonData = XLSX.utils.sheet_to_json(sheet) as Record<string, unknown>[];

                setRawRows(jsonData);
                validateData(jsonData);
            } catch (error) {
                console.error("Error parsing file:", error);
                toast.error(t('trucks.import.parseError'));
            }
        };
        reader.readAsBinaryString(file);
    };

    const validateData = (data: any[]) => {
        const validated: ImportedTruck[] = data.map((row) => {
            const errors: string[] = [];
            for (const column of ['License Plate', 'Province', 'Type', 'Brand', 'Model']) {
                if (!row[column]) errors.push(column);
            }

            const normalizeType = (val: any): string => {
                const type = val?.toString().toUpperCase() || "";
                const mapping: Record<string, string> = {
                    "4W": "4-wheel",
                    "4WJ": "4-wheel-jumbo",
                    "6W": "6-wheel",
                    "10W": "10-wheel",
                    "12W": "12-wheel",
                    "TRAILER": "trailer",
                    "HEAD": "head"
                };
                return mapping[type] || type.toLowerCase();
            };

            const parseDate = (val: any): string => {
                if (!val) return "";
                if (typeof val === 'number') {
                    const date = new Date(Math.round((val - 25569) * 86400 * 1000));
                    return date.toISOString();
                }
                try {
                    const date = new Date(val);
                    if (!isNaN(date.getTime())) return date.toISOString();
                } catch (e) { }
                return "";
            };

            const ownership = row['Ownership']?.toString().toLowerCase();
            const validOwnership = ownership === 'company' || ownership === 'subcontractor' ? (ownership === 'company' ? 'own' : 'subcontractor') : 'own';

            const status = row['Status']?.toString().toLowerCase();
            const validStatus = ['active', 'maintenance', 'inactive', 'in-transit'].includes(status) ? status : 'active';

            const truck: ImportedTruck = {
                licensePlate: row['License Plate']?.toString() || "",
                province: row['Province']?.toString() || "",
                type: normalizeType(row['Type']),
                brand: row['Brand']?.toString() || "",
                model: row['Model']?.toString() || "",
                truckStatus: validStatus,
                ownershipType: validOwnership,
                color: row['Color']?.toString() || "",
                year: row['Year']?.toString() || "",
                vin: row['VIN']?.toString() || "",
                engineNumber: row['Engine Number']?.toString() || "",
                fuelType: row['Fuel Type']?.toString() || "",
                registrationDate: parseDate(row['Registration Date']),
                maxLoadWeight: Number(row['Max Load']) || 0,
                isValid: errors.length === 0,
                errors: errors
            };

            return truck;
        });
        setImportedData(validated);
    };

    const handleImport = async () => {
        const validTrucks = importedData.filter(truck => truck.isValid);
        if (validTrucks.length === 0) return;

        try {
            // Go: every sheet row to `POST /v1/trucks/import` (validated, upserted by plate, per-row
            // reasons); Firestore: the rows validated here, one new document each (T25).
            const result = await importRows.mutateAsync({
                rawRows,
                validRows: validTrucks.map(({ isValid: _v, errors: _e, ...truck }) => {
                    void _v;
                    void _e;
                    return truck as unknown as Record<string, unknown>;
                }),
            });
            const imported = result.created + result.updated;
            toast.success(t('trucks.import.success').replace('{count}', imported.toString()));
            if (result.errors.length > 0) {
                toast.error(t('masterData.trucks.importRejected', {
                    count: result.errors.length,
                    rows: result.errors.map((e) => e.licensePlate || String(e.row)).join(", "),
                }));
            }
            setOpen(false);
            setFile(null);
            setImportedData([]);
            setRawRows([]);
        } catch (error) {
            toast.error(`${t('trucks.import.error')}: ${apiErrorText(error, (k, f) => t(k, f))}`);
        }
    };

    const downloadTemplate = () => {
        const headers = [
            "License Plate", "Province", "Type", "Brand", "Model", "Color", "Year",
            "VIN", "Engine Number", "Fuel Type", "Ownership", "Status",
            "Registration Date", "Max Load"
        ];

        const sampleRow = [
            "70-1234", "กรุงเทพมหานคร", "10W", "Isuzu", "FXZ360", "White", "2023", "VIN123456789", "ENG987654321", "Diesel", "Company", "Active", "2023-01-15", "25000",
            "ตัวอย่างข้อมูล ลบก่อนนำเข้อมูลเข้าสู่ระบบ"
        ];

        const ws = XLSX.utils.aoa_to_sheet([headers, sampleRow]);
        const wb = XLSX.utils.book_new();
        XLSX.utils.book_append_sheet(wb, ws, "Template");
        XLSX.writeFile(wb, "truck_import_template.xlsx");
    };

    return (
        <DialogContent className="sm:max-w-[900px] max-h-[90vh] flex flex-col">
            <DialogHeader>
                <DialogTitle>{t('trucks.import.title')}</DialogTitle>
                <DialogDescription>
                    {t('trucks.import.desc')}
                </DialogDescription>
            </DialogHeader>

            <div className="space-y-4 py-4 flex-1 overflow-hidden flex flex-col">
                <div className="flex items-center gap-4 shrink-0">
                    <div className="grid w-full max-w-sm items-center gap-1.5">
                        <Label htmlFor="file">{t('trucks.import.selectFile')}</Label>
                        <Input
                            id="file"
                            type="file"
                            accept=".xlsx, .xls, .csv"
                            onChange={handleFileChange}
                            ref={fileInputRef}
                        />
                    </div>
                    <Button variant="secondary" onClick={downloadTemplate} className="mt-6">
                        <Download className="mr-2 h-4 w-4" />
                        {t('trucks.import.downloadTemplate')}
                    </Button>
                </div>

                {importedData.length > 0 && (
                    <div className="border rounded-md flex-1 overflow-hidden flex flex-col">
                        <div className="p-2 bg-muted/50 border-b shrink-0">
                            <h4 className="text-sm font-semibold">{t('trucks.import.preview')} ({importedData.length})</h4>
                        </div>
                        <ScrollArea className="flex-1">
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead className="w-[40px]"></TableHead>
                                        <TableHead>{t('trucks.table.plate')}</TableHead>
                                        <TableHead>{t('trucks.table.model')}</TableHead>
                                        <TableHead className="hidden md:table-cell">{t('masterData.trucks.importDetails')}</TableHead>
                                        <TableHead>{t('trucks.section.truckType')}</TableHead>
                                        <TableHead>{t('trucks.table.status')}</TableHead>
                                        <TableHead>{t('trucks.table.ownership')}</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {importedData.map((truck, index) => (
                                        <TableRow key={index} className={!truck.isValid ? "bg-muted" : ""}>
                                            <TableCell>
                                                {truck.isValid ? (
                                                    <CheckCircle className="h-4 w-4 text-success" />
                                                ) : (
                                                    <TooltipProvider>
                                                        <Tooltip>
                                                             <TooltipTrigger asChild>
                                                                <AlertCircle className="h-4 w-4 text-destructive" />
                                                            </TooltipTrigger>
                                                            <TooltipContent>
                                                                <p>{truck.errors.map((column) => t("trucks.ui4.missingImportField", { column })).join(", ")}</p>
                                                            </TooltipContent>
                                                        </Tooltip>
                                                    </TooltipProvider>

                                                )}
                                            </TableCell>
                                            <TableCell className="font-medium">
                                                {truck.licensePlate}
                                                <div className="text-xs text-muted-foreground">{truck.province}</div>
                                            </TableCell>
                                            <TableCell>
                                                {truck.brand} {truck.model}
                                                <div className="text-xs text-muted-foreground">{truck.color} {truck.year}</div>
                                            </TableCell>
                                            <TableCell className="hidden md:table-cell text-xs">
                                                <div>{t("trucks.section.vin")}: {truck.vin || "-"}</div>
                                                <div>{t("trucks.section.engineNumber")}: {truck.engineNumber || "-"}</div>
                                            </TableCell>
                                            <TableCell>{truck.type}</TableCell>
                                            <TableCell>
                                                <Badge variant="outline" className="text-xs">{truck.truckStatus}</Badge>
                                            </TableCell>
                                            <TableCell className="text-xs capitalize">{truck.ownershipType}</TableCell>
                                        </TableRow>
                                    ))}
                                </TableBody>
                            </Table>
                        </ScrollArea>
                    </div>
                )}
            </div>

            <DialogFooter className="shrink-0">
                <Button variant="outline" onClick={() => setOpen(false)}>
                    {t('users.form.cancel')}
                </Button>
                <Button
                    onClick={handleImport}
                    disabled={importedData.length === 0 || isImporting || importedData.every(d => !d.isValid)}
                >
                    {isImporting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                    {t('trucks.import.confirm')}
                </Button>
            </DialogFooter>
        </DialogContent>
    );
}

function Loader2(props: any) {
    return (
        <svg
            {...props}
            xmlns="http://www.w3.org/2000/svg"
            width="24"
            height="24"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
        >
            <path d="M21 12a9 9 0 1 1-6.219-8.56" />
        </svg>
    );
}

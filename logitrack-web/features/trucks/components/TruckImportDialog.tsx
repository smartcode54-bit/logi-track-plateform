"use client";

import { useRef, useState } from "react";
import * as XLSX from "xlsx";
import { AlertCircle, CheckCircle, Download, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ScrollArea } from "@/components/ui/scroll-area";
import { useLanguage } from "@/context/language";
import type { LazyDialogControl } from "@/components/lazy-dialog";
import { apiErrorText } from "@/lib/apiError";
import { importTrucks, type TruckImportResult } from "../api/trucks";
import { useTruckWrites } from "../api/useTrucks";
import { importFieldErrorText, importPreviewRow } from "../utils/truckImport";
import { truckTypeLabel } from "../utils/labels";

type RejectedRow = TruckImportResult["errors"][number];

/** Preview uses Go's dry run: the exact import validators, aliases, dates and upsert rules. */
export function TruckImportDialog({ setOpen }: LazyDialogControl) {
    const { t } = useLanguage();
    const [rawRows, setRawRows] = useState<Record<string, unknown>[]>([]);
    const [rejected, setRejected] = useState<RejectedRow[]>([]);
    const [checking, setChecking] = useState(false);
    const [checked, setChecked] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const generation = useRef(0);
    const { importRows } = useTruckWrites();
    const busy = checking || importRows.isPending;

    const parseFile = async (file: File) => {
        const run = ++generation.current;
        setChecking(true);
        setChecked(false);
        setRawRows([]);
        setRejected([]);
        setError(null);
        try {
            const data = await new Promise<ArrayBuffer>((resolve, reject) => {
                const reader = new FileReader();
                reader.onload = () => resolve(reader.result as ArrayBuffer);
                reader.onerror = reject;
                reader.readAsArrayBuffer(file);
            });
            const workbook = XLSX.read(data, { type: "array", codepage: 65001 });
            const rows = XLSX.utils.sheet_to_json<Record<string, unknown>>(workbook.Sheets[workbook.SheetNames[0]]);
            const result = await importTrucks({ rawRows: rows, dryRun: true });
            if (run !== generation.current) return;
            setRawRows(rows);
            setRejected(result.errors);
            setChecked(true);
        } catch (error) {
            if (run === generation.current) setError(`${t("trucks.import.parseError")} ${apiErrorText(error, (key, fallback) => t(key, fallback))}`);
        } finally {
            if (run === generation.current) setChecking(false);
        }
    };

    const handleImport = async () => {
        if (!checked || busy || rawRows.length === 0) return;
        try {
            const result = await importRows.mutateAsync({ rawRows });
            toast.success(t("trucks.import.success", { count: result.created + result.updated }));
            if (result.errors.length > 0) {
                // Successful rows have landed. Keep only rejections, with Go's reasons, for correction.
                setRawRows(result.errors.map((row) => rawRows[row.row - 1]));
                setRejected(result.errors.map((row, index) => ({ ...row, row: index + 1 })));
                setError(t("trucks.import.correctRejected"));
                return;
            }
            setOpen(false);
        } catch (error) {
            setError(`${t("trucks.import.error")}: ${apiErrorText(error, (key, fallback) => t(key, fallback))}`);
        }
    };

    const downloadTemplate = () => {
        const headers = ["License Plate", "Province", "Type", "Brand", "Model", "Color", "Year", "VIN", "Engine Number", "Fuel Type", "Ownership", "Status", "Registration Date", "Max Load"];
        const sample = ["1กก-1234", "กรุงเทพมหานคร", "10 Wheels", "Isuzu", "FXZ360", "ขาว", "2023", "JH4KA8260MC000001", "ENG987654321", "diesel", "Company", "active", "2023-01-15", "25000"];
        const workbook = XLSX.utils.book_new();
        XLSX.utils.book_append_sheet(workbook, XLSX.utils.aoa_to_sheet([headers, sample]), "Template");
        XLSX.writeFile(workbook, "truck_import_template.xlsx");
    };

    return (
        <DialogContent showCloseButton={false} className="sm:max-w-[900px] max-h-[90vh] flex flex-col">
            <DialogHeader>
                <DialogTitle>{t("trucks.import.title")}</DialogTitle>
                <DialogDescription>{t("trucks.import.desc")}</DialogDescription>
            </DialogHeader>
            <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-hidden py-4">
                <div className="flex flex-wrap items-end gap-4">
                    <div className="grid w-full max-w-sm gap-2">
                        <Label htmlFor="truck-import-file">{t("trucks.import.selectFile")}</Label>
                        <Input id="truck-import-file" type="file" accept=".xlsx,.xls,.csv" disabled={busy}
                            onChange={(event) => { const file = event.target.files?.[0]; if (file) void parseFile(file); event.target.value = ""; }} />
                    </div>
                    <Button variant="secondary" onClick={downloadTemplate}><Download className="h-4 w-4" aria-hidden />{t("trucks.import.downloadTemplate")}</Button>
                </div>
                {checking && <p role="status">{t("common.loading")}</p>}
                {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
                {rawRows.length > 0 && <div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-md border">
                    <p className="border-b bg-muted p-2 text-sm font-semibold">{t("trucks.import.preview")} ({rawRows.length})</p>
                    <ScrollArea className="flex-1">
                        <Table>
                            <TableHeader><TableRow>
                                <TableHead>{t("trucks.table.plate")}</TableHead>
                                <TableHead>{t("trucks.table.model")}</TableHead>
                                <TableHead>{t("trucks.section.truckType")}</TableHead>
                                <TableHead>{t("trucks.import.validationResult")}</TableHead>
                            </TableRow></TableHeader>
                            <TableBody>{rawRows.map((raw, index) => {
                                const row = importPreviewRow(raw);
                                const failure = rejected.find((row) => row.row === index + 1);
                                return <TableRow key={index}>
                                    <TableCell>{row.licensePlate || t("trucks.ui4.missing")}<p className="text-xs text-muted-foreground">{row.province}</p></TableCell>
                                    <TableCell>{row.brand} {row.model}<p className="text-xs text-muted-foreground">{row.color} {row.year}</p></TableCell>
                                    <TableCell>{truckTypeLabel(row.type, t)}</TableCell>
                                    <TableCell>{failure ? <div className="space-y-1 text-sm text-destructive">
                                        <AlertCircle className="h-4 w-4" aria-hidden />
                                        {failure.fields.map((field, i) => <p key={i}>{importFieldErrorText(field, t)}</p>)}
                                    </div> : <span className="flex items-center gap-2 text-sm text-success"><CheckCircle className="h-4 w-4" aria-hidden />{t("trucks.import.valid")}</span>}</TableCell>
                                </TableRow>;
                            })}</TableBody>
                        </Table>
                    </ScrollArea>
                </div>}
            </div>
            <DialogFooter>
                <Button variant="outline" onClick={() => setOpen(false)} disabled={busy}>{t("trucks.edit.cancel")}</Button>
                <Button onClick={() => void handleImport()} disabled={!checked || busy || rawRows.length === 0 || rejected.length === rawRows.length}>
                    {busy && <Loader2 className="h-4 w-4 animate-spin" aria-hidden />}{t("trucks.import.confirm")}
                </Button>
            </DialogFooter>
        </DialogContent>
    );
}

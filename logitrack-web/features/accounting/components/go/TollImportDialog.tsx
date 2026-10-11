"use client";

/**
 * Toll import over Go (T42; `POST /v1/expenses/toll-import` as a form upload, Appendix B §B.2.15): the
 * spreadsheet goes to Go as it is, which matches the columns, keeps the `ผ่านทาง` rows and numbers them
 * (`toll_import_sequence`) on the server. "Check" sends it with `dryRun=true` (counts and skipped rows,
 * nothing written), "Import" without. No spreadsheet library loads in the browser.
 */
import { useState } from "react";
import { Loader2, Upload } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { apiErrorText } from "@/lib/apiError";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

import { importTolls, type TollImportResult } from "../../api/expenses.go";
import { SearchPicker, type PickValue } from "./shared";

export function TollImportDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
    const { t } = useLanguage();
    const [file, setFile] = useState<File | null>(null);
    const [driver, setDriver] = useState<PickValue | null>(null);
    const [truck, setTruck] = useState<PickValue | null>(null);
    const [check, setCheck] = useState<TollImportResult | null>(null);
    const [busy, setBusy] = useState<"check" | "import" | null>(null);
    const [error, setError] = useState<unknown>(null);

    const reset = () => {
        setFile(null);
        setCheck(null);
        setError(null);
    };
    const run = async (dryRun: boolean) => {
        if (!file || !truck || !driver) return;
        setBusy(dryRun ? "check" : "import");
        setError(null);
        try {
            const out = await importTolls({ file, driverId: driver.id, truckId: truck.id, dryRun });
            if (dryRun) setCheck(out);
            else {
                toast.success(t("accounting.go.toll.imported", { created: out.created, skipped: out.skipped }));
                reset();
                onOpenChange(false);
            }
        } catch (e) {
            setError(e);
        } finally {
            setBusy(null);
        }
    };

    return (
        <Dialog
            open={open}
            onOpenChange={(o) => {
                if (busy) return;
                onOpenChange(o);
                if (!o) reset();
            }}
        >
            <DialogContent className="sm:max-w-xl">
                <DialogHeader>
                    <DialogTitle>{t("accounting.tollImport.title")}</DialogTitle>
                    <DialogDescription>{t("accounting.tollImport.description")}</DialogDescription>
                </DialogHeader>
                <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-1">
                        <Label className="text-xs">{t("accounting.tollImport.selectTruck")}</Label>
                        <SearchPicker kind="truck" value={truck} onChange={(v) => { setTruck(v); setCheck(null); }} placeholder={t("accounting.tollImport.selectTruckPlaceholder")} className="w-full" />
                    </div>
                    <div className="space-y-1">
                        <Label className="text-xs">{t("accounting.tollImport.selectDriver")}</Label>
                        <SearchPicker kind="driver" value={driver} onChange={(v) => { setDriver(v); setCheck(null); }} placeholder={t("accounting.tollImport.selectDriverPlaceholder")} className="w-full" />
                    </div>
                    <div className="col-span-2 space-y-1">
                        <Label className="text-xs">{t("accounting.tollImport.clickUpload")}</Label>
                        <Input
                            type="file"
                            accept=".xlsx,.xls,.csv"
                            onChange={(e) => {
                                setFile(e.target.files?.[0] ?? null);
                                setCheck(null);
                            }}
                        />
                        <p className="text-xs text-muted-foreground">{t("accounting.tollImport.formatsSupported")}</p>
                    </div>
                </div>
                {error ? <p className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">{apiErrorText(error, t)}</p> : null}
                {check ? (
                    <div className="space-y-1 rounded-md border px-3 py-2 text-sm" data-testid="toll-check">
                        <p>{t("accounting.go.toll.checkResult", { created: check.created, skipped: check.skipped })}</p>
                        {check.skippedRows.length > 0 ? (
                            <ul className="max-h-32 overflow-auto text-xs text-muted-foreground">
                                {check.skippedRows.slice(0, 50).map((r) => (
                                    <li key={`${r.row}-${r.reason}`}>{t("accounting.go.toll.skippedRow", { row: r.row, reason: r.reason })}</li>
                                ))}
                            </ul>
                        ) : null}
                    </div>
                ) : null}
                <DialogFooter>
                    <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy !== null}>
                        {t("accounting.tollImport.cancel")}
                    </Button>
                    <Button variant="secondary" onClick={() => void run(true)} disabled={!file || !truck || !driver || busy !== null}>
                        {busy === "check" ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {t("accounting.go.toll.check")}
                    </Button>
                    <Button onClick={() => void run(false)} disabled={!file || !truck || !driver || !check || check.created === 0 || busy !== null}>
                        {busy === "import" ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Upload className="mr-2 h-4 w-4" />}
                        {busy === "import" ? t("accounting.tollImport.uploading") : t("accounting.tollImport.upload")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

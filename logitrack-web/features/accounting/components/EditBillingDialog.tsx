"use client";

import { useEffect, useMemo, useState } from "react";
import { Loader2, AlertTriangle, CheckCircle2 } from "lucide-react";
import { db } from "@/firebase/client";
import { useLanguage } from "@/context/language";
import {
    fetchRateEntriesForCustomers,
    fetchFuelAdjustmentsForCustomers,
    extractHubId,
    normalizeDestinationCode,
} from "@/lib/billingRates";
import {
    computeTripBillingFromParts,
    type BillingRateEntry,
    type FuelRateAdjustment,
    type TripBillingComputed,
} from "@/lib/billingCompute";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { writeTripBillingSnapshot, type WriteTripBillingInput, type MissingBillingRow } from "../api/billing";

export interface EditBillingDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    row: MissingBillingRow;
    hubNameMap: Map<string, string>;
    onSaved: (tripId: string) => void;
}

/** Build name→code reverse map from code→name hubNameMap */
function buildNameToCodeMap(hubNameMap: Map<string, string>): Map<string, string> {
    const reverse = new Map<string, string>();
    hubNameMap.forEach((name, code) => {
        if (name && code && !reverse.has(name)) reverse.set(name, code);
    });
    return reverse;
}

export function EditBillingDialog({ open, onOpenChange, row, hubNameMap, onSaved }: EditBillingDialogProps) {
    const { t } = useLanguage();

    // Reverse map: display name → PDP code
    const nameToCode = useMemo(() => buildNameToCodeMap(hubNameMap), [hubNameMap]);

    // Resolved billing lookup keys from the trip's existing data
    const resolvedHubId = useMemo(() => extractHubId(row.sourceHub), [row.sourceHub]);
    const resolvedDestinationCode = useMemo(() => {
        const raw = row.destination?.trim() ?? "";
        if (!raw) return "";
        // Try resolving display name → PDP code first
        const pdpCode = nameToCode.get(raw) ?? raw;
        return normalizeDestinationCode(pdpCode);
    }, [row.destination, nameToCode]);

    // Rate data
    const [rateEntries, setRateEntries] = useState<BillingRateEntry[]>([]);
    const [fuelAdjustments, setFuelAdjustments] = useState<FuelRateAdjustment[]>([]);
    const [dataLoaded, setDataLoaded] = useState(false);
    const [loadError, setLoadError] = useState<string | null>(null);

    // Auto-computed result
    const [computedResult, setComputedResult] = useState<TripBillingComputed | null>(null);
    const [computeError, setComputeError] = useState<string | null>(null);

    // Price input — always visible, pre-filled with computed rate
    const [priceStr, setPriceStr] = useState("");
    const [priceError, setPriceError] = useState<string | null>(null);

    // Save state
    const [saving, setSaving] = useState(false);
    const [saveError, setSaveError] = useState<string | null>(null);

    // Load rate data when dialog opens
    useEffect(() => {
        if (!open || !row.customerId) {
            setDataLoaded(false);
            return;
        }
        setDataLoaded(false);
        setLoadError(null);
        setComputedResult(null);
        setComputeError(null);
        setPriceStr("");
        setPriceError(null);
        setSaveError(null);

        Promise.all([
            fetchRateEntriesForCustomers(db, [row.customerId]),
            fetchFuelAdjustmentsForCustomers(db, [row.customerId]),
        ])
            .then(([rates, fuels]) => {
                setRateEntries(rates);
                setFuelAdjustments(fuels);
                setDataLoaded(true);
            })
            .catch(() => {
                setLoadError(t("accounting.income.editBilling.loadDataError") || "Failed to load rate data");
            });
    }, [open, row.customerId, t]);

    // Auto-compute when rate data is ready
    useEffect(() => {
        if (!dataLoaded || !row.customerId) return;

        const tripTimestamps = {
            deliveredTimestamp: row.deliveredTimestamp,
            createdAt: row.createdAt,
        };
        const taskInput = {
            // Use the resolved PDP code so billing lookup matches the rate card
            sourceHub: row.sourceHub,
            destination: resolvedDestinationCode || row.destination,
            truckType: row.truckType ?? "4WJ",
            sourceHubLinkedCustomerId: row.customerId,
            destinationLinkedCustomerId: undefined,
        };

        // Derive หลัก/เสริม (ADR-0005): try primary card first, fall back to supplementary.
        const result = computeTripBillingFromParts(tripTimestamps, taskInput, rateEntries, fuelAdjustments, "PRIMARY")
            ?? computeTripBillingFromParts(tripTimestamps, taskInput, rateEntries, fuelAdjustments, "SUPPLEMENTARY");
        if (result) {
            setComputedResult(result);
            setComputeError(null);
            setPriceStr(String(result.finalRateThb));
        } else {
            setComputedResult(null);
            setComputeError(t("accounting.income.editBilling.noRateMatch") || "No rate card found for this route");
        }
    }, [dataLoaded, rateEntries, fuelAdjustments, row, resolvedDestinationCode]);

    const handleSave = async () => {
        const rate = parseFloat(priceStr);
        if (!Number.isFinite(rate) || rate <= 0) {
            setPriceError(t("accounting.income.editBilling.manualRateInvalid") || "Enter a valid positive number");
            return;
        }

        setSaving(true);
        setSaveError(null);
        setPriceError(null);

        try {
            const isManual = computedResult == null || Math.abs(rate - computedResult.finalRateThb) > 0.001;

            const billingInput: WriteTripBillingInput = {
                tripId: row.id,
                billingEstimateThb: rate,
                billingBaseRateThb: computedResult?.baseRateThb ?? rate,
                billingRateImportId: computedResult?.rateImportId ?? "manual",
                billingLookupHubId: computedResult?.lookupHubId ?? resolvedHubId,
                billingLookupDestination: computedResult?.lookupDestination ?? resolvedDestinationCode,
                billingFuelAdjustmentId: computedResult?.fuelAdjustmentId ?? null,
                billingRateMultiplier: computedResult?.rateMultiplier ?? 1,
                billingAddThbPerTrip: computedResult?.addThbPerTrip ?? 0,
                billingEffectiveFromDateStr: computedResult?.effectiveFromDateStr ?? null,
                billingCustomerId: row.customerId ?? "",
                billingManualOverride: isManual,
            };

            await writeTripBillingSnapshot(billingInput);
            onSaved(row.id);
            onOpenChange(false);
        } catch {
            setSaveError(t("accounting.income.editBilling.saveError") || "Could not save billing");
        } finally {
            setSaving(false);
        }
    };

    const priceNum = parseFloat(priceStr);
    const isSaveDisabled = saving || !priceStr || !Number.isFinite(priceNum) || priceNum <= 0;
    const isManualPrice = computedResult != null && Number.isFinite(priceNum) && Math.abs(priceNum - computedResult.finalRateThb) > 0.001;
    const destinationResolved = resolvedDestinationCode && resolvedDestinationCode !== (row.destination ?? "").trim();

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="max-w-lg max-h-[90vh] overflow-y-auto">
                <DialogHeader>
                    <DialogTitle>{t("accounting.income.editBilling.title") || "Set Billing"}</DialogTitle>
                    <DialogDescription>
                        Trip {row.spxTripId || row.id} • {row.customerName}
                    </DialogDescription>
                </DialogHeader>

                <div className="space-y-4 py-2">
                    {/* Trip info — read-only */}
                    <div className="rounded-md border bg-muted/30 p-3 space-y-2 text-sm">
                        <div className="grid grid-cols-2 gap-x-4 gap-y-2">
                            <div>
                                <div className="text-xs text-muted-foreground mb-0.5">ต้นทาง</div>
                                <div className="font-medium">{row.sourceHub || "—"}</div>
                            </div>
                            <div>
                                <div className="text-xs text-muted-foreground mb-0.5">ปลายทาง</div>
                                <div className="font-medium">{row.destination || "—"}</div>
                            </div>
                            <div>
                                <div className="text-xs text-muted-foreground mb-0.5">ประเภทรถ</div>
                                <div className="font-medium">{row.truckType || "—"}</div>
                            </div>
                            <div>
                                <div className="text-xs text-muted-foreground mb-0.5">วันที่ส่ง</div>
                                <div className="font-medium">
                                    {row.deliveredTimestamp
                                        ? row.deliveredTimestamp.toLocaleDateString("th-TH")
                                        : "—"}
                                </div>
                            </div>
                        </div>

                        {/* Billing lookup keys */}
                        <div className="border-t pt-2 font-mono text-xs text-muted-foreground">
                            <span className="not-italic">Billing keys: </span>
                            <span className="text-foreground">{resolvedHubId || "—"}</span>
                            <span> → </span>
                            <span className={destinationResolved ? "text-amber-600 dark:text-amber-400" : "text-foreground"}>
                                {resolvedDestinationCode || "—"}
                            </span>
                            {destinationResolved && (
                                <span className="text-muted-foreground italic not-italic">
                                    {" "}(resolved from &quot;{row.destination}&quot;)
                                </span>
                            )}
                        </div>
                    </div>

                    {/* Loading state */}
                    {!dataLoaded && row.customerId && !loadError && (
                        <div className="flex items-center gap-2 text-sm text-muted-foreground">
                            <Loader2 className="h-4 w-4 animate-spin" />
                            กำลังโหลด Rate Card...
                        </div>
                    )}

                    {loadError && (
                        <Alert className="border-destructive/50 bg-destructive/10">
                            <AlertTriangle className="h-4 w-4 text-destructive" />
                            <AlertDescription className="text-destructive">{loadError}</AlertDescription>
                        </Alert>
                    )}

                    {/* Auto-computed rate result */}
                    {computedResult && (
                        <Alert className="border-green-200 bg-green-50 dark:bg-green-950/20">
                            <CheckCircle2 className="h-4 w-4 text-green-600" />
                            <AlertDescription>
                                <div className="text-green-800 dark:text-green-300 space-y-1">
                                    <div className="font-semibold">
                                        พบ Rate Card — คำนวณได้ ฿{computedResult.finalRateThb.toLocaleString()}
                                    </div>
                                    <div className="font-mono text-xs opacity-80">
                                        ฿{computedResult.baseRateThb} × {computedResult.rateMultiplier.toFixed(2)} + ฿{computedResult.addThbPerTrip} = ฿{computedResult.finalRateThb}
                                    </div>
                                </div>
                            </AlertDescription>
                        </Alert>
                    )}

                    {computeError && dataLoaded && (
                        <Alert className="border-amber-200 bg-amber-50 dark:bg-amber-950/20">
                            <AlertTriangle className="h-4 w-4 text-amber-600" />
                            <AlertDescription className="text-amber-800 dark:text-amber-300 text-sm">
                                {computeError}
                            </AlertDescription>
                        </Alert>
                    )}

                    {!row.taskId && (
                        <Alert className="border-blue-200 bg-blue-50 dark:bg-blue-950/20">
                            <AlertTriangle className="h-4 w-4 text-blue-600" />
                            <AlertDescription className="text-blue-800 dark:text-blue-300 text-sm">
                                {t("accounting.income.editBilling.noTaskNote") ||
                                    "Trip has no linked task — billing will be saved directly."}
                            </AlertDescription>
                        </Alert>
                    )}

                    {/* Price input — always shown */}
                    <div className="space-y-1.5">
                        <Label htmlFor="billing-price" className="flex items-center gap-2">
                            ราคา (THB)
                            {isManualPrice && (
                                <Badge variant="secondary" className="text-xs font-normal">
                                    Manual override
                                </Badge>
                            )}
                        </Label>
                        <Input
                            id="billing-price"
                            type="number"
                            placeholder="0.00"
                            value={priceStr}
                            onChange={(e) => {
                                setPriceStr(e.target.value);
                                setPriceError(null);
                            }}
                            step="0.01"
                            min="0"
                            className="text-lg font-semibold"
                        />
                        {priceError && <p className="text-destructive text-xs">{priceError}</p>}
                        {computedResult && !isManualPrice && (
                            <p className="text-xs text-muted-foreground">
                                ราคาจาก Rate Card — แก้ไขได้หากต้องการ
                            </p>
                        )}
                        {computeError && dataLoaded && (
                            <p className="text-xs text-muted-foreground">
                                ไม่พบ Rate Card — กรอกราคาด้วยตนเอง
                            </p>
                        )}
                    </div>

                    {saveError && <p className="text-destructive text-sm">{saveError}</p>}
                </div>

                <DialogFooter>
                    <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
                        {t("common.cancel") || "Cancel"}
                    </Button>
                    <Button onClick={() => void handleSave()} disabled={isSaveDisabled}>
                        {saving && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                        {t("accounting.income.editBilling.save") || "Save Billing"}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

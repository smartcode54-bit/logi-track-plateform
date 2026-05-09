"use client";

import { useEffect, useState } from "react";
import { Loader2, AlertTriangle } from "lucide-react";
import { db } from "@/firebase/client";
import { useLanguage } from "@/context/language";
import {
    fetchRateEntriesForCustomers,
    fetchFuelAdjustmentsForCustomers,
    extractHubId,
    normalizeDestinationCode,
} from "@/lib/billingRates";
import { computeTripBillingFromParts, type BillingRateEntry, type FuelRateAdjustment, type TripBillingComputed } from "@/lib/billingCompute";
import { SOC_DESTINATIONS } from "@/validate/taskSchema";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { SearchableSelect } from "@/components/ui/searchable-select";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Switch } from "@/components/ui/switch";
import { writeTripBillingSnapshot, updateTaskBillingFields, type WriteTripBillingInput } from "../actions.client";
import type { MissingBillingRow } from "./page";

export interface EditBillingDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    row: MissingBillingRow;
    hubNameMap: Map<string, string>;
    onSaved: (tripId: string) => void;
}

const TRUCK_TYPES = ["4WH", "4WJ", "6WH", "10WH", "18WH", "PICKUP", "VAN"];

export function EditBillingDialog({ open, onOpenChange, row, hubNameMap, onSaved }: EditBillingDialogProps) {
    const { t } = useLanguage();

    // Editable fields (truckType is read-only, tied to driver)
    const [sourceHub, setSourceHub] = useState(row.sourceHub ?? "");
    const [destination, setDestination] = useState(row.destination ?? "");

    // Manual override mode
    const [isManualOverride, setIsManualOverride] = useState(false);
    const [manualRateStr, setManualRateStr] = useState("");
    const [manualRateError, setManualRateError] = useState<string | null>(null);

    // Live preview
    const [previewResult, setPreviewResult] = useState<TripBillingComputed | null>(null);
    const [previewLoading, setPreviewLoading] = useState(false);
    const [previewError, setPreviewError] = useState<string | null>(null);

    // Rate data
    const [rateEntries, setRateEntries] = useState<BillingRateEntry[]>([]);
    const [fuelAdjustments, setFuelAdjustments] = useState<FuelRateAdjustment[]>([]);
    const [dataLoaded, setDataLoaded] = useState(false);

    // Save state
    const [saving, setSaving] = useState(false);
    const [saveError, setSaveError] = useState<string | null>(null);

    // Load rate data on dialog open
    useEffect(() => {
        if (!open || !row.customerId) {
            setDataLoaded(false);
            return;
        }
        setDataLoaded(false);
        setPreviewError(null);
        setPreviewResult(null);
        setManualRateStr("");
        setManualRateError(null);
        setSaveError(null);
        setIsManualOverride(false);

        Promise.all([
            fetchRateEntriesForCustomers(db, [row.customerId]),
            fetchFuelAdjustmentsForCustomers(db, [row.customerId]),
        ])
            .then(([rates, fuels]) => {
                setRateEntries(rates);
                setFuelAdjustments(fuels);
                setDataLoaded(true);
            })
            .catch((err) => {
                console.error("Failed to load rate data:", err);
                setPreviewError(t("accounting.income.editBilling.loadDataError") || "Failed to load rate data");
            });
    }, [open, row.customerId, t]);

    // Compute preview whenever inputs change
    useEffect(() => {
        if (!dataLoaded || !row.customerId) return;
        setPreviewLoading(true);

        try {
            const tripTimestamps = {
                deliveredTimestamp: row.deliveredTimestamp,
                createdAt: row.createdAt,
            };
            const taskInput = {
                sourceHub,
                destination,
                truckType: row.truckType ?? "4WJ",
                sourceHubLinkedCustomerId: row.customerId,
                destinationLinkedCustomerId: undefined,
            };
            const result = computeTripBillingFromParts(tripTimestamps, taskInput, rateEntries, fuelAdjustments);
            setPreviewResult(result);
            setPreviewError(result ? null : t("accounting.income.editBilling.noRateMatch") || "No rate card found");
        } catch (err) {
            console.error("Compute error:", err);
            setPreviewError(t("accounting.income.editBilling.noRateMatch") || "No rate card found");
        }
        setPreviewLoading(false);
    }, [sourceHub, destination, dataLoaded, rateEntries, fuelAdjustments, row.customerId, row.deliveredTimestamp, row.createdAt, row.truckType, t]);

    // Build origin options: Hub + SOC both included
    const originOptions = Array.from(hubNameMap.entries())
        .map(([code, name]) => {
            const isSoc = code.toUpperCase().startsWith("SOC");
            return {
                value: isSoc ? code : `${code} - ${name}`,
                label: isSoc ? `${code} (${name})` : `${code} - ${name}`,
            };
        });

    // Build destination options: SOC + Hub both included
    const destOptions = [
        ...Object.entries(SOC_DESTINATIONS).map(([key, value]) => ({
            value: key,
            label: `${key} (${value})`,
        })),
        ...Array.from(hubNameMap.entries())
            .map(([code, name]) => {
                const isSoc = code.toUpperCase().startsWith("SOC");
                return {
                    value: isSoc ? code : `${code} - ${name}`,
                    label: isSoc ? `${code} (${name})` : `${code} - ${name}`,
                };
            }),
    ];

    // Remove duplicates by value
    const uniqueDestOptions = Array.from(
        new Map(destOptions.map((opt) => [opt.value, opt])).values()
    );

    const handleSaveRecompute = async () => {
        if (!previewResult) {
            setPreviewError(t("accounting.income.editBilling.noRateMatch") || "No rate card found");
            return;
        }

        setSaving(true);
        setSaveError(null);

        try {
            const billingInput: WriteTripBillingInput = {
                tripId: row.id,
                billingEstimateThb: previewResult.finalRateThb,
                billingBaseRateThb: previewResult.baseRateThb,
                billingRateImportId: previewResult.rateImportId,
                billingLookupHubId: previewResult.lookupHubId,
                billingLookupDestination: previewResult.lookupDestination,
                billingFuelAdjustmentId: previewResult.fuelAdjustmentId ?? null,
                billingRateMultiplier: previewResult.rateMultiplier,
                billingAddThbPerTrip: previewResult.addThbPerTrip,
                billingEffectiveFromDateStr: previewResult.effectiveFromDateStr ?? null,
                billingCustomerId: previewResult.customerId,
            };

            const tasks = [];
            tasks.push(writeTripBillingSnapshot(billingInput));

            // Update task if it exists and fields changed (truckType stays unchanged, tied to driver)
            if (
                row.taskId &&
                (sourceHub !== (row.sourceHub ?? "") || destination !== (row.destination ?? ""))
            ) {
                tasks.push(
                    updateTaskBillingFields(row.taskId, {
                        sourceHub,
                        destination,
                        truckType: row.truckType ?? "4WJ",
                    })
                );
            }

            await Promise.all(tasks);
            onSaved(row.id);
            onOpenChange(false);
        } catch (err) {
            console.error("Save error:", err);
            setSaveError(t("accounting.income.editBilling.saveError") || "Could not save billing");
        } finally {
            setSaving(false);
        }
    };

    const handleSaveManualOverride = async () => {
        const rate = parseFloat(manualRateStr);
        if (!Number.isFinite(rate) || rate <= 0) {
            setManualRateError(t("accounting.income.editBilling.manualRateInvalid") || "Enter a valid positive number");
            return;
        }

        setSaving(true);
        setSaveError(null);
        setManualRateError(null);

        try {
            const billingInput: WriteTripBillingInput = {
                tripId: row.id,
                billingEstimateThb: rate,
                billingBaseRateThb: rate,
                billingRateImportId: "manual",
                billingLookupHubId: extractHubId(sourceHub),
                billingLookupDestination: normalizeDestinationCode(destination),
                billingFuelAdjustmentId: null,
                billingRateMultiplier: 1,
                billingAddThbPerTrip: 0,
                billingEffectiveFromDateStr: null,
                billingCustomerId: row.customerId ?? "",
                billingManualOverride: true,
            };

            await writeTripBillingSnapshot(billingInput);
            onSaved(row.id);
            onOpenChange(false);
        } catch (err) {
            console.error("Save error:", err);
            setSaveError(t("accounting.income.editBilling.saveError") || "Could not save billing");
        } finally {
            setSaving(false);
        }
    };

    const handleSave = async () => {
        if (isManualOverride) {
            await handleSaveManualOverride();
        } else {
            await handleSaveRecompute();
        }
    };

    const isSaveDisabled = saving || (isManualOverride ? !manualRateStr || manualRateError !== null : !previewResult);

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto overflow-x-hidden">
                <DialogHeader>
                    <DialogTitle>{t("accounting.income.editBilling.title") || "Edit Billing"}</DialogTitle>
                    <DialogDescription>
                        Trip {row.spxTripId || row.id} • Customer: {row.customerName}
                    </DialogDescription>
                </DialogHeader>

                <div className="space-y-4 py-2">
                    {/* Read-only info */}
                    <div className="grid grid-cols-2 gap-2 text-sm bg-muted/30 rounded p-3">
                        <div>
                            <span className="font-semibold">Origin:</span> {row.sourceHub}
                        </div>
                        <div>
                            <span className="font-semibold">Destination:</span> {row.destination}
                        </div>
                        <div>
                            <span className="font-semibold">Vehicle:</span> {row.truckType}
                        </div>
                        <div>
                            <span className="font-semibold">Delivered:</span> {row.deliveredTimestamp?.toLocaleString?.() ?? "N/A"}
                        </div>
                    </div>

                    {/* No taskId warning */}
                    {!row.taskId && (
                        <Alert className="border-blue-200 bg-blue-50">
                            <AlertTriangle className="h-4 w-4 text-blue-600" />
                            <AlertDescription className="text-blue-800">
                                {t("accounting.income.editBilling.noTaskNote") ||
                                    "This trip has no linked task. Billing will be saved but task fields will not be updated."}
                            </AlertDescription>
                        </Alert>
                    )}

                    {/* Editable fields - Origin & Destination only */}
                    <div className="grid grid-cols-2 gap-4">
                        <div>
                            <SearchableSelect
                                id="origin"
                                label={t("accounting.income.editBilling.origin") || "Origin (Hub)"}
                                value={sourceHub}
                                onValueChange={setSourceHub}
                                options={originOptions}
                                placeholder={t("accounting.income.editBilling.selectOrigin") || "Search origin..."}
                            />
                        </div>
                        <div>
                            <SearchableSelect
                                id="destination"
                                label={t("accounting.income.editBilling.destination") || "Destination"}
                                value={destination}
                                onValueChange={setDestination}
                                options={uniqueDestOptions}
                                placeholder={t("accounting.income.editBilling.selectDestination") || "Search destination..."}
                            />
                        </div>
                    </div>

                    {/* Preview box */}
                    <div className="rounded-md border p-3 space-y-2 bg-slate-50">
                        <p className="text-xs font-semibold text-muted-foreground">
                            {t("accounting.income.editBilling.preview") || "Computed rate preview"}
                        </p>
                        {previewLoading && (
                            <div className="flex items-center gap-2">
                                <Loader2 className="h-4 w-4 animate-spin" />
                                <span className="text-sm">Computing...</span>
                            </div>
                        )}
                        {previewResult && !isManualOverride && (
                            <div className="space-y-1 text-sm">
                                <div className="grid grid-cols-4 gap-2 font-mono text-xs">
                                    <div>
                                        <span className="text-muted-foreground">Base:</span> ฿{previewResult.baseRateThb.toLocaleString()}
                                    </div>
                                    <div>
                                        <span className="text-muted-foreground">×</span> {previewResult.rateMultiplier.toFixed(2)}
                                    </div>
                                    <div>
                                        <span className="text-muted-foreground">+</span> ฿{previewResult.addThbPerTrip}
                                    </div>
                                    <div className="font-bold text-green-600">= ฿{previewResult.finalRateThb.toLocaleString()}</div>
                                </div>
                            </div>
                        )}
                        {previewError && !isManualOverride && <p className="text-destructive text-xs">{previewError}</p>}
                    </div>

                    {/* Manual override toggle + input */}
                    <div className="space-y-2">
                        <div className="flex items-center gap-2">
                            <Switch id="manual-override" checked={isManualOverride} onCheckedChange={setIsManualOverride} />
                            <Label htmlFor="manual-override" className="cursor-pointer">
                                {t("accounting.income.editBilling.manualOverride") || "Manual rate override"}
                            </Label>
                        </div>
                        {isManualOverride && (
                            <div>
                                <Label htmlFor="manualRate">{t("accounting.income.editBilling.manualRate") || "Final rate (THB)"}</Label>
                                <Input
                                    id="manualRate"
                                    type="number"
                                    placeholder="0.00"
                                    value={manualRateStr}
                                    onChange={(e) => {
                                        setManualRateStr(e.target.value);
                                        setManualRateError(null);
                                    }}
                                    step="0.01"
                                    min="0"
                                />
                                {manualRateError && <p className="text-destructive text-xs mt-1">{manualRateError}</p>}
                            </div>
                        )}
                    </div>

                    {/* Error message */}
                    {saveError && <p className="text-destructive text-sm">{saveError}</p>}
                </div>

                <DialogFooter>
                    <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
                        {t("common.cancel") || "Cancel"}
                    </Button>
                    <Button onClick={handleSave} disabled={isSaveDisabled}>
                        {saving && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                        {t("accounting.income.editBilling.save") || "Save Billing"}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

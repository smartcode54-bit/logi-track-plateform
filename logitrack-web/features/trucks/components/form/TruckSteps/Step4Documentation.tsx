"use client";

import { useFormContext } from "react-hook-form";
import { TruckFormValues } from "@/validate/truckSchema";
import { CheckCircle2, FileText, Truck, Shield } from "lucide-react";

import { useLanguage } from "@/context/language";

export function Step4Documentation() {
    const form = useFormContext<TruckFormValues>();
    const values = form.getValues();
    const { t } = useLanguage();

    return (
        <div className="space-y-6">
            <div className="bg-card border rounded-lg p-6 animate-in fade-in slide-in-from-bottom-4 duration-500">
                <h3 className="text-lg font-medium mb-1">{t("trucks.step4.title")}</h3>
                <p className="text-sm text-muted-foreground mb-6">{t("trucks.step4.desc")}</p>

                <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                    {/* Summary Card 1: Specs */}
                    <div className="p-4 border rounded-lg bg-muted/20">
                        <div className="flex items-center gap-2 mb-3">
                            <Truck className="h-4 w-4 text-primary" />
                            <h4 className="font-semibold text-sm">{t("trucks.step4.specs")}</h4>
                        </div>
                        <div className="space-y-2 text-sm">
                            <div className="flex justify-between"><span className="text-muted-foreground">{t("trucks.step4.makeModel")}:</span> <span>{values.brand} {values.model}</span></div>
                            <div className="flex justify-between"><span className="text-muted-foreground">{t("trucks.step4.plate")}:</span> <span className="font-medium">{values.licensePlate}</span></div>
                            <div className="flex justify-between"><span className="text-muted-foreground">{t("trucks.step4.vin")}:</span> <span className="font-mono text-xs">{values.vin || t("trucks.ui4.notAvailable")}</span></div>
                            <div className="flex justify-between"><span className="text-muted-foreground">{t("trucks.step4.type")}:</span> <span>{values.type}</span></div>
                        </div>
                    </div>

                    {/* Summary Card 2: Compliance */}
                    <div className="p-4 border rounded-lg bg-muted/20">
                        <div className="flex items-center gap-2 mb-3">
                            <FileText className="h-4 w-4 text-warning" />
                            <h4 className="font-semibold text-sm">{t("trucks.step4.compliance")}</h4>
                        </div>
                        <div className="space-y-2 text-sm">
                            <div className="flex justify-between"><span className="text-muted-foreground">{t("trucks.step4.province")}:</span> <span>{values.province}</span></div>
                            <div className="flex justify-between"><span className="text-muted-foreground">{t("trucks.step4.taxDoc")}:</span> <span>{values.documentTax ? t("trucks.step4.uploaded") : t("trucks.step4.pending")}</span></div>
                            <div className="flex justify-between"><span className="text-muted-foreground">{t("trucks.step4.regDoc")}:</span> <span>{values.documentRegister ? t("trucks.step4.uploaded") : t("trucks.step4.pending")}</span></div>
                        </div>
                    </div>

                    {/* Summary Card 3: Insurance */}
                    <div className="p-4 border rounded-lg bg-muted/20">
                        <div className="flex items-center gap-2 mb-3">
                            <Shield className="h-4 w-4 text-success" />
                            <h4 className="font-semibold text-sm">{t("trucks.step4.insurance")}</h4>
                        </div>
                        <div className="space-y-2 text-sm">
                            <div className="flex justify-between"><span className="text-muted-foreground">{t("trucks.step4.provider")}:</span> <span>{values.insuranceCompany || t("trucks.ui4.notAvailable")}</span></div>
                            <div className="flex justify-between"><span className="text-muted-foreground">{t("trucks.step4.policy")}:</span> <span>{values.insurancePolicyNumber || t("trucks.ui4.notAvailable")}</span></div>
                            <div className="flex justify-between"><span className="text-muted-foreground">{t("trucks.step4.expiry")}:</span> <span className={!values.insuranceExpiryDate ? "" : "text-success"}>{values.insuranceExpiryDate || t("trucks.ui4.notAvailable")}</span></div>
                        </div>
                    </div>
                </div>

                <div className="mt-8 flex items-center justify-center p-6 border-2 border-dashed rounded-xl bg-primary/10 border-border">
                    <div className="text-center">
                        <div className="inline-flex h-12 w-12 items-center justify-center rounded-full bg-primary/10 mb-4">
                            <CheckCircle2 className="h-6 w-6 text-primary" />
                        </div>
                        <h3 className="text-lg font-medium text-primary">{t("trucks.step4.ready")}</h3>
                        <p className="text-sm text-primary max-w-xs mx-auto mt-1">
                            {t("trucks.step4.readyDesc")}
                        </p>
                    </div>
                </div>
            </div>
        </div>
    );
}

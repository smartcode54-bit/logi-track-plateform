"use client";

import { useFormContext } from "react-hook-form";
import { TruckFormValues } from "@/validate/truckSchema";
import { CheckCircle2, FileText, Truck, Shield } from "lucide-react";

export function Step4Documentation() {
    const form = useFormContext<TruckFormValues>();
    const values = form.getValues();

    return (
        <div className="space-y-6">
            <div className="bg-card border rounded-lg p-6 animate-in fade-in slide-in-from-bottom-4 duration-500">
                <h3 className="text-lg font-medium mb-1">Step 4: Review & Finalize</h3>
                <p className="text-sm text-muted-foreground mb-6">Please review all information before adding this vehicle to flight.</p>

                <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                    {/* Summary Card 1: Specs */}
                    <div className="p-4 border rounded-lg bg-muted/20">
                        <div className="flex items-center gap-2 mb-3">
                            <Truck className="h-4 w-4 text-blue-600" />
                            <h4 className="font-semibold text-sm">Vehicle Specs</h4>
                        </div>
                        <div className="space-y-2 text-sm">
                            <div className="flex justify-between"><span className="text-muted-foreground">Make/Model:</span> <span>{values.brand} {values.model}</span></div>
                            <div className="flex justify-between"><span className="text-muted-foreground">Plate:</span> <span className="font-medium">{values.licensePlate}</span></div>
                            <div className="flex justify-between"><span className="text-muted-foreground">VIN:</span> <span className="font-mono text-xs">{values.vin || "N/A"}</span></div>
                            <div className="flex justify-between"><span className="text-muted-foreground">Type:</span> <span>{values.type}</span></div>
                        </div>
                    </div>

                    {/* Summary Card 2: Compliance */}
                    <div className="p-4 border rounded-lg bg-muted/20">
                        <div className="flex items-center gap-2 mb-3">
                            <FileText className="h-4 w-4 text-orange-600" />
                            <h4 className="font-semibold text-sm">Compliance</h4>
                        </div>
                        <div className="space-y-2 text-sm">
                            <div className="flex justify-between"><span className="text-muted-foreground">Province:</span> <span>{values.province}</span></div>
                            <div className="flex justify-between"><span className="text-muted-foreground">Tax Doc:</span> <span>{values.documentTax ? "Uploaded" : "Pending"}</span></div>
                            <div className="flex justify-between"><span className="text-muted-foreground">Reg Doc:</span> <span>{values.documentRegister ? "Uploaded" : "Pending"}</span></div>
                        </div>
                    </div>

                    {/* Summary Card 3: Insurance */}
                    <div className="p-4 border rounded-lg bg-muted/20">
                        <div className="flex items-center gap-2 mb-3">
                            <Shield className="h-4 w-4 text-green-600" />
                            <h4 className="font-semibold text-sm">Insurance</h4>
                        </div>
                        <div className="space-y-2 text-sm">
                            <div className="flex justify-between"><span className="text-muted-foreground">Provider:</span> <span>{values.insuranceCompany || "N/A"}</span></div>
                            <div className="flex justify-between"><span className="text-muted-foreground">Policy #:</span> <span>{values.insurancePolicyNumber || "N/A"}</span></div>
                            <div className="flex justify-between"><span className="text-muted-foreground">Expiry:</span> <span className={!values.insuranceExpiryDate ? "" : "text-green-600"}>{values.insuranceExpiryDate || "N/A"}</span></div>
                        </div>
                    </div>
                </div>

                <div className="mt-8 flex items-center justify-center p-6 border-2 border-dashed rounded-xl bg-blue-50/50 border-blue-200">
                    <div className="text-center">
                        <div className="inline-flex h-12 w-12 items-center justify-center rounded-full bg-blue-100 mb-4">
                            <CheckCircle2 className="h-6 w-6 text-blue-600" />
                        </div>
                        <h3 className="text-lg font-medium text-blue-900">Ready to Submit</h3>
                        <p className="text-sm text-blue-700 max-w-xs mx-auto mt-1">
                            By clicking "Save and Continue", this vehicle will be added to the active fleet registry.
                        </p>
                    </div>
                </div>
            </div>
        </div>
    );
}

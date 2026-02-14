"use client";

import { useFormContext } from "react-hook-form";
import { TruckFormValues } from "@/validate/truckSchema";
import {
    FormControl,
    FormField,
    FormItem,
    FormLabel,
    FormMessage,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { InsuranceSection } from "../InsuranceSection";
import { RegistrationSection } from "../RegistrationSection";

interface Step2Props {
    onFileSelect: (field: string, file: File, blobUrl: string) => void;
}

export function Step2Compliance({ onFileSelect }: Step2Props) {
    const form = useFormContext<TruckFormValues>();
    const ownershipType = form.watch("ownershipType");

    return (
        <div className="space-y-6">
            <div className="bg-card border rounded-lg p-6 animate-in fade-in slide-in-from-bottom-4 duration-500">
                <h3 className="text-lg font-medium mb-1">Step 2: Compliance</h3>
                <p className="text-sm text-muted-foreground mb-6">Enter registration and insurance details.</p>

                {/* Registration Section - Reuse existing or inline */}
                {ownershipType === 'own' ? (
                    <RegistrationSection />
                ) : (
                    <div className="p-4 bg-muted/50 rounded-md text-sm text-muted-foreground">
                        Registration details are optional for subcontractor vehicles.
                    </div>
                )}
            </div>

            <div className="bg-card border rounded-lg p-6 animate-in fade-in slide-in-from-bottom-5 duration-500 delay-100">
                <h3 className="text-lg font-medium mb-4">Insurance Information</h3>
                <InsuranceSection onFileSelect={(file, blobUrl) => onFileSelect("insuranceDocuments", file, blobUrl)} />
            </div>
        </div>
    );
}

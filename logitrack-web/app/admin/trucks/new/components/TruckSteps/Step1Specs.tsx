"use client";

import { useFormContext } from "react-hook-form";
import { TruckFormValues } from "@/validate/truckSchema";
import { IdentificationSection } from "../IdentificationSection";
import { VehicleDetailsSection } from "../VehicleDetailsSection";
import { PhotosSection } from "../PhotosSection";

interface Step1Props {
    onFileSelect: (field: string, file: File, blobUrl: string) => void;
}

import { useLanguage } from "@/context/language";

export function Step1Specs({ onFileSelect }: Step1Props) {
    const { t } = useLanguage();

    return (
        <div className="space-y-6">
            <div className="bg-card border rounded-lg p-6 animate-in fade-in slide-in-from-bottom-4 duration-500">
                <h3 className="text-lg font-medium mb-4">{t("trucks.step1.title")}</h3>
                <div className="space-y-6">
                    <IdentificationSection />
                    <VehicleDetailsSection />
                </div>
            </div>

            {/* Vehicle Appearance / Photos */}
            <div className="bg-card border rounded-lg p-6 animate-in fade-in slide-in-from-bottom-5 duration-500 delay-100">
                <h3 className="text-sm font-semibold text-muted-foreground uppercase tracking-wider mb-4">{t("trucks.step1.appearance")}</h3>
                <PhotosSection onFileSelect={onFileSelect} />
            </div>
        </div>
    );
}

//test husky auto update vibe-rules.md-6

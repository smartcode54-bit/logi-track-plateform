"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { useForm, FormProvider } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { ArrowLeft, CheckCircle2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
import { useAuth } from "@/context/auth";
import { truckSchema, TruckFormValues, truckDefaultValues } from "@/validate/truckSchema";
import { createInvalidHandler } from "@/lib/formInvalidHandler";
import { useTruckWrites } from "../../api/useTrucks";
import { TRUCK_FIELD_MAP } from "../../api/trucks";
import { applyServerFieldErrors, toastSaveError } from "@/lib/formServerErrors";

// Import Steps from local sub-folder
import { Step1Specs } from "./TruckSteps/Step1Specs";
import { Step2Compliance } from "./TruckSteps/Step2Compliance";
import { Step3Maintenance } from "./TruckSteps/Step3Maintenance";
import { Step4Documentation } from "./TruckSteps/Step4Documentation";

const STEPS = [
    { id: 1, titleKey: "trucks.wizard.step1.title", descKey: "trucks.wizard.step1.desc" },
    { id: 2, titleKey: "trucks.wizard.step2.title", descKey: "trucks.wizard.step2.desc" },
    { id: 3, titleKey: "trucks.wizard.step3.title", descKey: "trucks.wizard.step3.desc" },
    { id: 4, titleKey: "trucks.wizard.step4.title", descKey: "trucks.wizard.step4.desc" },
];

export default function NewTruckWizard() {
    const router = useRouter();
    const { t } = useLanguage();
    const authContext = useAuth();
    const currentUser = authContext?.currentUser ?? null;

    const [currentStep, setCurrentStep] = useState(1);
    const { create } = useTruckWrites();
    const isSubmitting = create.isPending;
    const [filesToUpload] = useState<Map<string, File>>(() => new Map());

    // Initialize Form
    const methods = useForm<TruckFormValues>({
        resolver: zodResolver(truckSchema) as any,
        defaultValues: truckDefaultValues,
        mode: "onChange",
    });

    const { trigger, handleSubmit, watch } = methods;
    const ownershipType = watch("ownershipType");

    // Handle File Selection (Passed down to steps)
    const handleFileSelect = (fieldOrFile: string | File, fileOrBlob: File | string, blobUrl?: string) => {
        if (typeof fieldOrFile === 'string' && blobUrl) {
            filesToUpload.set(blobUrl, fileOrBlob as File);
        } else if (fieldOrFile instanceof File && typeof fileOrBlob === 'string') {
            filesToUpload.set(fileOrBlob, fieldOrFile);
        }
    };

    // Navigation Handlers
    const nextStep = async () => {
        let fieldsToValidate: (keyof TruckFormValues)[] = [];

        if (currentStep === 1) {
            fieldsToValidate = [
                "licensePlate", "province", "vin", "engineNumber", "truckStatus",
                "brand", "model", "year", "color", "type", "seats",
                "ownershipType"
            ];
            if (ownershipType === 'subcontractor') fieldsToValidate.push("subcontractorId");
        } else if (currentStep === 2 && ownershipType === 'own') {
            fieldsToValidate = ["documentTax", "documentRegister"];
        } else if (currentStep === 3 && ownershipType === 'own') {
            fieldsToValidate = [
                "fuelType", "engineCapacity", "fuelCapacity", "maxLoadWeight",
                "currentMileage", "nextServiceMileage", "pmIntervalKm",
            ];
        }

        const isValid = await trigger(fieldsToValidate);
        if (isValid) {
            setCurrentStep((prev) => Math.min(prev + 1, STEPS.length));
            window.scrollTo({ top: 0, behavior: "smooth" });
        }
    };

    const prevStep = () => {
        setCurrentStep((prev) => Math.max(prev - 1, 1));
        window.scrollTo({ top: 0, behavior: "smooth" });
    };

    const onSubmit = async (data: TruckFormValues) => {
        try {
            if (!currentUser) throw new Error(t("masterData.notSignedIn"));
            // Go: `POST /v1/trucks`, then the picked files (their keys embed the new truck) in a
            // `PATCH`; Firestore: the legacy uploads and document (T25).
            await create.mutateAsync({ values: data, pending: filesToUpload, userId: currentUser.uid });
            router.push("/app/trucks");
        } catch (error) {
            if (applyServerFieldErrors(methods, error, t, TRUCK_FIELD_MAP) > 0) setCurrentStep(1);
            toastSaveError(error, t, "trucks.edit.error");
        }
    };

    return (
        <FormProvider {...methods}>
            <div className="flex flex-col gap-section">
                <div className="sticky top-16 z-raised border-b bg-background pb-4">
                    <PageHeader
                        title={t("nav.trucks")}
                        recordName={t("trucks.wizard.title")}
                        description={t("trucks.wizard.subtitle")
                            .replace("{step}", currentStep.toString())
                            .replace("{total}", STEPS.length.toString())}
                        back={{ href: "/app/trucks" }}
                    />
                </div>

                <div className="flex min-w-0 flex-1">
                    <div className="w-64 border-r bg-muted/30 p-6 hidden md:block overflow-y-auto">
                        <div className="space-y-6">
                            {STEPS.map((step) => {
                                const isActive = step.id === currentStep;
                                const isCompleted = step.id < currentStep;

                                return (
                                    <div key={step.id} className={`flex items-start gap-3 relative ${isActive ? "opacity-100" : "opacity-60"}`}>
                                        <div className={`
                                            flex items-center justify-center w-8 h-8 rounded-full border-2 text-sm font-semibold z-10 bg-background
                                            ${isActive ? "border-primary text-primary" : ""}
                                            ${isCompleted ? "border-primary bg-primary text-primary-foreground" : "border-muted-foreground/30 text-muted-foreground"}
                                        `}>
                                            {isCompleted ? <CheckCircle2 className="h-5 w-5" /> : step.id}
                                        </div>
                                        {step.id !== STEPS.length && (
                                            <div className="absolute left-4 top-8 bottom-[-24px] w-[2px] bg-border" />
                                        )}
                                        <div className="pt-1">
                                            <p className={`text-sm font-medium ${isActive ? "text-primary" : ""}`}>{t(step.titleKey)}</p>
                                            <p className="text-xs text-muted-foreground">{t(step.descKey)}</p>
                                        </div>
                                    </div>
                                );
                            })}
                        </div>

                        <div className="mt-8 p-4 bg-primary/10 border border-border rounded-lg">
                            <div className="flex items-center gap-2 mb-2 text-primary">
                                <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" className="w-4 h-4">
                                    <path fillRule="evenodd" d="M2.25 12c0-5.385 4.365-9.75 9.75-9.75s9.75 4.365 9.75 9.75-4.365 9.75-9.75 9.75S2.25 17.385 2.25 12zm8.706-1.442c1.146-.573 2.437.463 2.126 1.706l-.709 2.836.042-.02a.75.75 0 01.67 1.34l-.04.022c-1.147.573-2.438-.463-2.127-1.706l.71-2.836-.042.02a.75.75 0 11-.671-1.34l.041-.022zM12 9a.75.75 0 100-1.5.75.75 0 000 1.5z" clipRule="evenodd" />
                                </svg>
                                <span className="text-xs font-bold">{t("trucks.wizard.tips")}</span>
                            </div>
                            <p className="text-xs text-muted-foreground leading-relaxed">
                                {t("trucks.wizard.tips.vin")}
                            </p>
                        </div>
                    </div>

                    <div className="min-w-0 flex-1 p-4 md:p-8">
                        <div className="max-w-3xl mx-auto pb-8">
                            <form onSubmit={handleSubmit(onSubmit as any, createInvalidHandler(methods, t))}>
                                {currentStep === 1 && <Step1Specs onFileSelect={handleFileSelect} />}
                                {currentStep === 2 && <Step2Compliance onFileSelect={handleFileSelect} />}
                                {currentStep === 3 && <Step3Maintenance />}
                                {currentStep === 4 && <Step4Documentation />}
                            </form>
                        </div>

                        <div data-form-action-bar className="sticky bottom-0 z-sticky flex flex-col gap-3 border-t border-border bg-background p-4 md:flex-row md:items-center md:justify-between">
                            <Button
                                type="button"
                                variant="outline"
                                onClick={currentStep === 1 ? () => router.push('/app/trucks') : prevStep}
                            >
                                {currentStep === 1 ? t("trucks.wizard.cancel") : t("trucks.wizard.back")}
                            </Button>

                            <Button
                                type="button"
                                className="min-w-[140px]"
                                onClick={currentStep === 4 ? handleSubmit(onSubmit as any, createInvalidHandler(methods, t)) : nextStep}
                                disabled={isSubmitting}
                            >
                                {isSubmitting ? t("trucks.wizard.saving") : currentStep === 4 ? t("trucks.wizard.save") : t("trucks.wizard.next")}
                                {currentStep !== 4 && <ArrowLeft className="ml-2 h-4 w-4 rotate-180" />}
                            </Button>
                        </div>
                    </div>
                </div>
            </div>
        </FormProvider>
    );
}

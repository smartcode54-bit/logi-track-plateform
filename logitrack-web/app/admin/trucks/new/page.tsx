"use client";

import { useState, useEffect } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useForm, FormProvider } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { ArrowLeft, CheckCircle2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useLanguage } from "@/context/language";
import { useAuth } from "@/context/auth";
import { truckSchema, TruckFormValues, truckDefaultValues } from "@/validate/truckSchema";
import { uploadTruckFile, saveNewTruckToFirestoreClient } from "./action.client";
import { getSubcontractors } from "../../subcontractors/actions.client";

// Import Steps
import { Step1Specs } from "./components/TruckSteps/Step1Specs";
import { Step2Compliance } from "./components/TruckSteps/Step2Compliance";
import { Step3Maintenance } from "./components/TruckSteps/Step3Maintenance";
import { Step4Documentation } from "./components/TruckSteps/Step4Documentation";

const STEPS = [
    { id: 1, title: "Vehicle Specs", description: "Make, Model, VIN" },
    { id: 2, title: "Compliance", description: "Registration & Insurance" },
    { id: 3, title: "Maintenance", description: "Engine & Status" },
    { id: 4, title: "Documentation", description: "Review & Save" },
];

export default function CreateTruckPage() {
    const router = useRouter();
    const { t } = useLanguage();
    const authContext = useAuth();
    const currentUser = authContext?.currentUser ?? null;

    const [currentStep, setCurrentStep] = useState(1);
    const [isSubmitting, setIsSubmitting] = useState(false);
    const [subcontractors, setSubcontractors] = useState<any[]>([]);
    const [filesToUpload] = useState<Map<string, File>>(() => new Map());

    // Load subcontractors
    useEffect(() => {
        getSubcontractors().then(setSubcontractors);
    }, []);

    // Initialize Form
    const methods = useForm<TruckFormValues>({
        resolver: zodResolver(truckSchema) as any,
        defaultValues: truckDefaultValues,
        mode: "onChange", // Enable validation on change for better UX in wizard
    });

    const { trigger, handleSubmit, watch, setValue } = methods;
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

        // Validate fields based on current step
        if (currentStep === 1) {
            // Validate all Identification and Vehicle Detail fields
            fieldsToValidate = [
                "licensePlate", "province", "vin", "engineNumber", "truckStatus", // From IdentificationSection
                "brand", "model", "year", "color", "type", "seats", // From VehicleDetailsSection
                "ownershipType"
            ];
            if (ownershipType === 'subcontractor') fieldsToValidate.push("subcontractorId");
        } else if (currentStep === 2 && ownershipType === 'own') {
            // Only validate strict compliance for owned trucks
            fieldsToValidate = ["documentTax", "documentRegister"];
        } else if (currentStep === 3 && ownershipType === 'own') {
            fieldsToValidate = ["fuelType", "engineCapacity", "fuelCapacity", "maxLoadWeight"]; // From EngineInformationSection
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

    // Final Submission
    const onSubmit = async (data: TruckFormValues) => {
        setIsSubmitting(true);
        try {
            if (!currentUser) throw new Error("User not authenticated");

            // Clone data
            const finalData = { ...data };

            // Helper to upload
            const uploadIfNeeded = async (blobUrl: string | undefined | null, pathPrefix: string): Promise<string | undefined> => {
                if (!blobUrl || !blobUrl.startsWith("blob:")) return blobUrl || undefined;
                const file = filesToUpload.get(blobUrl);
                if (!file) return undefined;
                return await uploadTruckFile(file, `trucks/${pathPrefix}/${Date.now()}_${file.name}`);
            };

            // Upload Images
            const imageFields = ['imageFrontRight', 'imageFrontLeft', 'imageBackRight', 'imageBackLeft'] as const;
            for (const field of imageFields) {
                const url = await uploadIfNeeded(finalData[field], `photos/${field.replace('image', '').toLowerCase()}`);
                if (url) (finalData as any)[field] = url;
            }

            // Upload Docs
            if (finalData.documentTax) {
                const url = await uploadIfNeeded(finalData.documentTax, "documents/tax");
                if (url) finalData.documentTax = url;
            }
            if (finalData.documentRegister) {
                const url = await uploadIfNeeded(finalData.documentRegister, "documents/register");
                if (url) finalData.documentRegister = url;
            }
            if (finalData.insuranceDocuments && finalData.insuranceDocuments.length > 0) {
                const newDocs: string[] = [];
                for (const doc of finalData.insuranceDocuments) {
                    const url = await uploadIfNeeded(doc, "insurance");
                    if (url) newDocs.push(url);
                }
                finalData.insuranceDocuments = newDocs;
            }

            await saveNewTruckToFirestoreClient(finalData as any, currentUser.uid);
            router.push("/admin/trucks");

        } catch (error) {
            console.error(error);
            setIsSubmitting(false);
        }
    };

    return (
        <FormProvider {...methods}>
            <div className="min-h-screen bg-background text-foreground flex flex-col">
                {/* Header */}
                <div className="flex items-center px-8 py-6 border-b bg-background sticky top-0 z-10">
                    <Button variant="ghost" size="icon" asChild className="mr-4">
                        <Link href="/admin/trucks">
                            <ArrowLeft className="h-5 w-5" />
                        </Link>
                    </Button>
                    <div>
                        <h1 className="text-2xl font-bold">Truck Details Form</h1>
                        <p className="text-muted-foreground text-sm">Step {currentStep} of {STEPS.length}: Please enter the vehicle information below.</p>
                    </div>
                </div>

                <div className="flex flex-1 overflow-hidden">
                    {/* Sidebar Steps */}
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
                                            <p className={`text-sm font-medium ${isActive ? "text-primary" : ""}`}>{step.title}</p>
                                            <p className="text-xs text-muted-foreground">{step.description}</p>
                                        </div>
                                    </div>
                                );
                            })}
                        </div>

                        {/* Tips Card */}
                        <div className="mt-8 p-4 bg-blue-950/20 border border-blue-900/30 rounded-lg">
                            <div className="flex items-center gap-2 mb-2 text-blue-400">
                                <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" className="w-4 h-4">
                                    <path fillRule="evenodd" d="M2.25 12c0-5.385 4.365-9.75 9.75-9.75s9.75 4.365 9.75 9.75-4.365 9.75-9.75 9.75S2.25 17.385 2.25 12zm8.706-1.442c1.146-.573 2.437.463 2.126 1.706l-.709 2.836.042-.02a.75.75 0 01.67 1.34l-.04.022c-1.147.573-2.438-.463-2.127-1.706l.71-2.836-.042.02a.75.75 0 11-.671-1.34l.041-.022zM12 9a.75.75 0 100-1.5.75.75 0 000 1.5z" clipRule="evenodd" />
                                </svg>
                                <span className="text-xs font-bold uppercase tracking-wider">Tips</span>
                            </div>
                            <p className="text-xs text-muted-foreground leading-relaxed">
                                Ensure the VIN matches the chassis engraving exactly for registration compliance.
                            </p>
                        </div>
                    </div>

                    {/* Main Form Area */}
                    <div className="flex-1 overflow-y-auto p-8 relative">
                        <div className="max-w-3xl mx-auto pb-24">
                            <form onSubmit={handleSubmit(onSubmit as any)}>
                                {currentStep === 1 && <Step1Specs onFileSelect={handleFileSelect} />}
                                {currentStep === 2 && <Step2Compliance onFileSelect={handleFileSelect} />}
                                {currentStep === 3 && <Step3Maintenance />}
                                {currentStep === 4 && <Step4Documentation />}
                            </form>
                        </div>

                        {/* Sticky Footer for Navigation */}
                        <div className="fixed bottom-0 right-0 left-0 md:left-64 p-4 border-t bg-background/80 backdrop-blur-sm flex justify-between items-center z-20">
                            <Button
                                type="button"
                                variant="outline"
                                onClick={currentStep === 1 ? () => router.push('/admin/trucks') : prevStep}
                            >
                                {currentStep === 1 ? "Cancel" : "Back"}
                            </Button>

                            <Button
                                type="button"
                                className="bg-blue-600 hover:bg-blue-700 text-white min-w-[140px]"
                                onClick={currentStep === 4 ? handleSubmit(onSubmit as any) : nextStep}
                                disabled={isSubmitting}
                            >
                                {isSubmitting ? "Saving..." : currentStep === 4 ? "Save and Continue" : "Next Step"}
                                {currentStep !== 4 && <ArrowLeft className="ml-2 h-4 w-4 rotate-180" />}
                            </Button>
                        </div>
                    </div>
                </div>
            </div>
        </FormProvider>
    );
}

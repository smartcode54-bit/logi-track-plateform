"use client";

import { useState, useEffect } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { Truck, Home, LayoutDashboard, Save, ArrowLeft, Loader2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
    Breadcrumb,
    BreadcrumbItem,
    BreadcrumbLink,
    BreadcrumbList,
    BreadcrumbPage,
    BreadcrumbSeparator,
} from "@/components/ui/breadcrumb";
import {
    Form,
    FormControl,
    FormField,
    FormItem,
    FormLabel,
    FormMessage,
} from "@/components/ui/form";
import { useLanguage } from "@/context/language";
import { useAuth } from "@/context/auth";
import { truckSchema, TruckFormValues, TruckValidatedData } from "@/validate/truckSchema";

import { IdentificationSection } from "../new/components/IdentificationSection";
import { VehicleDetailsSection } from "../new/components/VehicleDetailsSection";
import { RegistrationSection } from "../new/components/RegistrationSection";
import { EngineInformationSection } from "../new/components/EngineCapacitySection";
import { MaintenanceSection } from "../new/components/MaintenanceSection";
import { PhotosSection } from "../new/components/PhotosSection";
import { InsuranceSection } from "../new/components/InsuranceSection";
import { updateTruckInFirestoreClient, uploadTruckFile } from "../new/action.client";
import { getTruckByIdClient } from "../actions.client";
import { getSubcontractors } from "../../subcontractors/actions.client";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

// Subcontractor Selector Component
function SubcontractorSelector({ value, onChange }: { value?: string, onChange: (val: string) => void }) {
    const [subs, setSubs] = useState<any[]>([]);

    useEffect(() => {
        getSubcontractors().then(setSubs);
    }, []);

    return (
        <Select onValueChange={onChange} value={value || ""}>
            <FormControl>
                <SelectTrigger>
                    <SelectValue placeholder="Select a subcontractor" />
                </SelectTrigger>
            </FormControl>
            <SelectContent>
                {subs.map(sub => (
                    <SelectItem key={sub.id} value={sub.id}>{sub.name}</SelectItem>
                ))}
            </SelectContent>
        </Select>
    );
}

export default function EditTruckClient() {
    const router = useRouter();
    const searchParams = useSearchParams();
    const truckId = searchParams.get("id") as string;

    const [isSubmitting, setIsSubmitting] = useState(false);
    const [isLoading, setIsLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);

    // Store files to be uploaded: Key = Blob URL, Value = File object
    const [filesToUpload] = useState<Map<string, File>>(() => new Map());

    const { t } = useLanguage();
    const authContext = useAuth();
    const currentUser = authContext?.currentUser ?? null;

    // Initialize form
    const form = useForm<TruckFormValues>({
        resolver: zodResolver(truckSchema) as any,
        defaultValues: {
            // Default values will be reset when data is loaded
            // isActive: true, // Removed as it is not in schema
            ownershipType: "own",
            subcontractorId: "",
            licensePlate: "",
            province: "",
            brand: "",
            model: "",
            year: String(new Date().getFullYear()),
            color: "",
            type: "",
            // headType: "", // Removed as it is not in schema
            fuelType: "Diesel",
            engineCapacity: 0,
            fuelCapacity: 0,
            maxLoadWeight: 0,
            seats: "4",
            // mileage: 0, // Removed as it is not in schema
            registrationDate: undefined,
            buyingDate: undefined,
            engineNumber: "",
            // chassisNumber: "", // Removed as it is not in schema
            imageFrontRight: "",
            imageFrontLeft: "",
            imageBackRight: "",
            imageBackLeft: "",
            documentTax: "",
            documentRegister: "",
            insuranceDocuments: [],
        },
    });

    // Fetch truck data
    useEffect(() => {
        const fetchTruck = async () => {
            try {
                // If user is not yet loaded, wait. 
                // However, we can fetch public data or generic data if auth not needed for fetch.
                // Assuming we need auth to fetch truck details? 
                // For now, let's fetch.

                const truckData = await getTruckByIdClient(truckId);

                if (truckData) {
                    // Transform dates from Firestore Timestamps to Date objects if necessary
                    // The getTruckByIdClient likely returns data in a format close to what we need.
                    // We might need to handle Timestamp conversion if not done in client action.
                    // Assuming client action returns plain objects or we handle it here.

                    // Cast/Transform to form values
                    const formValues: any = {
                        ...truckData,
                        // Ensure dates are valid strings or undefined
                        // If they are Firestore Timestamps, they handle conversion in actions.client.ts
                        // We rely on getTruckByIdClient returning strings for dates.
                        year: String(truckData.year || ""),
                        seats: String(truckData.seats || ""),
                        registrationDate: truckData.registrationDate || undefined,
                        buyingDate: truckData.buyingDate || undefined,
                        taxExpiryDate: truckData.taxExpiryDate || undefined,
                        lastServiceDate: truckData.lastServiceDate || undefined,
                        nextServiceDate: truckData.nextServiceDate || undefined,
                        insuranceStartDate: truckData.insuranceStartDate || undefined,
                        insuranceExpiryDate: truckData.insuranceExpiryDate || undefined,

                        // Handle renewal status: if empty string, set to undefined to pass Zod validation
                        taxRenewalStatus: (truckData.taxRenewalStatus as any) === "" ? undefined : truckData.taxRenewalStatus,
                        insuranceRenewalStatus: (truckData.insuranceRenewalStatus as any) === "" ? undefined : truckData.insuranceRenewalStatus,
                    };

                    form.reset(formValues);
                } else {
                    setError("Truck not found");
                }
            } catch (err) {
                console.error("Error fetching truck:", err);
                setError("Failed to load truck data");
            } finally {
                setIsLoading(false);
            }
        };

        fetchTruck();
    }, [truckId, form]);

    const handleFileSelect = (fieldOrFile: string | File, fileOrBlob: File | string, blobUrl?: string) => {
        if (typeof fieldOrFile === 'string' && blobUrl) {
            // From PhotosSection
            filesToUpload.set(blobUrl, fileOrBlob as File);
        } else if (fieldOrFile instanceof File && typeof fileOrBlob === 'string') {
            // From InsuranceSection
            filesToUpload.set(fileOrBlob, fieldOrFile);
        }
    };

    // Form submission handler
    const onSubmit = async (data: TruckFormValues) => {
        console.log("Submitting form with data:", data);
        setIsSubmitting(true);
        setError(null);

        try {
            if (!currentUser) {
                throw new Error("User not authenticated");
            }

            // Clone data for modification
            const finalData = { ...data };

            // Helper to upload if blob exists
            const uploadIfNeeded = async (blobUrl: string | undefined | null, pathPrefix: string): Promise<string | undefined> => {
                if (!blobUrl || !blobUrl.startsWith("blob:")) return blobUrl || undefined;

                const file = filesToUpload.get(blobUrl);
                if (!file) {
                    console.warn(`File not found for blob URL: ${blobUrl}`);
                    return undefined;
                }

                return await uploadTruckFile(file, `trucks/${pathPrefix}/${Date.now()}_${file.name}`);
            };

            // 1. Upload Standard Images
            const imageFields = ['imageFrontRight', 'imageFrontLeft', 'imageBackRight', 'imageBackLeft'] as const;
            for (const field of imageFields) {
                const url = await uploadIfNeeded(finalData[field], `photos/${field.replace('image', '').toLowerCase()}`);
                if (url) (finalData as any)[field] = url;
            }

            // 2. Upload Standard Documents
            if (finalData.documentTax) {
                const url = await uploadIfNeeded(finalData.documentTax, "documents/tax");
                if (url) finalData.documentTax = url;
            }
            if (finalData.documentRegister) {
                const url = await uploadIfNeeded(finalData.documentRegister, "documents/register");
                if (url) finalData.documentRegister = url;
            }

            // 3. Upload Insurance Documents
            if (finalData.insuranceDocuments && finalData.insuranceDocuments.length > 0) {
                const newDocs: string[] = [];
                for (const doc of finalData.insuranceDocuments) {
                    const url = await uploadIfNeeded(doc, "insurance");
                    if (url) newDocs.push(url);
                }
                finalData.insuranceDocuments = newDocs;
            }

            // Exclude renewal status fields from the update to prevent overwriting concurrent changes
            // e.g. if someone is renewing while this form is open
            const {
                taxRenewalStatus,
                insuranceRenewalStatus,
                taxExpense,
                taxReceipt,
                insuranceReceipt,
                ...dataToUpdate
            } = finalData;

            await updateTruckInFirestoreClient(truckId, dataToUpdate as TruckValidatedData, currentUser.uid);

            toast.success("Truck updated successfully");

            // wait for a bit to show the toast
            setTimeout(() => {
                router.push(`/admin/trucks/view?id=${truckId}`);
            }, 1000);

        } catch (error) {
            console.error("Error updating truck:", error);
            const errorMessage = error instanceof Error
                ? error.message
                : "Failed to update truck. Please try again.";
            setError(errorMessage);
        } finally {
            setIsSubmitting(false);
        }
    };

    if (isLoading) {
        return (
            <div className="flex items-center justify-center min-h-[400px]">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                <span className="ml-2 text-muted-foreground">Loading truck data...</span>
            </div>
        );
    }

    return (
        <div className="container mx-auto px-4 py-8">
            <div className="max-w-4xl mx-auto">
                {/* ... Breadcrumb & Header ... */}

                <div className="flex items-center justify-between mb-8">
                    <div>
                        <h1 className="text-3xl font-bold text-foreground">
                            Edit Truck
                        </h1>
                        <p className="text-muted-foreground mt-1">
                            Update truck information
                        </p>
                    </div>
                    <Button variant="outline" asChild>
                        <Link href={`/admin/trucks/view?id=${truckId}`} className="flex items-center gap-2">
                            <ArrowLeft className="h-4 w-4" />
                            Back to Details
                        </Link>
                    </Button>
                </div>

                {/* Form */}
                <Form {...form}>
                    <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-6">
                        {error && (
                            <div className="bg-destructive/15 text-destructive px-4 py-3 rounded-md border border-destructive/50">
                                <p className="text-sm font-medium">{error}</p>
                            </div>
                        )}
                        {/* Ownership Type Selection */}
                        <div className="bg-card border rounded-lg p-6">
                            {/* ... Ownership Fields ... */}
                            <h3 className="text-lg font-medium mb-4">Ownership</h3>
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                                <FormField
                                    control={form.control}
                                    name="ownershipType"
                                    render={({ field }) => (
                                        <FormItem className="space-y-3">
                                            <FormLabel>Type</FormLabel>
                                            <FormControl>
                                                <div className="flex gap-4">
                                                    <Button
                                                        type="button"
                                                        variant={field.value === "own" ? "default" : "outline"}
                                                        onClick={() => {
                                                            field.onChange("own");
                                                            form.setValue("subcontractorId", ""); // Clear sub ID
                                                        }}
                                                        className="flex-1"
                                                    >
                                                        Own Fleet
                                                    </Button>
                                                    <Button
                                                        type="button"
                                                        variant={field.value === "subcontractor" ? "default" : "outline"}
                                                        onClick={() => field.onChange("subcontractor")}
                                                        className="flex-1"
                                                    >
                                                        Subcontractor
                                                    </Button>
                                                </div>
                                            </FormControl>
                                            <FormMessage />
                                        </FormItem>
                                    )}
                                />
                                {form.watch("ownershipType") === "subcontractor" && (
                                    <FormField
                                        control={form.control}
                                        name="subcontractorId"
                                        render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>Subcontractor (Required)</FormLabel>
                                                <SubcontractorSelector
                                                    value={field.value}
                                                    onChange={field.onChange}
                                                />
                                                <FormMessage />
                                            </FormItem>
                                        )}
                                    />
                                )}
                            </div>
                        </div>

                        <IdentificationSection />
                        <VehicleDetailsSection />
                        <EngineInformationSection />
                        <MaintenanceSection />
                        <RegistrationSection />
                        <InsuranceSection onFileSelect={handleFileSelect} />
                        <PhotosSection onFileSelect={handleFileSelect} />

                        <div className="flex justify-end gap-4">
                            <Button type="button" variant="outline" asChild>
                                <Link href={`/admin/trucks/view?id=${truckId}`}>Cancel</Link>
                            </Button>
                            <Button
                                type="submit"
                                className="flex items-center gap-2"
                                disabled={isSubmitting}
                            >
                                {isSubmitting ? (
                                    <>
                                        <Loader2 className="h-4 w-4 animate-spin" />
                                        Saving...
                                    </>
                                ) : (
                                    <>
                                        <Save className="h-4 w-4" />
                                        Save Changes
                                    </>
                                )}
                            </Button>
                        </div>
                    </form>
                </Form>
            </div>
        </div>
    );
}

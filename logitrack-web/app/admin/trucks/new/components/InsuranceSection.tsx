"use client";

import {
    FormControl,
    FormField,
    FormItem,
    FormLabel,
    FormMessage,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { DatePicker } from "@/components/ui/date-picker";
import { format } from "date-fns";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { useFormContext } from "react-hook-form";
import { ShieldCheck, Upload, X, FileText, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useState } from "react";
import { ref, uploadBytes, getDownloadURL } from "firebase/storage";
import { storage } from "@/firebase/client";
import Link from "next/link";

interface InsuranceSectionProps {
    onFileSelect?: (file: File, blobUrl: string) => void;
}

export function InsuranceSection({ onFileSelect }: InsuranceSectionProps) {
    const { control, watch, setValue } = useFormContext();
    const [isUploading, setIsUploading] = useState(false);

    // Watch documents
    const insuranceDocuments = watch("insuranceDocuments") || [];

    const handleFileChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
        if (!e.target.files || e.target.files.length === 0) return;

        const file = e.target.files[0];

        // Max 5MB
        if (file.size > 5 * 1024 * 1024) {
            alert("File size must be less than 5MB");
            return;
        }

        // If onFileSelect is provided, skip immediate upload
        if (onFileSelect) {
            const blobUrl = URL.createObjectURL(file);
            // Append to array locally
            onFileSelect(file, blobUrl);
            setValue("insuranceDocuments", [...insuranceDocuments, blobUrl], {
                shouldValidate: true,
                shouldDirty: true,
                shouldTouch: true
            });
            e.target.value = "";
            return;
        }

        try {
            setIsUploading(true);
            // trucks/insurance/{timestamp}_{filename}
            const filename = `insurance_${Date.now()}_${file.name.replace(/[^a-zA-Z0-9.]/g, "_")}`;
            const storageRef = ref(storage, `trucks/insurance/${filename}`);

            const snapshot = await uploadBytes(storageRef, file);
            const url = await getDownloadURL(snapshot.ref);

            setValue("insuranceDocuments", [...insuranceDocuments, url], {
                shouldValidate: true,
                shouldDirty: true,
                shouldTouch: true
            });

        } catch (error) {
            console.error("Error uploading document:", error);
            alert("Failed to upload document");
        } finally {
            setIsUploading(false);
            e.target.value = "";
        }
    };

    const removeDocument = (indexToRemove: number) => {
        const newDocs = insuranceDocuments.filter((_: any, index: number) => index !== indexToRemove);
        setValue("insuranceDocuments", newDocs, {
            shouldValidate: true,
            shouldDirty: true,
            shouldTouch: true
        });
    };

    return (
        <Card>
            <CardHeader>
                <CardTitle className="flex items-center gap-2">
                    <ShieldCheck className="h-5 w-5 text-primary" />
                    Insurance Information
                </CardTitle>
            </CardHeader>
            <CardContent className="space-y-6">
                {/* ID and Basic Info */}
                <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                    <FormField
                        control={control}
                        name="insurancePolicyId"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>Policy ID (Internal)</FormLabel>
                                <FormControl>
                                    <Input placeholder="e.g., INS001" {...field} />
                                </FormControl>
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                    <FormField
                        control={control}
                        name="insurancePolicyNumber"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>Policy Number</FormLabel>
                                <FormControl>
                                    <Input placeholder="e.g., VR123456789" {...field} />
                                </FormControl>
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                </div>

                <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
                    <FormField
                        control={control}
                        name="insuranceCompany"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>Provider</FormLabel>
                                <FormControl>
                                    <Input placeholder="e.g., Viriyah Insurance" {...field} />
                                </FormControl>
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                    <FormField
                        control={control}
                        name="insuranceType"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>Coverage Type</FormLabel>
                                <Select onValueChange={field.onChange} value={field.value || ""}>
                                    <FormControl>
                                        <SelectTrigger>
                                            <SelectValue placeholder="Select type" />
                                        </SelectTrigger>
                                    </FormControl>
                                    <SelectContent>
                                        <SelectItem value="1">ประกันชั้น 1</SelectItem>
                                        <SelectItem value="2">ประกันชั้น 2</SelectItem>
                                        <SelectItem value="2+">ประกันชั้น 2+</SelectItem>
                                        <SelectItem value="3">ประกันชั้น 3</SelectItem>
                                        <SelectItem value="3+">ประกันชั้น 3+</SelectItem>
                                    </SelectContent>
                                </Select>
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                    <FormField
                        control={control}
                        name="insurancePremium"
                        render={({ field }) => (
                            <FormItem>
                                <FormLabel>Premium (Baht)</FormLabel>
                                <FormControl>
                                    <Input
                                        type="number"
                                        placeholder="e.g., 14500"
                                        {...field}
                                        onChange={(e) => {
                                            const val = e.target.value === "" ? undefined : Number(e.target.value);
                                            field.onChange(val);
                                        }}
                                        value={field.value ?? ""}
                                    />
                                </FormControl>
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                </div>

                <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                    <FormField
                        control={control}
                        name="insuranceStartDate"
                        render={({ field }) => (
                            <FormItem className="flex flex-col">
                                <FormLabel>Start Date</FormLabel>
                                <DatePicker
                                    value={field.value ? new Date(field.value) : undefined}
                                    onChange={(date) => field.onChange(date ? format(date, "yyyy-MM-dd") : "")}
                                    fromYear={new Date().getFullYear() - 5}
                                    toYear={new Date().getFullYear() + 5}
                                />
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                    <FormField
                        control={control}
                        name="insuranceExpiryDate"
                        render={({ field }) => (
                            <FormItem className="flex flex-col">
                                <FormLabel>End Date</FormLabel>
                                <DatePicker
                                    value={field.value ? new Date(field.value) : undefined}
                                    onChange={(date) => field.onChange(date ? format(date, "yyyy-MM-dd") : "")}
                                    fromYear={new Date().getFullYear()}
                                    toYear={new Date().getFullYear() + 10}
                                />
                                <FormMessage />
                            </FormItem>
                        )}
                    />
                </div>

                <FormField
                    control={control}
                    name="insuranceNotes"
                    render={({ field }) => (
                        <FormItem>
                            <FormLabel>Notes</FormLabel>
                            <FormControl>
                                <Textarea placeholder="e.g., Repairs at authorized garages only" {...field} />
                            </FormControl>
                            <FormMessage />
                        </FormItem>
                    )}
                />

                {/* Documents Upload */}
                <div className="space-y-4">
                    <FormLabel>Insurance Documents</FormLabel>
                    <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                        {insuranceDocuments.map((docUrl: string, index: number) => (
                            <div key={index} className="relative aspect-[3/4] rounded-md overflow-hidden border bg-muted flex flex-col items-center justify-center p-2 group">
                                <FileText className="h-8 w-8 text-primary mb-2" />
                                <Link
                                    href={docUrl}
                                    target="_blank"
                                    className="text-xs text-center text-primary hover:underline break-all px-2 line-clamp-2"
                                >
                                    Document {index + 1}
                                </Link>
                                <div className="absolute inset-0 bg-black/40 opacity-0 group-hover:opacity-100 transition-opacity flex items-center justify-center">
                                    <Button
                                        variant="destructive"
                                        size="icon"
                                        type="button"
                                        onClick={() => removeDocument(index)}
                                    >
                                        <X className="h-4 w-4" />
                                    </Button>
                                </div>
                            </div>
                        ))}

                        <div className="relative aspect-[3/4] flex flex-col items-center justify-center border-2 border-dashed rounded-md hover:bg-muted/50 transition-colors cursor-pointer">
                            <input
                                type="file"
                                accept=".pdf,image/*"
                                onChange={handleFileChange}
                                disabled={isUploading}
                                className="absolute inset-0 w-full h-full opacity-0 cursor-pointer disabled:cursor-not-allowed"
                            />
                            {isUploading ? (
                                <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                            ) : (
                                <>
                                    <Upload className="h-6 w-6 text-muted-foreground mb-2" />
                                    <span className="text-xs font-medium text-muted-foreground">Upload PDF/Img</span>
                                </>
                            )}
                        </div>
                    </div>
                </div>

            </CardContent>
        </Card>
    );
}

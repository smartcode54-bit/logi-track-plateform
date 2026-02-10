"use client";

import { ChangeEvent, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Loader2, Upload, FileText, X, Image as ImageIcon } from "lucide-react";
import { uploadSubcontractorFile } from "../actions.client";
import { toast } from "sonner";
import Image from "next/image";

interface FileUploaderProps {
    label: string;
    folder: string; // e.g., "id_cards" or "company_docs"
    onUploadComplete: (url: string) => void;
    currentUrls?: string[];
    onRemove?: (url: string) => void;
    multiple?: boolean;
}

export default function FileUploader({ label, folder, onUploadComplete, currentUrls = [], onRemove, multiple = false }: FileUploaderProps) {
    const [uploading, setUploading] = useState(false);

    const isImageUrl = (url: string) => {
        try {
            const urlObj = new URL(url);
            const pathname = urlObj.pathname;
            return /\.(jpg|jpeg|png|webp|gif)$/i.test(pathname) ||
                /\.(jpg|jpeg|png|webp|gif)\?/.test(url); // Fallback for some signed URLs
        } catch (e) {
            return false;
        }
    };

    const handleFileChange = async (e: ChangeEvent<HTMLInputElement>) => {
        if (!e.target.files || e.target.files.length === 0) return;

        const file = e.target.files[0]; // Handle single file for now, loop if multiple needed
        try {
            setUploading(true);
            const path = `subcontractors/${folder}/${Date.now()}_${file.name}`;
            const url = await uploadSubcontractorFile(file, path);
            onUploadComplete(url);
            toast.success("File uploaded successfully");

            // Reset input
            e.target.value = "";
        } catch (error) {
            console.error(error);
            toast.error("Failed to upload file");
        } finally {
            setUploading(false);
        }
    };

    return (
        <div className="space-y-4">
            <Label>{label}</Label>

            {/* Existing Files List */}
            {currentUrls.length > 0 && (
                <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-4">
                    {currentUrls.map((url, index) => {
                        const isImage = isImageUrl(url);
                        return (
                            <div key={index} className="relative group border rounded-md overflow-hidden bg-muted/30">
                                <a href={url} target="_blank" rel="noopener noreferrer" className="block aspect-square w-full">
                                    {isImage ? (
                                        <div className="relative w-full h-full">
                                            {/* We use standard img tag here because Next/Image requires configured domains or strictly static imports for optimal use, and these are dynamic arbitrary user uploads */}
                                            {/* eslint-disable-next-line @next/next/no-img-element */}
                                            <img
                                                src={url}
                                                alt={`Document ${index + 1}`}
                                                className="w-full h-full object-cover transition-transform hover:scale-105"
                                            />
                                        </div>
                                    ) : (
                                        <div className="flex flex-col items-center justify-center w-full h-full p-4 gap-2 text-muted-foreground hover:text-foreground hover:bg-muted/50 transition-colors">
                                            <FileText className="h-8 w-8" />
                                            <span className="text-xs text-center truncate w-full px-2">Document {index + 1}</span>
                                        </div>
                                    )}
                                </a>
                                {onRemove && (
                                    <Button
                                        variant="destructive"
                                        size="icon"
                                        type="button"
                                        onClick={(e) => {
                                            e.preventDefault();
                                            onRemove(url);
                                        }}
                                        className="absolute top-1 right-1 h-6 w-6 opacity-0 group-hover:opacity-100 transition-opacity"
                                    >
                                        <X className="h-3 w-3" />
                                    </Button>
                                )}
                            </div>
                        );
                    })}
                </div>
            )}

            {/* Upload Input */}
            <div className="flex items-center gap-2">
                <Input
                    type="file"
                    accept="image/*,application/pdf"
                    onChange={handleFileChange}
                    disabled={uploading}
                    className="max-w-xs"
                />
                {uploading && <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />}
            </div>
            <p className="text-xs text-muted-foreground">
                Accepted formats: JPG, PNG, PDF
            </p>
        </div>
    );
}

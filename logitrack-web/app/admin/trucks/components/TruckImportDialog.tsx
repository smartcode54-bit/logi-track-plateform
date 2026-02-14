"use client";

import { useState, useRef } from "react";
import { Button } from "@/components/ui/button";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
    DialogTrigger,
} from "@/components/ui/dialog";
import {
    Tooltip,
    TooltipContent,
    TooltipProvider,
    TooltipTrigger,
} from "@/components/ui/tooltip";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Upload, FileSpreadsheet, AlertCircle, CheckCircle, XCircle, Download } from "lucide-react";
import { useLanguage } from "@/context/language";
import * as XLSX from "xlsx";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Badge } from "@/components/ui/badge";
import { collection, writeBatch, doc } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { toast } from "sonner";

interface ImportedTruck {
    licensePlate: string;
    province: string;
    type: string;
    brand: string;
    model: string;
    truckStatus: string;
    ownershipType: string;
    isValid: boolean;
    errors: string[];
}

export function TruckImportDialog() {
    const { t } = useLanguage();
    const [open, setOpen] = useState(false);
    const [file, setFile] = useState<File | null>(null);
    const [importedData, setImportedData] = useState<ImportedTruck[]>([]);
    const [isImporting, setIsImporting] = useState(false);
    const fileInputRef = useRef<HTMLInputElement>(null);

    const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
        const selectedFile = e.target.files?.[0];
        if (selectedFile) {
            setFile(selectedFile);
            parseFile(selectedFile);
        }
    };

    const parseFile = async (file: File) => {
        const reader = new FileReader();
        reader.onload = (e) => {
            try {
                const data = e.target?.result;
                const workbook = XLSX.read(data, { type: "binary" });
                const sheetName = workbook.SheetNames[0];
                const sheet = workbook.Sheets[sheetName];
                const jsonData = XLSX.utils.sheet_to_json(sheet);

                validateData(jsonData);
            } catch (error) {
                console.error("Error parsing file:", error);
                toast.error(t('trucks.import.parseError'));
            }
        };
        reader.readAsBinaryString(file);
    };

    const validateData = (data: any[]) => {
        const validated: ImportedTruck[] = data.map((row) => {
            const errors: string[] = [];
            // Required fields
            if (!row['License Plate']) errors.push("Missing License Plate");
            if (!row['Province']) errors.push("Missing Province");
            if (!row['Type']) errors.push("Missing Type");

            // Default values and normalization
            const truck: ImportedTruck = {
                licensePlate: row['License Plate'] || "",
                province: row['Province'] || "",
                type: row['Type']?.toLowerCase() || "4-wheel", // Normalize 
                brand: row['Brand'] || "",
                model: row['Model'] || "",
                truckStatus: row['Status']?.toLowerCase() || "active",
                ownershipType: row['Ownership']?.toLowerCase() === 'company' ? 'own' : 'subcontractor',
                isValid: errors.length === 0,
                errors: errors
            };
            return truck;
        });
        setImportedData(validated);
    };

    const handleImport = async () => {
        const validTrucks = importedData.filter(truck => truck.isValid);
        if (validTrucks.length === 0) return;

        setIsImporting(true);
        try {
            const batch = writeBatch(db);
            const trucksRef = collection(db, COLLECTIONS.TRUCKS);

            validTrucks.forEach(truck => {
                const newDocRef = doc(trucksRef);
                batch.set(newDocRef, {
                    licensePlate: truck.licensePlate,
                    province: truck.province,
                    type: truck.type,
                    brand: truck.brand,
                    model: truck.model,
                    truckStatus: truck.truckStatus,
                    ownershipType: truck.ownershipType,
                    createdAt: new Date(),
                    updatedAt: new Date(),
                    // Default empty fields to avoid undefined issues
                    vin: "",
                    engineNumber: "",
                    year: "",
                    color: "",
                    fuelType: "",
                    currentMileage: 0
                });
            });

            await batch.commit();
            toast.success(t('trucks.import.success').replace('{count}', validTrucks.length.toString()));
            setOpen(false);
            setFile(null);
            setImportedData([]);
        } catch (error) {
            console.error("Import error:", error);
            toast.error(t('trucks.import.error'));
        } finally {
            setIsImporting(false);
        }
    };

    const downloadTemplate = () => {
        const headers = [
            "License Plate",
            "Province",
            "Type",
            "Brand",
            "Model",
            "Status",
            "Ownership"
        ];
        const ws = XLSX.utils.aoa_to_sheet([headers]);
        const wb = XLSX.utils.book_new();
        XLSX.utils.book_append_sheet(wb, ws, "Template");
        XLSX.writeFile(wb, "truck_import_template.xlsx");
    };

    return (
        <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger asChild>
                <Button variant="outline" className="gap-2">
                    <Upload className="h-4 w-4" />
                    {t('trucks.import')}
                </Button>
            </DialogTrigger>
            <DialogContent className="sm:max-w-[800px]">
                <DialogHeader>
                    <DialogTitle>{t('trucks.import.title')}</DialogTitle>
                    <DialogDescription>
                        {t('trucks.import.desc')}
                    </DialogDescription>
                </DialogHeader>

                <div className="space-y-4 py-4">
                    <div className="flex items-center gap-4">
                        <div className="grid w-full max-w-sm items-center gap-1.5">
                            <Label htmlFor="file">{t('trucks.import.selectFile')}</Label>
                            <Input
                                id="file"
                                type="file"
                                accept=".xlsx, .xls, .csv"
                                onChange={handleFileChange}
                                ref={fileInputRef}
                            />
                        </div>
                        <Button variant="secondary" onClick={downloadTemplate} className="mt-6">
                            <Download className="mr-2 h-4 w-4" />
                            {t('trucks.import.downloadTemplate')}
                        </Button>
                    </div>

                    {importedData.length > 0 && (
                        <div className="border rounded-md">
                            <div className="p-2 bg-muted/50 border-b">
                                <h4 className="text-sm font-semibold">{t('trucks.import.preview')} ({importedData.length})</h4>
                            </div>
                            <ScrollArea className="h-[300px]">
                                <Table>
                                    <TableHeader>
                                        <TableRow>
                                            <TableHead className="w-[50px]"></TableHead>
                                            <TableHead>Plate</TableHead>
                                            <TableHead>Province</TableHead>
                                            <TableHead>Type</TableHead>
                                            <TableHead>Status</TableHead>
                                            <TableHead>Ownership</TableHead>
                                        </TableRow>
                                    </TableHeader>
                                    <TableBody>
                                        {importedData.map((truck, index) => (
                                            <TableRow key={index} className={!truck.isValid ? "bg-red-50" : ""}>
                                                <TableCell>
                                                    {truck.isValid ? (
                                                        <CheckCircle className="h-4 w-4 text-green-500" />
                                                    ) : (
                                                        <TooltipProvider>
                                                            <Tooltip>
                                                                <TooltipTrigger>
                                                                    <AlertCircle className="h-4 w-4 text-red-500" />
                                                                </TooltipTrigger>
                                                                <TooltipContent>
                                                                    <p>{truck.errors.join(", ")}</p>
                                                                </TooltipContent>
                                                            </Tooltip>
                                                        </TooltipProvider>

                                                    )}
                                                </TableCell>
                                                <TableCell>{truck.licensePlate}</TableCell>
                                                <TableCell>{truck.province}</TableCell>
                                                <TableCell>{truck.type}</TableCell>
                                                <TableCell>
                                                    <Badge variant="outline">{truck.truckStatus}</Badge>
                                                </TableCell>
                                                <TableCell>{truck.ownershipType}</TableCell>
                                            </TableRow>
                                        ))}
                                    </TableBody>
                                </Table>
                            </ScrollArea>
                        </div>
                    )}
                </div>

                <DialogFooter>
                    <Button variant="outline" onClick={() => setOpen(false)}>
                        {t('users.form.cancel')}
                    </Button>
                    <Button
                        onClick={handleImport}
                        disabled={importedData.length === 0 || isImporting || importedData.every(d => !d.isValid)}
                    >
                        {isImporting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                        {t('trucks.import.confirm')}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

function Loader2(props: any) {
    return (
        <svg
            {...props}
            xmlns="http://www.w3.org/2000/svg"
            width="24"
            height="24"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
        >
            <path d="M21 12a9 9 0 1 1-6.219-8.56" />
        </svg>
    )
}

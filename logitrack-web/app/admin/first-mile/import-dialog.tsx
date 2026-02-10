"use client";

import { useState, useRef } from "react";
import * as XLSX from "xlsx";
import { Upload, X, FileSpreadsheet, Check, AlertCircle, Download } from "lucide-react";
import { format, parse } from "date-fns";
import { collection, writeBatch, doc } from "firebase/firestore";
import { db } from "@/firebase/client";
import { SOC_KEYS, SOC_DESTINATIONS } from "@/validate/firstMileTaskSchema";

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
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Progress } from "@/components/ui/progress";

interface ImportDialogProps {
    onSuccess: () => void;
}

export function FirstMileImportDialog({ onSuccess }: ImportDialogProps) {
    const [open, setOpen] = useState(false);
    const [file, setFile] = useState<File | null>(null);
    const [data, setData] = useState<any[]>([]);
    const [uploading, setUploading] = useState(false);
    const [progress, setProgress] = useState(0);
    const [error, setError] = useState<string | null>(null);
    const fileInputRef = useRef<HTMLInputElement>(null);

    const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
        const selectedFile = e.target.files?.[0];
        if (selectedFile) {
            setFile(selectedFile);
            parseExcel(selectedFile);
        }
    };

    const parseExcel = async (file: File) => {
        try {
            const data = await file.arrayBuffer();
            const workbook = XLSX.read(data);
            const sheetName = workbook.SheetNames[0];
            const sheet = workbook.Sheets[sheetName];
            const jsonData = XLSX.utils.sheet_to_json(sheet, { header: 1 });

            // Assuming Row 1 is Headers
            // We need to map columns Flexibly. 
            // Let's expect headers similar to the image or loose matching
            const headers = (jsonData[0] as string[]).map(h => h?.toString().toLowerCase().trim());
            const rows = jsonData.slice(1);

            const parsedRows = rows.map((row: any, index) => {
                if (!row || row.length === 0) return null;

                // Helper to find value by possible header names
                const getValue = (__headers: string[]) => {
                    const idx = headers.findIndex(h => __headers.some(ph => h?.includes(ph)));
                    return idx !== -1 ? row[idx] : undefined;
                };

                const rawDate = getValue(['date', 'วัน']);
                const sourceHub = getValue(['source', 'hub', 'ต้นทาง']);
                const destination = getValue(['destination', 'soc', 'ปลายทาง']);
                const time = getValue(['time', 'เวลา']);
                const plateType = getValue(['plateType', 'plate', 'ประเภทรถ', 'Truck Type']) || "4WH";
                const truckType = plateType; // Assign to new variable
                const shipmentId = getValue(['shipment', 'id', 'เลขงาน']);
                const licensePlate = getValue(['license', 'plate', 'ทะเบียน']);
                const driverName = getValue(['driver', 'name', 'คนขับ']);
                const driverPhone = getValue(['phone', 'tel', 'เบอร์']);

                // Normalize Data
                let formattedDate = new Date();
                if (rawDate) {
                    // Check if Excel Serial Date
                    if (typeof rawDate === 'number') {
                        const dateCode = XLSX.SSF.parse_date_code(rawDate);
                        // Fix potential JS Month index issue if needed (SSF usually correct)
                        formattedDate = new Date(dateCode.y, dateCode.m - 1, dateCode.d);
                    } else {
                        // Try parsing string "5-ก.พ.-2026" or "2026-02-05"
                        // For simplicity, defaulting to today if fail, or try basic parse
                        const d = new Date(rawDate);
                        if (!isNaN(d.getTime())) formattedDate = d;
                    }
                }

                // Normalize SOC
                let matchedSOC = destination;
                if (destination) {
                    const dStr = String(destination).toUpperCase();
                    if (dStr.includes("E") || dStr.includes("BUEROI")) matchedSOC = "SOC-E";
                    else if (dStr.includes("N") || dStr.includes("WANG")) matchedSOC = "SOC-N";
                    else if (dStr.includes("W") || dStr.includes("SAMUT")) matchedSOC = "SOC-W";
                }

                // Validate essentials
                const isValid = matchedSOC && sourceHub; // Minimum requirement

                return {
                    id: index, // Temp ID
                    date: formattedDate,
                    sourceHub,
                    destination: matchedSOC,
                    time,
                    truckType,
                    FirstMileTaskId: shipmentId,
                    licensePlate,
                    driverName,
                    driverPhone,
                    isValid
                };
            }).filter(Boolean); // Remove empty rows

            setData(parsedRows);
            setError(null);
        } catch (err) {
            console.error(err);
            setError("Failed to parse Excel file. Please check the format.");
        }
    };

    const handleDownloadTemplate = () => {
        const headers = [
            "Date (วัน)",
            "Source Hub (ค้นทาง)",
            "Destination (ปลายทาง)",
            "Time (เวลา)",
            "Truck Type (ประเภทรถ)",
            "Shipment ID (เลขงาน)",
            "License Plate (ทะเบียน)",
            "Driver Name (คนขับ)",
            "Driver Phone (เบอร์)"
        ];
        const ws = XLSX.utils.aoa_to_sheet([headers]);
        const wb = XLSX.utils.book_new();
        XLSX.utils.book_append_sheet(wb, ws, "Template");
        XLSX.writeFile(wb, "FirstMileTask_Template.xlsx");
    };

    const handleUpload = async () => {
        if (data.length === 0) return;
        setUploading(true);
        setProgress(10);

        try {
            const batchSize = 500;
            const totalBatches = Math.ceil(data.length / batchSize);

            for (let i = 0; i < totalBatches; i++) {
                const batch = writeBatch(db);
                const chunk = data.slice(i * batchSize, (i + 1) * batchSize);

                chunk.forEach(row => {
                    if (!row.isValid) return; // Skip invalid

                    const docRef = doc(collection(db, "first_mile_tasks"));
                    batch.set(docRef, {
                        date: row.date,
                        sourceHub: row.sourceHub,
                        destination: row.destination, // Need to ensure it matches Enum if likely
                        time: row.time || "",
                        truckType: row.truckType || "",
                        FirstMileTaskId: row.FirstMileTaskId || "",
                        licensePlate: row.licensePlate || "",
                        driverName: row.driverName || "",
                        driverPhone: row.driverPhone || "",
                        status: "Pending",
                        createdAt: new Date(),
                    });
                });

                await batch.commit();
                setProgress(10 + Math.round(((i + 1) / totalBatches) * 90));
            }

            setUploading(false);
            setOpen(false);
            onSuccess();
            // Reset
            setData([]);
            setFile(null);
        } catch (err) {
            console.error(err);
            setUploading(false);
            setError("Failed to push data to database.");
        }
    };

    return (
        <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger asChild>
                <Button variant="outline" className="gap-2">
                    <FileSpreadsheet className="h-4 w-4" />
                    Import Excel
                </Button>
            </DialogTrigger>
            <DialogContent className="max-w-4xl max-h-[80vh] flex flex-col">
                <DialogHeader>
                    <DialogTitle>Import Assignments from Excel</DialogTitle>
                    <DialogDescription>
                        Upload a spreadsheet with Plan specific columns (Date, Source, Destination, etc.).
                    </DialogDescription>
                </DialogHeader>

                <div className="flex-1 overflow-hidden flex flex-col gap-4">
                    {!file ? (
                        <div className="flex flex-col gap-4 h-full">
                            <div className="flex justify-between items-center">
                                <span className="text-sm text-muted-foreground">
                                    Please use the template to ensure correct data format.
                                </span>
                                <Button variant="outline" size="sm" onClick={handleDownloadTemplate} className="gap-2">
                                    <Download className="h-4 w-4" />
                                    Download Template
                                </Button>
                            </div>

                            <div
                                className="border-2 border-dashed rounded-lg p-12 flex flex-col items-center justify-center text-muted-foreground hover:bg-muted/50 transition-colors cursor-pointer"
                                onClick={() => fileInputRef.current?.click()}
                            >
                                <Upload className="h-12 w-12 mb-4 text-gray-400" />
                                <p className="font-medium text-lg">Click to Upload Excel File</p>
                                <p className="text-sm">.xlsx, .xls formats supported</p>
                                <input
                                    ref={fileInputRef}
                                    type="file"
                                    accept=".xlsx, .xls"
                                    className="hidden"
                                    onChange={handleFileChange}
                                />
                            </div>
                        </div>
                    ) : (
                        <div className="flex flex-col gap-4 h-full">
                            <div className="flex items-center justify-between bg-muted/30 p-3 rounded-md border">
                                <div className="flex items-center gap-3">
                                    <div className="bg-green-100 p-2 rounded">
                                        <FileSpreadsheet className="h-5 w-5 text-green-600" />
                                    </div>
                                    <div>
                                        <p className="font-medium text-sm">{file.name}</p>
                                        <p className="text-xs text-muted-foreground">{data.length} records found</p>
                                    </div>
                                </div>
                                <Button variant="ghost" size="icon" onClick={() => setFile(null)}>
                                    <X className="h-4 w-4" />
                                </Button>
                            </div>

                            {error && (
                                <Alert variant="destructive">
                                    <AlertCircle className="h-4 w-4" />
                                    <AlertTitle>Error</AlertTitle>
                                    <AlertDescription>{error}</AlertDescription>
                                </Alert>
                            )}

                            <div className="border rounded-md flex-1 overflow-hidden">
                                <ScrollArea className="h-[400px]">
                                    <Table>
                                        <TableHeader>
                                            <TableRow className="bg-muted/50">
                                                <TableHead>Row</TableHead>
                                                <TableHead>Date</TableHead>
                                                <TableHead>Source</TableHead>
                                                <TableHead>Dest</TableHead>
                                                <TableHead>Time</TableHead>
                                                <TableHead>Truck Type</TableHead>
                                                <TableHead>First Mile Task ID</TableHead>
                                                <TableHead>Driver</TableHead>
                                                <TableHead>Status</TableHead>
                                            </TableRow>
                                        </TableHeader>
                                        <TableBody>
                                            {data.map((row, idx) => (
                                                <TableRow key={idx} className={!row.isValid ? "bg-red-50" : ""}>
                                                    <TableCell className="font-mono text-xs">{idx + 1}</TableCell>
                                                    <TableCell>{row.date ? format(row.date, 'dd/MM/yyyy') : '-'}</TableCell>
                                                    <TableCell>{row.sourceHub}</TableCell>
                                                    <TableCell>
                                                        <span className={row.destination ? "text-green-600 font-medium" : "text-red-500"}>
                                                            {row.destination || "Unknown"}
                                                        </span>
                                                    </TableCell>
                                                    <TableCell>{row.time}</TableCell>
                                                    <TableCell>{row.truckType}</TableCell>
                                                    <TableCell className="text-xs">{row.FirstMileTaskId}</TableCell>
                                                    <TableCell>{row.driverName}</TableCell>
                                                    <TableCell>
                                                        {row.isValid ? (
                                                            <Check className="h-4 w-4 text-green-500" />
                                                        ) : (
                                                            <span className="text-xs text-red-500 font-medium">Invalid</span>
                                                        )}
                                                    </TableCell>
                                                </TableRow>
                                            ))}
                                        </TableBody>
                                    </Table>
                                </ScrollArea>
                            </div>
                        </div>
                    )}
                </div>

                <DialogFooter className="mt-4">
                    {uploading ? (
                        <div className="w-full space-y-2">
                            <div className="flex justify-between text-xs">
                                <span>Uploading...</span>
                                <span>{progress}%</span>
                            </div>
                            <Progress value={progress} />
                        </div>
                    ) : (
                        <>
                            <Button variant="outline" onClick={() => setOpen(false)}>Cancel</Button>
                            <Button onClick={handleUpload} disabled={!file || data.length === 0 || uploading}>
                                Upload {data.filter(d => d.isValid).length} Records
                            </Button>
                        </>
                    )}
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

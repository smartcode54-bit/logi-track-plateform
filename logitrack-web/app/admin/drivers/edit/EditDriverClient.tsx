"use client";

import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { driverSchema, Driver } from "@/validate/driverSchema";
import { getDriverByIdClient, updateDriver } from "../actions.client";
import { useRouter, useSearchParams } from "next/navigation";
import { useState, useEffect } from "react";
import { Button } from "@/components/ui/button";
import {
    Form,
    FormControl,
    FormField,
    FormItem,
    FormLabel,
    FormMessage,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import {
    Loader2, ArrowLeft, CheckCircle2, Upload, User,
    FileText, Truck, Save, X
} from "lucide-react";
import Link from "next/link";
import { toast } from "sonner";
import { DatePicker } from "@/components/ui/date-picker";
import { format } from "date-fns";
import { db } from "@/firebase/client";
import { collection, getDocs, query } from "firebase/firestore";
import { COLLECTIONS } from "@/lib/collections";
import Image from "next/image";

export default function EditDriverClient() {
    const router = useRouter();
    const searchParams = useSearchParams();
    const driverId = searchParams.get('id');

    const [isSubmitting, setIsSubmitting] = useState(false);
    const [isLoading, setIsLoading] = useState(true);

    // File states - for NEW uploads
    const [selectedImage, setSelectedImage] = useState<File | null>(null);
    const [imagePreview, setImagePreview] = useState<string | null>(null);
    const [idCardFile, setIdCardFile] = useState<File | null>(null);
    const [truckLicenseFile, setTruckLicenseFile] = useState<File | null>(null);

    // Existing URLs (for display if not changing)
    const [existingProfileImage, setExistingProfileImage] = useState<string | null>(null);
    const [existingIdCardImage, setExistingIdCardImage] = useState<string | null>(null);
    const [existingTruckLicenseImage, setExistingTruckLicenseImage] = useState<string | null>(null);

    const [subcontractors, setSubcontractors] = useState<any[]>([]);

    const form = useForm<Driver>({
        resolver: zodResolver(driverSchema) as any,
        defaultValues: {
            firstName: "",
            lastName: "",
            mobile: "",
            email: "",
            idCard: "",
            truckLicenseId: "",
            contractYears: "" as any,
            status: "Active",
            employmentType: "FULL_TIME",
        },
        mode: "onChange",
    });

    // Fetch driver data
    useEffect(() => {
        const fetchDriver = async () => {
            if (!driverId) {
                toast.error("No driver ID provided");
                router.push('/admin/drivers');
                return;
            }
            try {
                const driver = await getDriverByIdClient(driverId);
                if (driver) {
                    // Populate form
                    const formData = {
                        ...driver,
                        // Ensure empty strings for missing optional fields
                        email: driver.email || "",
                        contractYears: driver.contractYears || ("" as any),
                        // Ensure dates are strings or undefined for DatePicker/Form
                        birthDate: driver.birthDate || undefined,
                        idCardExpiredDate: driver.idCardExpiredDate || undefined,
                        truckLicenseExpiredDate: driver.truckLicenseExpiredDate || undefined,
                    };
                    form.reset(formData);

                    // Set existing images
                    if (driver.profileImage) {
                        setExistingProfileImage(driver.profileImage);
                        setImagePreview(driver.profileImage);
                    }
                    if (driver.idCardImage) setExistingIdCardImage(driver.idCardImage);
                    if (driver.truckLicenseImage) setExistingTruckLicenseImage(driver.truckLicenseImage);
                } else {
                    toast.error("Driver not found");
                    router.push('/admin/drivers');
                }
            } catch (error) {
                console.error(error);
                toast.error("Failed to load driver data");
            } finally {
                setIsLoading(false);
            }
        };
        fetchDriver();
    }, [driverId, router, form]);


    // Fetch subs
    const employmentType = form.watch("employmentType");
    useEffect(() => {
        const fetchSubcontractors = async () => {
            try {
                const q = query(collection(db, COLLECTIONS.SUBCONTRACTORS));
                const snapshot = await getDocs(q);
                const subs = snapshot.docs.map(doc => ({ id: doc.id, ...doc.data() }));
                setSubcontractors(subs);
            } catch (err) {
                console.error("Error fetching subcontractors:", err);
            }
        };
        fetchSubcontractors();
    }, []);

    // Handlers
    const handleImageChange = (e: React.ChangeEvent<HTMLInputElement>) => {
        const file = e.target.files?.[0];
        if (file) {
            setSelectedImage(file);
            const reader = new FileReader();
            reader.onloadend = () => setImagePreview(reader.result as string);
            reader.readAsDataURL(file);
        }
    };

    const removeImage = () => {
        setSelectedImage(null);
        setImagePreview(null);
        setExistingProfileImage(null);
        form.setValue("profileImage", undefined);
    };

    const onSubmit = async (data: Driver) => {
        if (!driverId) return;
        try {
            setIsSubmitting(true);

            // Prepare files map for the action
            // Only include files if they are new/changed
            const files: any = {};
            if (selectedImage) files.profile = selectedImage;
            if (idCardFile) files.idCard = idCardFile;
            if (truckLicenseFile) files.license = truckLicenseFile;

            await updateDriver(driverId, data, Object.keys(files).length > 0 ? files : undefined);

            toast.success("Driver updated successfully");
            router.push(`/admin/drivers/view?id=${driverId}`);
        } catch (error) {
            console.error(error);
            toast.error("Failed to update driver");
        } finally {
            setIsSubmitting(false);
        }
    };

    if (isLoading) {
        return (
            <div className="flex items-center justify-center min-h-screen">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                <span className="ml-2 text-muted-foreground">Loading...</span>
            </div>
        );
    }

    return (
        <div className="container mx-auto px-4 py-8">
            <div className="max-w-4xl mx-auto space-y-6">
                {/* Header */}
                <div className="flex items-center justify-between">
                    <div>
                        <h1 className="text-3xl font-bold text-foreground">
                            Edit Driver
                        </h1>
                        <p className="text-muted-foreground mt-1">
                            Update driver information
                        </p>
                    </div>
                    <Button variant="outline" asChild>
                        <Link href={`/admin/drivers/view?id=${driverId}`} className="flex items-center gap-2">
                            <ArrowLeft className="h-4 w-4" />
                            Back to Details
                        </Link>
                    </Button>
                </div>

                <Form {...form}>
                    <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-6">

                        {/* Personal Info */}
                        <Card>
                            <CardHeader>
                                <CardTitle className="flex items-center gap-2">
                                    <User className="h-5 w-5 text-primary" />
                                    Personal Information
                                </CardTitle>
                            </CardHeader>
                            <CardContent className="space-y-6">
                                {/* Profile Image */}
                                <div className="flex justify-center mb-6">
                                    <div className="relative group">
                                        <div className="w-32 h-32 rounded-full border-2 border-dashed border-gray-300 flex items-center justify-center overflow-hidden bg-gray-50 relative">
                                            {imagePreview ? (
                                                <Image src={imagePreview} alt="Preview" fill className="object-cover" />
                                            ) : (
                                                <label htmlFor="p-upload" className="flex flex-col items-center justify-center cursor-pointer w-full h-full">
                                                    <Upload className="h-8 w-8 text-muted-foreground mb-2" />
                                                    <span className="text-xs text-muted-foreground">Change Photo</span>
                                                </label>
                                            )}
                                            <input id="p-upload" type="file" accept="image/*" className="hidden" onChange={handleImageChange} />
                                        </div>
                                        {imagePreview && (
                                            <button type="button" onClick={removeImage} className="absolute -top-1 -right-1 bg-red-500 text-white rounded-full p-1.5 hover:bg-red-600 shadow-sm">
                                                <X className="h-4 w-4" />
                                            </button>
                                        )}
                                    </div>
                                </div>

                                <div className="grid md:grid-cols-2 gap-6">
                                    <FormField control={form.control} name="firstName" render={({ field }) => (
                                        <FormItem><FormLabel>First Name <span className="text-red-500">*</span></FormLabel><FormControl><Input placeholder="John" {...field} /></FormControl><FormMessage /></FormItem>
                                    )} />
                                    <FormField control={form.control} name="lastName" render={({ field }) => (
                                        <FormItem><FormLabel>Last Name <span className="text-red-500">*</span></FormLabel><FormControl><Input placeholder="Doe" {...field} /></FormControl><FormMessage /></FormItem>
                                    )} />
                                </div>

                                <div className="grid md:grid-cols-2 gap-6">
                                    <FormField control={form.control} name="mobile" render={({ field }) => (
                                        <FormItem><FormLabel>Mobile <span className="text-red-500">*</span></FormLabel><FormControl><Input placeholder="08x-xxx-xxxx" {...field} /></FormControl><FormMessage /></FormItem>
                                    )} />
                                    <FormField control={form.control} name="email" render={({ field }) => (
                                        <FormItem><FormLabel>Email</FormLabel><FormControl><Input placeholder="john@example.com" {...field} /></FormControl><FormMessage /></FormItem>
                                    )} />
                                </div>

                                <FormField control={form.control} name="birthDate" render={({ field }) => {
                                    const maxDate = new Date(); maxDate.setFullYear(maxDate.getFullYear() - 20);
                                    const minDate = new Date(); minDate.setFullYear(minDate.getFullYear() - 55);
                                    return (
                                        <FormItem className="flex flex-col">
                                            <FormLabel>Birth Date (Age 20-55) <span className="text-red-500">*</span></FormLabel>
                                            <DatePicker
                                                value={field.value ? new Date(field.value) : undefined}
                                                onChange={(date) => field.onChange(date ? format(date, "yyyy-MM-dd") : "")}
                                                fromYear={minDate.getFullYear()}
                                                toYear={maxDate.getFullYear()}
                                                disabled={(d: Date) => d > maxDate || d < minDate}
                                            />
                                            <FormMessage />
                                        </FormItem>
                                    );
                                }} />
                            </CardContent>
                        </Card>

                        {/* Identity Documents */}
                        <Card>
                            <CardHeader>
                                <CardTitle className="flex items-center gap-2">
                                    <FileText className="h-5 w-5 text-primary" />
                                    Identity Documents
                                </CardTitle>
                            </CardHeader>
                            <CardContent className="space-y-6">
                                <div className="bg-slate-50 dark:bg-slate-900/50 p-4 rounded-lg space-y-4">
                                    <h3 className="font-semibold flex items-center gap-2">National ID Card</h3>
                                    <FormField control={form.control} name="idCard" render={({ field }) => (
                                        <FormItem><FormLabel>ID Number <span className="text-red-500">*</span></FormLabel><FormControl><Input placeholder="13-digit ID" maxLength={13} {...field} /></FormControl><FormMessage /></FormItem>
                                    )} />
                                    <div className="grid md:grid-cols-2 gap-4">
                                        <FormField control={form.control} name="idCardExpiredDate" render={({ field }) => (
                                            <FormItem className="flex flex-col">
                                                <FormLabel>Expiry Date</FormLabel>
                                                <DatePicker
                                                    value={field.value ? new Date(field.value) : undefined}
                                                    onChange={(date) => field.onChange(date ? format(date, "yyyy-MM-dd") : "")}
                                                    fromYear={new Date().getFullYear()}
                                                    toYear={new Date().getFullYear() + 10}
                                                />
                                                <FormMessage />
                                            </FormItem>
                                        )} />
                                        <FormItem>
                                            <FormLabel>ID Card Image</FormLabel>
                                            <div className="flex items-center gap-2">
                                                <Input type="file" onChange={(e) => { const f = e.target.files?.[0]; if (f) setIdCardFile(f); }} />
                                                {idCardFile ? <FileText className="text-green-600 h-5 w-5" /> : existingIdCardImage && <CheckCircle2 className="text-blue-500 h-5 w-5" />}
                                            </div>
                                            {!idCardFile && existingIdCardImage && <p className="text-xs text-muted-foreground mt-1">Current file exists. Upload to replace.</p>}
                                        </FormItem>
                                    </div>
                                </div>

                                <div className="bg-slate-50 dark:bg-slate-900/50 p-4 rounded-lg space-y-4">
                                    <h3 className="font-semibold flex items-center gap-2">Driving License</h3>
                                    <div className="grid md:grid-cols-2 gap-4">
                                        <FormField control={form.control} name="truckLicenseId" render={({ field }) => (
                                            <FormItem><FormLabel>License Number <span className="text-red-500">*</span></FormLabel><FormControl><Input placeholder="License No." maxLength={8} {...field} /></FormControl><FormMessage /></FormItem>
                                        )} />
                                        <FormField control={form.control} name="licenseType" render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>License Type</FormLabel>
                                                <Select onValueChange={field.onChange} value={field.value}>
                                                    <FormControl><SelectTrigger><SelectValue placeholder="Select..." /></SelectTrigger></FormControl>
                                                    <SelectContent>
                                                        <SelectItem value="บ.1">บ.1</SelectItem>
                                                        <SelectItem value="บ.2">บ.2</SelectItem>
                                                        <SelectItem value="บ.3">บ.3</SelectItem>
                                                        <SelectItem value="บ.4">บ.4</SelectItem>
                                                        <SelectItem value="ท.1">ท.1</SelectItem>
                                                        <SelectItem value="ท.2">ท.2</SelectItem>
                                                        <SelectItem value="ท.3">ท.3</SelectItem>
                                                        <SelectItem value="ท.4">ท.4</SelectItem>
                                                    </SelectContent>
                                                </Select>
                                                <FormMessage />
                                            </FormItem>
                                        )} />
                                    </div>
                                    <div className="grid md:grid-cols-2 gap-4">
                                        <FormField control={form.control} name="truckLicenseExpiredDate" render={({ field }) => (
                                            <FormItem className="flex flex-col">
                                                <FormLabel>Expiry Date</FormLabel>
                                                <DatePicker
                                                    value={field.value ? new Date(field.value) : undefined}
                                                    onChange={(date) => field.onChange(date ? format(date, "yyyy-MM-dd") : "")}
                                                    fromYear={new Date().getFullYear()}
                                                    toYear={new Date().getFullYear() + 10}
                                                />
                                                <FormMessage />
                                            </FormItem>
                                        )} />
                                        <FormItem>
                                            <FormLabel>License Image</FormLabel>
                                            <div className="flex items-center gap-2">
                                                <Input type="file" onChange={(e) => { const f = e.target.files?.[0]; if (f) setTruckLicenseFile(f); }} />
                                                {truckLicenseFile ? <FileText className="text-green-600 h-5 w-5" /> : existingTruckLicenseImage && <CheckCircle2 className="text-blue-500 h-5 w-5" />}
                                            </div>
                                            {!truckLicenseFile && existingTruckLicenseImage && <p className="text-xs text-muted-foreground mt-1">Current file exists. Upload to replace.</p>}
                                        </FormItem>
                                    </div>
                                </div>
                            </CardContent>
                        </Card>

                        {/* Employment Details */}
                        <Card>
                            <CardHeader>
                                <CardTitle className="flex items-center gap-2">
                                    <Truck className="h-5 w-5 text-primary" />
                                    Employment Details
                                </CardTitle>
                            </CardHeader>
                            <CardContent className="space-y-6">
                                <FormField control={form.control} name="employmentType" render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>Type</FormLabel>
                                        <Select onValueChange={field.onChange} value={field.value}>
                                            <FormControl><SelectTrigger><SelectValue /></SelectTrigger></FormControl>
                                            <SelectContent>
                                                <SelectItem value="FULL_TIME">Full Time</SelectItem>
                                                <SelectItem value="PART_TIME">Part Time</SelectItem>
                                                <SelectItem value="SUBCONTRACTOR">Subcontractor</SelectItem>
                                            </SelectContent>
                                        </Select>
                                        <FormMessage />
                                    </FormItem>
                                )} />

                                {employmentType === 'SUBCONTRACTOR' && (
                                    <FormField control={form.control} name="subcontractorId" render={({ field }) => (
                                        <FormItem>
                                            <FormLabel>Subcontractor Company</FormLabel>
                                            <Select onValueChange={(val) => {
                                                field.onChange(val);
                                                const sub = subcontractors.find(s => s.id === val);
                                                if (sub) form.setValue("subcontractorName", sub.companyName || sub.name);
                                            }} value={field.value}>
                                                <FormControl><SelectTrigger><SelectValue placeholder="Select..." /></SelectTrigger></FormControl>
                                                <SelectContent>
                                                    {subcontractors.map(s => (
                                                        <SelectItem key={s.id} value={s.id}>{s.companyName || s.name || "Unknown"}</SelectItem>
                                                    ))}
                                                </SelectContent>
                                            </Select>
                                            <FormMessage />
                                        </FormItem>
                                    )} />
                                )}

                                {employmentType !== 'PART_TIME' && (
                                    <FormField control={form.control} name="contractYears" render={({ field }) => (
                                        <FormItem><FormLabel>Contract Duration (Years)</FormLabel><FormControl><Input type="number" {...field} onChange={e => field.onChange(e.target.value)} /></FormControl><FormMessage /></FormItem>
                                    )} />
                                )}

                                <FormField control={form.control} name="status" render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>Status</FormLabel>
                                        <Select onValueChange={field.onChange} value={field.value}>
                                            <FormControl><SelectTrigger><SelectValue /></SelectTrigger></FormControl>
                                            <SelectContent>
                                                <SelectItem value="Active">Active</SelectItem>
                                                <SelectItem value="On-Duty">On-Duty</SelectItem>
                                                <SelectItem value="Inactive">Inactive</SelectItem>
                                            </SelectContent>
                                        </Select>
                                        <FormMessage />
                                    </FormItem>
                                )} />
                            </CardContent>
                        </Card>

                        {/* Actions */}
                        <div className="flex justify-end gap-4">
                            <Button type="button" variant="outline" asChild>
                                <Link href={`/admin/drivers/view?id=${driverId}`}>Cancel</Link>
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

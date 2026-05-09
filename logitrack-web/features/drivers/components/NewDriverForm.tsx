"use client";

import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { driverSchema, Driver } from "@/validate/driverSchema";
import { createDriver } from "@/features/drivers/api/drivers";
import { useRouter } from "next/navigation";
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
    FileText, Truck, X
} from "lucide-react";
import Link from "next/link";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { DatePicker } from "@/components/ui/date-picker";
import { db } from "@/firebase/client";
import { collection, getDocs, query } from "firebase/firestore";
import { COLLECTIONS } from "@/lib/collections";
import Image from "next/image";
import { useLanguage } from "@/context/language";

export default function NewDriverForm() {
    const router = useRouter();
    const { t } = useLanguage();

    const STEPS = [
        { id: 1, title: t("drivers.step1.title"), description: t("drivers.step1.description") },
        { id: 2, title: t("drivers.step2.title"), description: t("drivers.step2.description") },
        { id: 3, title: t("drivers.step3.title"), description: t("drivers.step3.description") },
        { id: 4, title: t("drivers.step4.title"), description: t("drivers.step4.description") },
    ];
    const [currentStep, setCurrentStep] = useState(1);
    const [isSubmitting, setIsSubmitting] = useState(false);

    // File states
    const [selectedImage, setSelectedImage] = useState<File | null>(null);
    const [imagePreview, setImagePreview] = useState<string | null>(null);
    const [idCardFile, setIdCardFile] = useState<File | null>(null);
    const [truckLicenseFile, setTruckLicenseFile] = useState<File | null>(null);

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

    // Fetch subs
    const employmentType = form.watch("employmentType");
    useEffect(() => {
        const fetchSubcontractors = async () => {
            if (employmentType === 'SUBCONTRACTOR') {
                try {
                    const q = query(collection(db, COLLECTIONS.SUBCONTRACTORS));
                    const snapshot = await getDocs(q);
                    const subs = snapshot.docs.map(doc => ({ id: doc.id, ...doc.data() }));
                    setSubcontractors(subs);
                } catch (err) {
                    console.error("Error fetching subcontractors:", err);
                    toast.error(t("drivers.toast.subLoadError"));
                }
            }
        };
        fetchSubcontractors();
    }, [employmentType]);

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
        form.setValue("profileImage", undefined);
    };

    const onSubmit = async (data: Driver) => {
        try {
            setIsSubmitting(true);

            const files = {
                profile: selectedImage,
                idCard: idCardFile,
                license: truckLicenseFile
            };

            await createDriver(data, files);

            toast.success(`${t("drivers.word.driver")} ${data.firstName} ${data.lastName} ${t("drivers.toast.registerSuccess")}`);
            router.push("/app/drivers");
        } catch (error) {
            console.error(error);
            toast.error(t("drivers.toast.registerError"));
        } finally {
            setIsSubmitting(false);
        }
    };

    const nextStep = async () => {
        let fieldsToValidate: any[] = [];
        if (currentStep === 1) fieldsToValidate = ['firstName', 'lastName', 'mobile', 'email', 'birthDate'];
        if (currentStep === 2) fieldsToValidate = ['idCard', 'truckLicenseId'];
        if (currentStep === 3) fieldsToValidate = ['employmentType', 'contractYears', 'employmentPeriod', 'subcontractorId'];

        const isValid = await form.trigger(fieldsToValidate);
        if (isValid) {
            setCurrentStep(prev => prev + 1);
            window.scrollTo({ top: 0, behavior: "smooth" });
        }
    };

    const prevStep = () => {
        setCurrentStep(prev => Math.max(1, prev - 1));
        window.scrollTo({ top: 0, behavior: "smooth" });
    };

    return (
        <div className="min-h-screen bg-background text-foreground flex flex-col">
            {/* Header */}
            <div className="flex items-center px-8 py-6 border-b bg-background sticky top-0 z-10">
                <Button variant="ghost" size="icon" asChild className="mr-4">
                    <Link href="/app/drivers" prefetch={false}>
                        <ArrowLeft className="h-5 w-5" />
                    </Link>
                </Button>
                <div>
                    <h1 className="text-2xl font-bold">{t("drivers.new.title")}</h1>
                    <p className="text-muted-foreground text-sm">{t("drivers.new.step")} {currentStep} {t("drivers.new.of")} {STEPS.length}: {STEPS[currentStep - 1].title}</p>
                </div>
            </div>

            <div className="flex flex-1 overflow-hidden">
                {/* Sidebar */}
                <div className="w-64 border-r bg-muted/30 p-6 hidden md:block overflow-y-auto">
                    <div className="space-y-6">
                        {STEPS.map((step) => {
                            const isActive = step.id === currentStep;
                            const isCompleted = step.id < currentStep;
                            return (
                                <div key={step.id} className={cn("flex items-start gap-3 relative", isActive ? "opacity-100" : "opacity-60")}>
                                    <div className={cn(
                                        "flex items-center justify-center w-8 h-8 rounded-full border-2 text-sm font-semibold z-10 bg-background",
                                        isActive ? "border-primary text-primary" : "",
                                        isCompleted ? "border-primary bg-primary text-primary-foreground" : "border-muted-foreground/30 text-muted-foreground"
                                    )}>
                                        {isCompleted ? <CheckCircle2 className="h-5 w-5" /> : step.id}
                                    </div>
                                    {step.id !== STEPS.length && (
                                        <div className="absolute left-4 top-8 bottom-[-24px] w-[2px] bg-border" />
                                    )}
                                    <div className="pt-1">
                                        <p className={cn("text-sm font-medium", isActive ? "text-primary" : "")}>{step.title}</p>
                                        <p className="text-xs text-muted-foreground">{step.description}</p>
                                    </div>
                                </div>
                            );
                        })}
                    </div>
                </div>

                {/* Main Content */}
                <div className="flex-1 overflow-y-auto p-6 md:p-8 relative">
                    <div className="max-w-3xl mx-auto pb-24">
                        <Form {...form}>
                            <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-8">

                                {/* Step 1: Personal Info */}
                                {currentStep === 1 && (
                                    <div className="space-y-6 animate-in slide-in-from-right-4 fade-in">
                                        <Card className="border-none shadow-sm">
                                            <CardHeader><CardTitle>{t("drivers.form.personalInfo")}</CardTitle></CardHeader>
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
                                                                    <span className="text-xs text-muted-foreground">{t("drivers.form.uploadPhoto")}</span>
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
                                                        <FormItem><FormLabel>{t("drivers.form.firstName")} <span className="text-red-500">*</span></FormLabel><FormControl><Input placeholder={t("drivers.form.firstName.placeholder")} {...field} /></FormControl><FormMessage /></FormItem>
                                                    )} />
                                                    <FormField control={form.control} name="lastName" render={({ field }) => (
                                                        <FormItem><FormLabel>{t("drivers.form.lastName")} <span className="text-red-500">*</span></FormLabel><FormControl><Input placeholder={t("drivers.form.lastName.placeholder")} {...field} /></FormControl><FormMessage /></FormItem>
                                                    )} />
                                                </div>

                                                <div className="grid md:grid-cols-2 gap-6">
                                                    <FormField control={form.control} name="mobile" render={({ field }) => (
                                                        <FormItem><FormLabel>{t("drivers.form.mobile")} <span className="text-red-500">*</span></FormLabel><FormControl><Input placeholder={t("drivers.form.mobile.placeholder")} {...field} /></FormControl><FormMessage /></FormItem>
                                                    )} />
                                                    <FormField control={form.control} name="email" render={({ field }) => (
                                                        <FormItem><FormLabel>{t("drivers.form.email")}</FormLabel><FormControl><Input placeholder={t("drivers.form.email.placeholder")} {...field} /></FormControl><FormMessage /></FormItem>
                                                    )} />
                                                </div>

                                                <FormField control={form.control} name="birthDate" render={({ field }) => {
                                                    const maxDate = new Date(); maxDate.setFullYear(maxDate.getFullYear() - 20);
                                                    const minDate = new Date(); minDate.setFullYear(minDate.getFullYear() - 55);
                                                    return (
                                                        <FormItem>
                                                            <FormLabel>{t("drivers.form.birthDate")} <span className="text-red-500">*</span></FormLabel>
                                                            <DatePicker
                                                                value={field.value}
                                                                onChange={field.onChange}
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
                                    </div>
                                )}

                                {/* Step 2: Identity */}
                                {currentStep === 2 && (
                                    <div className="space-y-6 animate-in slide-in-from-right-4 fade-in">
                                        <Card className="border-none shadow-sm">
                                            <CardHeader><CardTitle>{t("drivers.form.identityDocs")}</CardTitle></CardHeader>
                                            <CardContent className="space-y-6">
                                                <div className="p-4 border rounded-lg bg-slate-50 dark:bg-slate-900/50 space-y-4">
                                                    <h3 className="font-semibold flex items-center gap-2"><User className="h-4 w-4" /> {t("drivers.form.nationalIdCard")}</h3>
                                                    <FormField control={form.control} name="idCard" render={({ field }) => (
                                                        <FormItem><FormLabel>{t("drivers.form.idNumber")} <span className="text-red-500">*</span></FormLabel><FormControl><Input placeholder={t("drivers.form.idNumber.placeholder")} maxLength={13} {...field} /></FormControl><FormMessage /></FormItem>
                                                    )} />
                                                    <div className="grid md:grid-cols-2 gap-4">
                                                        <FormField control={form.control} name="idCardExpiredDate" render={({ field }) => (
                                                            <FormItem><FormLabel>{t("drivers.form.expiryDate")}</FormLabel><DatePicker value={field.value} onChange={field.onChange} fromYear={new Date().getFullYear()} toYear={new Date().getFullYear() + 10} /><FormMessage /></FormItem>
                                                        )} />
                                                        <FormItem>
                                                            <FormLabel>{t("drivers.form.uploadImage")}</FormLabel>
                                                            <div className="flex items-center gap-2">
                                                                <Input type="file" onChange={(e) => { const f = e.target.files?.[0]; if (f) setIdCardFile(f); }} />
                                                                {idCardFile && <FileText className="text-green-600 h-5 w-5" />}
                                                            </div>
                                                        </FormItem>
                                                    </div>
                                                </div>

                                                <div className="p-4 border rounded-lg bg-slate-50 dark:bg-slate-900/50 space-y-4">
                                                    <h3 className="font-semibold flex items-center gap-2"><Truck className="h-4 w-4" /> {t("drivers.form.drivingLicense")}</h3>
                                                    <div className="grid md:grid-cols-2 gap-4">
                                                        <FormField control={form.control} name="truckLicenseId" render={({ field }) => (
                                                            <FormItem><FormLabel>{t("drivers.form.licenseNumber")} <span className="text-red-500">*</span></FormLabel><FormControl><Input placeholder={t("drivers.form.licenseNumber.placeholder")} maxLength={8} {...field} /></FormControl><FormMessage /></FormItem>
                                                        )} />
                                                        <FormField control={form.control} name="licenseType" render={({ field }) => (
                                                            <FormItem>
                                                                <FormLabel>{t("drivers.form.licenseType")}</FormLabel>
                                                                <Select onValueChange={field.onChange} defaultValue={field.value}>
                                                                    <FormControl><SelectTrigger value={field.value}><SelectValue placeholder={t("drivers.form.licenseType.placeholder")} /></SelectTrigger></FormControl>
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
                                                            <FormItem><FormLabel>{t("drivers.form.expiryDate")}</FormLabel><DatePicker value={field.value} onChange={field.onChange} fromYear={new Date().getFullYear()} toYear={new Date().getFullYear() + 10} /><FormMessage /></FormItem>
                                                        )} />
                                                        <FormItem>
                                                            <FormLabel>{t("drivers.form.uploadImage")}</FormLabel>
                                                            <div className="flex items-center gap-2">
                                                                <Input type="file" onChange={(e) => { const f = e.target.files?.[0]; if (f) setTruckLicenseFile(f); }} />
                                                                {truckLicenseFile && <FileText className="text-green-600 h-5 w-5" />}
                                                            </div>
                                                        </FormItem>
                                                    </div>
                                                </div>
                                            </CardContent>
                                        </Card>
                                    </div>
                                )}

                                {/* Step 3: Employment */}
                                {currentStep === 3 && (
                                    <div className="space-y-6 animate-in slide-in-from-right-4 fade-in">
                                        <Card className="border-none shadow-sm">
                                            <CardHeader><CardTitle>{t("drivers.form.employmentDetails")}</CardTitle></CardHeader>
                                            <CardContent className="space-y-6">
                                                <FormField control={form.control} name="employmentType" render={({ field }) => (
                                                    <FormItem>
                                                        <FormLabel>{t("drivers.form.type")}</FormLabel>
                                                        <Select onValueChange={field.onChange} defaultValue={field.value}>
                                                            <FormControl><SelectTrigger><SelectValue /></SelectTrigger></FormControl>
                                                            <SelectContent>
                                                                <SelectItem value="FULL_TIME">{t("drivers.form.type.fullTime")}</SelectItem>
                                                                <SelectItem value="PART_TIME">{t("drivers.form.type.partTime")}</SelectItem>
                                                                <SelectItem value="SUBCONTRACTOR">{t("drivers.form.type.subcontractor")}</SelectItem>
                                                            </SelectContent>
                                                        </Select>
                                                        <FormMessage />
                                                    </FormItem>
                                                )} />

                                                {employmentType === 'SUBCONTRACTOR' && (
                                                    <FormField control={form.control} name="subcontractorId" render={({ field }) => (
                                                        <FormItem>
                                                            <FormLabel>{t("drivers.form.subcontractorCompany")}</FormLabel>
                                                            <Select onValueChange={(val) => {
                                                                field.onChange(val);
                                                                const sub = subcontractors.find(s => s.id === val);
                                                                if (sub) form.setValue("subcontractorName", sub.companyName || sub.name);
                                                            }} value={field.value}>
                                                                <FormControl><SelectTrigger><SelectValue placeholder={t("drivers.form.subcontractorCompany.placeholder")} /></SelectTrigger></FormControl>
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
                                                        <FormItem><FormLabel>{t("drivers.form.contractDuration")}</FormLabel><FormControl><Input type="number" {...field} onChange={e => field.onChange(e.target.value)} /></FormControl><FormMessage /></FormItem>
                                                    )} />
                                                )}

                                                <FormField control={form.control} name="status" render={({ field }) => (
                                                    <FormItem>
                                                        <FormLabel>{t("drivers.form.initialStatus")}</FormLabel>
                                                        <Select onValueChange={field.onChange} defaultValue={field.value}>
                                                            <FormControl><SelectTrigger><SelectValue /></SelectTrigger></FormControl>
                                                            <SelectContent>
                                                                <SelectItem value="Active">{t("drivers.status.active")}</SelectItem>
                                                                <SelectItem value="On-Duty">{t("drivers.status.onDuty")}</SelectItem>
                                                                <SelectItem value="Inactive">{t("drivers.status.inactive")}</SelectItem>
                                                            </SelectContent>
                                                        </Select>
                                                        <FormMessage />
                                                    </FormItem>
                                                )} />
                                            </CardContent>
                                        </Card>
                                    </div>
                                )}

                                {/* Step 4: Review */}
                                {currentStep === 4 && (
                                    <div className="space-y-6 animate-in slide-in-from-right-4 fade-in">
                                        <Card className="border-none shadow-sm">
                                            <CardHeader><CardTitle>{t("drivers.form.reviewConfirm")}</CardTitle><CardDescription>{t("drivers.form.reviewDesc")}</CardDescription></CardHeader>
                                            <CardContent className="space-y-4">
                                                <div className="grid grid-cols-2 gap-4 text-sm border p-4 rounded-lg">
                                                    <span className="text-muted-foreground">{t("drivers.form.reviewName")}</span>
                                                    <span className="font-medium text-right">{form.getValues("firstName")} {form.getValues("lastName")}</span>
                                                    <span className="text-muted-foreground">{t("drivers.form.reviewMobile")}</span>
                                                    <span className="font-medium text-right">{form.getValues("mobile")}</span>
                                                    <span className="text-muted-foreground">{t("drivers.form.reviewIdCard")}</span>
                                                    <span className="font-medium text-right">{form.getValues("idCard")}</span>
                                                    <span className="text-muted-foreground">{t("drivers.form.reviewLicenseId")}</span>
                                                    <span className="font-medium text-right">{form.getValues("truckLicenseId")}</span>
                                                    <span className="text-muted-foreground">{t("drivers.form.reviewEmployment")}</span>
                                                    <span className="font-medium text-right">{form.getValues("employmentType")}</span>
                                                </div>
                                                <div className="flex gap-2 text-sm text-muted-foreground bg-blue-50 dark:bg-blue-900/20 p-3 rounded-md">
                                                    <CheckCircle2 className="h-4 w-4 text-blue-500" />
                                                    <span>{t("drivers.form.auditNote")}</span>
                                                </div>
                                            </CardContent>
                                        </Card>
                                    </div>
                                )}

                            </form>
                        </Form>
                    </div>

                    {/* Footer */}
                    <div className="absolute bottom-0 right-0 left-0 p-4 border-t bg-background/80 backdrop-blur-sm flex justify-between items-center z-20">
                        <Button type="button" variant="outline" onClick={currentStep === 1 ? () => router.push('/app/drivers') : prevStep} className="bg-background">
                            {currentStep === 1 ? t("drivers.form.cancel") : t("drivers.form.back")}
                        </Button>
                        <Button type="button" className="bg-blue-600 hover:bg-blue-700 text-white min-w-[140px]" onClick={currentStep === 4 ? form.handleSubmit(onSubmit) : nextStep} disabled={isSubmitting}>
                            {isSubmitting ? <><Loader2 className="mr-2 h-4 w-4 animate-spin" /> {t("drivers.form.saving")}</> : currentStep === 4 ? t("drivers.form.completeRegistration") : t("drivers.form.nextStep")}
                        </Button>
                    </div>
                </div>
            </div>
        </div>
    );
}

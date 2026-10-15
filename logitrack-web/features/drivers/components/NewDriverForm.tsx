"use client";

import { DriverFormMessage as FormMessage } from "./DriverFormMessage";
import { useForm } from "react-hook-form";
import { driverFormResolver, driverInvalidHandler as createInvalidHandler } from "./driverFormResolver";
import { Driver } from "@/validate/driverSchema";
import { isDriverLoginOutOfReach, type DriverDetailDTO } from "@/features/drivers/api/drivers";
import { useCreateDriverLogin, useDriverWrites } from "@/features/drivers/api/useDrivers";
import { useSubcontractorOptions } from "@/features/subcontractors/api/useSubcontractors";
import { carrierName } from "@/features/subcontractors/api/subcontractors";
import { applyServerFieldErrors, toastSaveError } from "@/lib/formServerErrors";
import type { CreateUserResult } from "@/features/users/api/users";
import { TemporaryPasswordNotice } from "./TemporaryPasswordNotice";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { PartialCreateError } from "@/lib/goUpload";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { RequiredMark } from "@/components/ui/required-mark";
import { Button } from "@/components/ui/button";
import {
    Form,
    FormControl,
    FormField,
    FormItem,
    FormLabel,
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
    Loader2, CheckCircle2, Upload, User,
    FileText, Truck, X, KeyRound,
} from "lucide-react";
import { Switch } from "@/components/ui/switch";
import Link from "next/link";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { DatePicker } from "@/components/ui/date-picker";
import { DriverBirthDatePicker } from "./DriverBirthDatePicker";
import Image from "next/image";
import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
import { assertDriverUpload, driverUploadAccept, DriverUploadError } from "../api/driverUploads";
import { driverDisplayName } from "@/lib/driverName";

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
    const { create } = useDriverWrites();
    // The temporary password stays in this component's state only (never a query or mutation cache).
    const createLogin = useCreateDriverLogin();
    const isSubmitting = create.isPending || createLogin.isPending;

    // Login account section state — default ON so new drivers can use the app. The login is a user
    // created through `POST /v1/users {role:"driver", driverId}` with a generated temporary password
    // shown once (R29, T25): no password is chosen here.
    const [createLoginAccount, setCreateLoginAccount] = useState(true);
    const [savedDriver, setSavedDriver] = useState<DriverDetailDTO | null>(null);
    const [created, setCreated] = useState<{ driverId: string; login: CreateUserResult | null } | null>(null);

    // File states
    const [selectedImage, setSelectedImage] = useState<File | null>(null);
    const [imagePreview, setImagePreview] = useState<string | null>(null);
    const [idCardFile, setIdCardFile] = useState<File | null>(null);
    const [truckLicenseFile, setTruckLicenseFile] = useState<File | null>(null);

    const form = useForm<Driver>({
        resolver: driverFormResolver(),
        defaultValues: {
            firstName: "",
            lastName: "",
            fullNameTh: "",
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

    const watchedEmail = form.watch("email");
    const employmentType = form.watch("employmentType");
    // `['subcontractors','options']` once the employment type needs it (T25). The customer driver ids are
    // keyed by customer code; the save resolves those codes by search (`createDriver`, UI8), no customer list.
    const { options: subcontractors, isError: subsError } = useSubcontractorOptions(employmentType === "SUBCONTRACTOR");
    if (subsError) console.warn("[drivers] subcontractor options failed");

    // Handlers
    const handleImageChange = (e: React.ChangeEvent<HTMLInputElement>) => {
        const file = e.target.files?.[0];
        if (file) {
            try { assertDriverUpload(file, "driver_profile"); } catch (error) {
                if (error instanceof DriverUploadError) toast.error(t(`drivers.upload.${error.reason}`, { file: error.fileName }));
                e.target.value = "";
                return;
            }
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
        if (createLoginAccount && !data.email?.trim()) {
            toast.error(t("drivers.form.loginAccount.emailRequired"));
            setCurrentStep(1);
            return;
        }
        let driverId: string;
        try {
            driverId = await create.mutateAsync({
                values: data,
                existing: savedDriver ?? undefined,
                files: { profile: selectedImage, idCard: idCardFile, license: truckLicenseFile },
            });
        } catch (error) {
            if (error instanceof PartialCreateError) {
                setSavedDriver(error.entity as DriverDetailDTO);
                toast.error(t("drivers.upload.partialSaved"));
                return;
            }
            if (error instanceof DriverUploadError) {
                toast.error(t(`drivers.upload.${error.reason}`, { file: error.fileName }));
                return;
            }
            if (applyServerFieldErrors(form, error, t, { licenseExpiredDate: "truckLicenseExpiredDate", tenantId: "subcontractorId" }) > 0) setCurrentStep(1);
            if (savedDriver) toast.error(t("drivers.upload.partialSaved"));
            else toastSaveError(error, t, "drivers.toast.registerError");
            return;
        }
        toast.success(`${driverDisplayName(data)} ${t("drivers.toast.registerSuccess")}`);
        if (!createLoginAccount) {
            router.push("/app/drivers");
            return;
        }
        try {
            // The login's membership goes in the driver's tenant: the carrier of a subcontractor driver
            // (as `createDriver` placed it), else the caller's active tenant.
            const carrier = data.employmentType === "SUBCONTRACTOR" && data.subcontractorId ? data.subcontractorId : undefined;
            const login = await createLogin.create({
                driver: { id: driverId, tenantId: carrier, mobile: data.mobile, fullNameTh: data.fullNameTh, firstName: data.firstName, lastName: data.lastName, displayName: data.fullNameTh },
                email: data.email!.trim(),
            });
            setCreated({ driverId, login });
        } catch (error) {
            if (isDriverLoginOutOfReach(error)) toast.error(t("masterData.drivers.login.outOfReach"));
            else toastSaveError(error, t, "drivers.toast.accountCreateError");
            setCreated({ driverId, login: null });
        }
    };

    const nextStep = async () => {
        let fieldsToValidate: any[] = [];
        if (currentStep === 1) fieldsToValidate = ['firstName', 'lastName', 'fullNameTh', 'mobile', 'email', 'birthDate'];
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

    if (created) {
        return (
            <div className="flex max-w-xl flex-col gap-section">
                <PageHeader title={t("nav.drivers")} recordName={t("drivers.new.title")} />
                {created.login ? (
                    <TemporaryPasswordNotice email={created.login.user.email ?? form.getValues("email") ?? ""} temporaryPassword={created.login.temporaryPassword} />
                ) : (
                    <p className="text-sm text-muted-foreground">{t("masterData.drivers.login.createLater")}</p>
                )}
                <div className="flex gap-2">
                    <Button asChild>
                        <Link href={`/app/drivers/view?id=${created.driverId}`} prefetch={false}>{t("drivers.action.viewProfile")}</Link>
                    </Button>
                    <Button variant="outline" asChild>
                        <Link href="/app/drivers" prefetch={false}>{t("drivers.detail.backToDrivers")}</Link>
                    </Button>
                </div>
            </div>
        );
    }

    return (
        <div className="flex flex-col gap-section">
            {/* Header */}
            <div className="sticky top-16 z-raised border-b bg-background pb-4">
                <PageHeader
                    title={t("nav.drivers")}
                    recordName={t("drivers.new.title")}
                    description={`${t("drivers.new.step")} ${currentStep} ${t("drivers.new.of")} ${STEPS.length}: ${STEPS[currentStep - 1].title}`}
                    back={{ href: "/app/drivers" }}
                />
            </div>

            {savedDriver ? (
                <Alert>
                    <AlertDescription>
                        {t("drivers.upload.partialSaved")}{" "}
                        <Link className="underline underline-offset-4" href={`/app/drivers/view?id=${savedDriver.id}`} prefetch={false}>{t("drivers.action.viewProfile")}</Link>
                    </AlertDescription>
                </Alert>
            ) : null}
            <div className="flex min-w-0 flex-1">
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
                <div className="min-w-0 flex-1 p-4 md:p-8">
                    <div className="max-w-3xl mx-auto pb-8">
                        <Form {...form}>
                            <form noValidate onSubmit={form.handleSubmit(onSubmit, createInvalidHandler(form, t))} className="space-y-8">

                                {/* Step 1: Personal Info */}
                                {currentStep === 1 && (
                                    <div className="space-y-6 animate-in slide-in-from-right-4 fade-in">
                                        <Card className="border-none shadow-sm">
                                            <CardHeader><CardTitle>{t("drivers.form.personalInfo")}</CardTitle></CardHeader>
                                            <CardContent className="space-y-6">
                                                {/* Profile Image */}
                                                <div className="flex justify-center mb-6">
                                                    <div className="relative group">
                                                        <div className="w-32 h-32 rounded-full border-2 border-dashed border-border flex items-center justify-center overflow-hidden bg-muted relative">
                                                            {imagePreview ? (
                                                                <Image src={imagePreview} alt={t("drivers.detail.profilePhoto")} fill className="object-cover" />
                                                            ) : (
                                                                <label htmlFor="p-upload" className="flex flex-col items-center justify-center cursor-pointer w-full h-full">
                                                                    <Upload className="h-8 w-8 text-muted-foreground mb-2" />
                                                                    <span className="text-xs text-muted-foreground">{t("drivers.form.uploadPhoto")}</span>
                                                                </label>
                                                            )}
                                                            <input id="p-upload" type="file" accept={driverUploadAccept("driver_profile")} className="hidden" onChange={handleImageChange} />
                                                        </div>
                                                        {imagePreview && (
                                                            <Button aria-label={t("common.removePhoto")} type="button" variant="destructive" size="icon" onClick={removeImage} className="absolute -top-1 -right-1 h-8 w-8">
                                                                <X className="h-4 w-4" />
                                                            </Button>
                                                        )}
                                                    </div>
                                                </div>

                                                <div className="grid md:grid-cols-2 gap-6">
                                                    <FormField control={form.control} name="firstName" render={({ field }) => (
                                                        <FormItem><FormLabel>{t("drivers.form.firstName")} <RequiredMark /></FormLabel><FormControl><Input aria-required="true" placeholder={t("drivers.form.firstName.placeholder")} {...field} /></FormControl><FormMessage /></FormItem>
                                                    )} />
                                                    <FormField control={form.control} name="lastName" render={({ field }) => (
                                                        <FormItem><FormLabel>{t("drivers.form.lastName")} <RequiredMark /></FormLabel><FormControl><Input aria-required="true" placeholder={t("drivers.form.lastName.placeholder")} {...field} /></FormControl><FormMessage /></FormItem>
                                                    )} />
                                                </div>

                                                <FormField control={form.control} name="fullNameTh" render={({ field }) => (
                                                    <FormItem><FormLabel>{t("drivers.form.fullNameTh")} <RequiredMark /></FormLabel><FormControl><Input aria-required="true" placeholder={t("drivers.form.fullNameTh.placeholder")} {...field} /></FormControl><FormMessage /></FormItem>
                                                )} />

                                                <div className="grid md:grid-cols-2 gap-6">
                                                    <FormField control={form.control} name="mobile" render={({ field }) => (
                                                        <FormItem><FormLabel>{t("drivers.form.mobile")} <RequiredMark /></FormLabel><FormControl><Input aria-required="true" placeholder={t("drivers.form.mobile.placeholder")} {...field} /></FormControl><FormMessage /></FormItem>
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
                                                            <FormLabel>{t("drivers.form.birthDate")} <RequiredMark /></FormLabel>
                                                            <DriverBirthDatePicker
                                                                value={field.value}
                                                                onChange={field.onChange}
                                                                minDate={minDate}
                                                                maxDate={maxDate}
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
                                                <div className="p-4 border rounded-lg bg-muted space-y-4">
                                                    <h3 className="font-semibold flex items-center gap-2"><User className="h-4 w-4" /> {t("drivers.form.nationalIdCard")}</h3>
                                                    <FormField control={form.control} name="idCard" render={({ field }) => (
                                                        <FormItem><FormLabel>{t("drivers.form.idNumber")} <RequiredMark /></FormLabel><FormControl><Input aria-required="true" placeholder={t("drivers.form.idNumber.placeholder")} maxLength={13} {...field} /></FormControl><FormMessage /></FormItem>
                                                    )} />
                                                    <div className="grid md:grid-cols-2 gap-4">
                                                        <FormField control={form.control} name="idCardExpiredDate" render={({ field }) => (
                                                            <FormItem><FormLabel>{t("drivers.form.expiryDate")}</FormLabel><DatePicker value={field.value} onChange={field.onChange} fromYear={new Date().getFullYear()} toYear={new Date().getFullYear() + 10} /><FormMessage /></FormItem>
                                                        )} />
                                                        <FormItem>
                                                            <FormLabel>{t("drivers.form.uploadImage")}</FormLabel>
                                                            <div className="flex items-center gap-2">
                                                                <Input type="file" aria-label={t("drivers.edit.idCardImage")} accept={driverUploadAccept("driver_id_card")} onChange={(e) => {
                                                    const file = e.target.files?.[0];
                                                    if (!file) return;
                                                    try { assertDriverUpload(file, "driver_id_card"); setIdCardFile(file); } catch (error) {
                                                        if (error instanceof DriverUploadError) toast.error(t(`drivers.upload.${error.reason}`, { file: error.fileName }));
                                                        e.target.value = "";
                                                    }
                                                }} />
                                                                {idCardFile && <FileText className="text-success h-5 w-5" />}
                                                            </div>
                                                        </FormItem>
                                                    </div>
                                                </div>

                                                <div className="p-4 border rounded-lg bg-muted space-y-4">
                                                    <h3 className="font-semibold flex items-center gap-2"><Truck className="h-4 w-4" /> {t("drivers.form.drivingLicense")}</h3>
                                                    <div className="grid md:grid-cols-2 gap-4">
                                                        <FormField control={form.control} name="truckLicenseId" render={({ field }) => (
                                                            <FormItem><FormLabel>{t("drivers.form.licenseNumber")} <RequiredMark /></FormLabel><FormControl><Input aria-required="true" placeholder={t("drivers.form.licenseNumber.placeholder")} maxLength={8} {...field} /></FormControl><FormMessage /></FormItem>
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
                                                                <Input type="file" aria-label={t("drivers.edit.licenseImage")} accept={driverUploadAccept("driver_license")} onChange={(e) => {
                                                    const file = e.target.files?.[0];
                                                    if (!file) return;
                                                    try { assertDriverUpload(file, "driver_license"); setTruckLicenseFile(file); } catch (error) {
                                                        if (error instanceof DriverUploadError) toast.error(t(`drivers.upload.${error.reason}`, { file: error.fileName }));
                                                        e.target.value = "";
                                                    }
                                                }} />
                                                                {truckLicenseFile && <FileText className="text-success h-5 w-5" />}
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
                                                                if (sub) form.setValue("subcontractorName", carrierName(sub));
                                                            }} value={field.value}>
                                                                <FormControl><SelectTrigger><SelectValue placeholder={t("drivers.form.subcontractorCompany.placeholder")} /></SelectTrigger></FormControl>
                                                                <SelectContent>
                                                                    {subcontractors.map(s => (
                                                                        <SelectItem key={s.id} value={s.id}>{carrierName(s) || s.id}</SelectItem>
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

                                        {/* Login Account Card */}
                                        <Card className="border-none shadow-sm">
                                            <CardHeader>
                                                <CardTitle className="flex items-center justify-between text-base">
                                                    <div className="flex items-center gap-2">
                                                        <KeyRound className="h-4 w-4 text-primary" />
                                                        {t("drivers.form.loginAccount")}
                                                    </div>
                                                    <Switch
                                                        checked={createLoginAccount}
                                                        onCheckedChange={setCreateLoginAccount}
                                                    />
                                                </CardTitle>
                                                <CardDescription>{t("drivers.form.loginAccount.desc")}</CardDescription>
                                            </CardHeader>
                                            {createLoginAccount && (
                                                <CardContent className="space-y-4">
                                                    {watchedEmail ? (
                                                        <div className="text-sm text-muted-foreground">
                                                            <span>{t("drivers.form.loginAccount.emailLabel")}: </span>
                                                            <span className="font-medium text-foreground">{watchedEmail}</span>
                                                        </div>
                                                    ) : (
                                                        <p className="text-sm text-warning flex items-center gap-1">
                                                            ⚠ {t("drivers.form.loginAccount.emailMissing")}
                                                        </p>
                                                    )}
                                                    <p className="text-xs text-muted-foreground">{t("masterData.drivers.login.temporaryExplain")}</p>
                                                </CardContent>
                                            )}
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
                                                    <span className="text-muted-foreground">{t("drivers.form.reviewFullNameTh")}</span>
                                                    <span className="font-medium text-right">{form.getValues("fullNameTh")}</span>
                                                    <span className="text-muted-foreground">{t("drivers.form.reviewMobile")}</span>
                                                    <span className="font-medium text-right">{form.getValues("mobile")}</span>
                                                    <span className="text-muted-foreground">{t("drivers.form.reviewIdCard")}</span>
                                                    <span className="font-medium text-right">{form.getValues("idCard")}</span>
                                                    <span className="text-muted-foreground">{t("drivers.form.reviewLicenseId")}</span>
                                                    <span className="font-medium text-right">{form.getValues("truckLicenseId")}</span>
                                                    <span className="text-muted-foreground">{t("drivers.form.reviewEmployment")}</span>
                                                    <span className="font-medium text-right">{t(`drivers.form.type.${({ FULL_TIME: "fullTime", PART_TIME: "partTime", SUBCONTRACTOR: "subcontractor" } as const)[form.getValues("employmentType")]}`)}</span>
                                                </div>
                                                <div className="flex gap-2 text-sm text-muted-foreground bg-primary/10 p-3 rounded-md">
                                                    <CheckCircle2 className="h-4 w-4 text-primary" />
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
                    <div data-form-action-bar className="sticky bottom-0 z-sticky flex flex-col gap-3 border-t border-border bg-background p-4 md:flex-row md:items-center md:justify-between">
                        <Button type="button" variant="outline" onClick={currentStep === 1 ? () => router.push('/app/drivers') : prevStep} className="bg-background">
                            {currentStep === 1 ? t("drivers.form.cancel") : t("drivers.form.back")}
                        </Button>
                        <Button type="button" className="min-w-[140px]" onClick={currentStep === 4 ? form.handleSubmit(onSubmit, createInvalidHandler(form, t)) : nextStep} disabled={isSubmitting}>
                            {isSubmitting ? <><Loader2 className="mr-2 h-4 w-4 animate-spin" /> {t("drivers.form.saving")}</> : currentStep === 4 ? t("drivers.form.completeRegistration") : t("drivers.form.nextStep")}
                        </Button>
                    </div>
                </div>
            </div>
        </div>
    );
}

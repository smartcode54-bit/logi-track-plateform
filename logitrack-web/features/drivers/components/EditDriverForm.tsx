"use client";

import { DriverFormMessage as FormMessage } from "./DriverFormMessage";
import { useForm } from "react-hook-form";
import { driverFormResolver, driverInvalidHandler as createInvalidHandler } from "./driverFormResolver";
import { Driver } from "@/validate/driverSchema";
import { driverFormValues, isDriverLoginOutOfReach, type DriverDetailDTO } from "@/features/drivers/api/drivers";
import { useCreateDriverLogin, useDriver, useDriverWrites } from "@/features/drivers/api/useDrivers";
import { useSubcontractorOptions } from "@/features/subcontractors/api/useSubcontractors";
import { carrierName } from "@/features/subcontractors/api/subcontractors";
import { applyServerFieldErrors, toastSaveError } from "@/lib/formServerErrors";
import type { CreateUserResult } from "@/features/users/api/users";
import { TemporaryPasswordNotice } from "./TemporaryPasswordNotice";
import { useRouter, useSearchParams } from "next/navigation";
import { useState, useEffect } from "react";
import { RequiredMark } from "@/components/ui/required-mark";
import { Alert, AlertTitle, AlertDescription } from "@/components/ui/alert";
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
    FileText, Truck, Save, X, Building2, KeyRound, ShieldCheck, ShieldOff,
} from "lucide-react";
import Link from "next/link";
import { toast } from "sonner";
import { DatePicker } from "@/components/ui/date-picker";
import { DriverBirthDatePicker } from "./DriverBirthDatePicker";
import { format } from "date-fns";
import Image from "next/image";
import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
import { assertDriverUpload, driverUploadAccept, DriverUploadError } from "../api/driverUploads";
import { driverDisplayName } from "@/lib/driverName";
import { PageLoading } from "@/components/page-states";
import { CustomerDriverIdsFields } from "./CustomerDriverIdsFields";
import { RecordState, recordUnavailable } from "@/components/record-state";

/**
 * Reset() replaces every value, so a key absent from the Firestore doc becomes `undefined`
 * rather than falling back to defaultValues. Older driver docs predate required fields such as
 * fullNameTh, which then fail validation with no visible cause. Merge these under the fetched doc.
 */
const FORM_DEFAULTS: Partial<Driver> = {
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
    customerDriverIds: {},
};

export default function EditDriverForm() {
    const router = useRouter();
    const searchParams = useSearchParams();
    const driverId = searchParams.get('id');
    const { t } = useLanguage();

    // `['drivers','detail',id]` (T25) and the writes, by the `masterdata` flag; logins always go
    // through `POST /v1/users` (users are PostgreSQL's from P0, R49).
    const driverQuery = useDriver(driverId ?? "");
    const { update } = useDriverWrites();
    // The temporary password stays in this component's state only (never a query or mutation cache).
    const createLogin = useCreateDriverLogin();
    const isSubmitting = update.isPending;
    const isLoading = driverQuery.isPending && Boolean(driverId);
    const [loaded, setLoaded] = useState<DriverDetailDTO | null>(null);

    // Login account state
    const [showCreateAccount, setShowCreateAccount] = useState(false);
    const [accountEmail, setAccountEmail] = useState("");
    const [createdLogin, setCreatedLogin] = useState<CreateUserResult | null>(null);
    const isCreatingAccount = createLogin.isPending;

    // File states - for NEW uploads
    const [selectedImage, setSelectedImage] = useState<File | null>(null);
    const [imagePreview, setImagePreview] = useState<string | null>(null);
    const [profileRemoved, setProfileRemoved] = useState(false);
    const [idCardFile, setIdCardFile] = useState<File | null>(null);
    const [truckLicenseFile, setTruckLicenseFile] = useState<File | null>(null);

    // Existing URLs (for display if not changing)
    const [existingIdCardImage, setExistingIdCardImage] = useState<string | null>(null);
    const [existingTruckLicenseImage, setExistingTruckLicenseImage] = useState<string | null>(null);

    // `['subcontractors','options']`: one cached list. The customer driver ids are keyed by customer code; the
    // save resolves the codes the driver does not hold yet by search (`updateDriver`, UI8), no customer list.
    const { options: subcontractors } = useSubcontractorOptions();

    const form = useForm<Driver>({
        resolver: driverFormResolver(),
        defaultValues: FORM_DEFAULTS as Driver,
        mode: "onChange",
    });

    // Reset once when the stored driver arrives (a background refetch must not overwrite edits).
    useEffect(() => {
        const driver = driverQuery.data;
        if (!driver || loaded?.id === driver.id) return;
        const values = driverFormValues(driver);
        form.reset({
            ...FORM_DEFAULTS,
            ...values,
            contractYears: values.contractYears ?? ("" as any),
            customerDriverIds: values.customerDriverIds ?? {},
        } as Driver);
        setImagePreview(driver.profileUrl);
        setExistingIdCardImage(driver.idCardUrl ?? null);
        setExistingTruckLicenseImage(driver.licenseUrl ?? null);
        setAccountEmail(driver.email ?? "");
        setLoaded(driver);
    }, [driverQuery.data, loaded, form]);

    const handleImageChange = (e: React.ChangeEvent<HTMLInputElement>) => {
        const file = e.target.files?.[0];
        if (file) {
            try { assertDriverUpload(file, "driver_profile"); } catch (error) {
                if (error instanceof DriverUploadError) toast.error(t(`drivers.upload.${error.reason}`, { file: error.fileName }));
                e.target.value = "";
                return;
            }
            setSelectedImage(file);
            setProfileRemoved(false);
            const reader = new FileReader();
            reader.onloadend = () => setImagePreview(reader.result as string);
            reader.readAsDataURL(file);
        }
    };

    const removeImage = () => {
        setSelectedImage(null);
        setImagePreview(null);
        setProfileRemoved(true);
        form.setValue("profileImage", undefined);
    };

    const handleCreateAccount = async () => {
        if (!loaded || !accountEmail.trim()) return;
        try {
            // `POST /v1/users {role:"driver", driverId}`: Go generates a temporary password, shown once (R29).
            // In the driver's tenant (the carrier of a subcontractor driver), where its link must be.
            const { id, tenantId, mobile, fullNameTh, firstName, lastName, displayName } = loaded;
            const result = await createLogin.create({
                driver: { id, tenantId, mobile, fullNameTh, firstName, lastName, displayName },
                email: accountEmail.trim(),
            });
            setCreatedLogin(result);
            setShowCreateAccount(false);
            toast.success(t("drivers.toast.accountCreated"));
        } catch (error) {
            if (isDriverLoginOutOfReach(error)) toast.error(t("masterData.drivers.login.outOfReach"));
            else toastSaveError(error, t, "drivers.toast.accountCreateError");
        }
    };

    const onSubmit = async (data: Driver) => {
        if (!driverId || !loaded) return;
        try {
            await update.mutateAsync({
                id: driverId,
                values: data,
                files: { profile: selectedImage, idCard: idCardFile, license: truckLicenseFile },
                removeProfile: profileRemoved,
                previous: loaded,
            });
            toast.success(t("drivers.toast.updateSuccess"));
            router.push(`/app/drivers/view?id=${driverId}`);
        } catch (error) {
            if (error instanceof DriverUploadError) {
                toast.error(t(`drivers.upload.${error.reason}`, { file: error.fileName }));
                return;
            }
            applyServerFieldErrors(form, error, t, { status: "status", licenseExpiredDate: "truckLicenseExpiredDate", tenantId: "subcontractorId" });
            toastSaveError(error, t, "drivers.toast.updateError");
        }
    };

    const employmentType = form.watch("employmentType");

    // No id, no driver, or the read failed: not found, or a load error with a retry (UI3D), never an empty form.
    // A failed background refetch keeps the loaded record (recordUnavailable); only a missing one shows RecordState.
    if (!driverId || recordUnavailable(driverQuery)) {
        return <RecordState error={driverQuery.error} onRetry={() => void driverQuery.refetch()} retrying={driverQuery.isFetching} />;
    }

    if (isLoading) return <PageLoading />;

    const driverName = driverQuery.data ? driverDisplayName({ ...driverQuery.data }, driverQuery.data.id) : "";

    return (
        <div className="flex max-w-4xl flex-col gap-section">
            <PageHeader
                title={t("nav.drivers")}
                recordName={driverName}
                description={t("drivers.edit.subtitle")}
                back={{ href: `/app/drivers/view?id=${driverId}`, label: t("drivers.edit.backToDetails") }}
            />
            <div className="space-y-6">
                <Form {...form}>
                    <form noValidate onSubmit={form.handleSubmit(onSubmit, createInvalidHandler(form, t))} className="space-y-6">
                        <Card>
                            <CardHeader>
                                <CardTitle className="flex items-center gap-2">
                                    <User className="h-5 w-5 text-primary" />
                                    {t("drivers.form.personalInfo")}
                                </CardTitle>
                            </CardHeader>
                            <CardContent className="space-y-6">
                                <div className="flex justify-center mb-6">
                                    <div className="relative group">
                                        <div className="w-32 h-32 rounded-full border-2 border-dashed border-border flex items-center justify-center overflow-hidden bg-muted relative">
                                            {imagePreview ? (
                                                <Image src={imagePreview} alt={t("drivers.detail.profilePhoto")} fill className="object-cover" />
                                            ) : (
                                                <label htmlFor="p-upload" className="flex flex-col items-center justify-center cursor-pointer w-full h-full">
                                                    <Upload className="h-8 w-8 text-muted-foreground mb-2" />
                                                    <span className="text-xs text-muted-foreground">{t("drivers.edit.changePhoto")}</span>
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
                                        <FormItem className="flex flex-col">
                                            <FormLabel>{t("drivers.form.birthDate")} <RequiredMark /></FormLabel>
                                            <DriverBirthDatePicker
                                                value={field.value ? new Date(field.value) : undefined}
                                                onChange={(date) => field.onChange(date ? format(date, "yyyy-MM-dd") : "")}
                                                minDate={minDate}
                                                maxDate={maxDate}
                                            />
                                            <FormMessage />
                                        </FormItem>
                                    );
                                }} />
                            </CardContent>
                        </Card>

                        <Card>
                            <CardHeader>
                                <CardTitle className="flex items-center gap-2">
                                    <FileText className="h-5 w-5 text-primary" />
                                    {t("drivers.form.identityDocs")}
                                </CardTitle>
                            </CardHeader>
                            <CardContent className="space-y-6">
                                <div className="bg-muted p-4 rounded-lg space-y-4">
                                    <h3 className="font-semibold flex items-center gap-2">{t("drivers.form.nationalIdCard")}</h3>
                                    <FormField control={form.control} name="idCard" render={({ field }) => (
                                        <FormItem><FormLabel>{t("drivers.form.idNumber")} <RequiredMark /></FormLabel><FormControl><Input aria-required="true" placeholder={t("drivers.form.idNumber.placeholder")} maxLength={13} {...field} /></FormControl><FormMessage /></FormItem>
                                    )} />
                                    <div className="grid md:grid-cols-2 gap-4">
                                        <FormField control={form.control} name="idCardExpiredDate" render={({ field }) => (
                                            <FormItem className="flex flex-col">
                                                <FormLabel>{t("drivers.form.expiryDate")}</FormLabel>
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
                                            <FormLabel>{t("drivers.edit.idCardImage")}</FormLabel>
                                            <div className="flex items-center gap-2">
                                                <Input type="file" aria-label={t("drivers.edit.idCardImage")} accept={driverUploadAccept("driver_id_card")} onChange={(e) => {
                                                    const file = e.target.files?.[0];
                                                    if (!file) return;
                                                    try { assertDriverUpload(file, "driver_id_card"); setIdCardFile(file); } catch (error) {
                                                        if (error instanceof DriverUploadError) toast.error(t(`drivers.upload.${error.reason}`, { file: error.fileName }));
                                                        e.target.value = "";
                                                    }
                                                }} />
                                                {idCardFile ? <FileText className="text-success h-5 w-5" /> : existingIdCardImage && <CheckCircle2 className="text-primary h-5 w-5" />}
                                            </div>
                                            {!idCardFile && existingIdCardImage && <p className="text-xs text-muted-foreground mt-1">{t("drivers.edit.currentFileExists")}</p>}
                                        </FormItem>
                                    </div>
                                </div>

                                <div className="bg-muted p-4 rounded-lg space-y-4">
                                    <h3 className="font-semibold flex items-center gap-2">{t("drivers.form.drivingLicense")}</h3>
                                    <div className="grid md:grid-cols-2 gap-4">
                                        <FormField control={form.control} name="truckLicenseId" render={({ field }) => (
                                            <FormItem><FormLabel>{t("drivers.form.licenseNumber")} <RequiredMark /></FormLabel><FormControl><Input aria-required="true" placeholder={t("drivers.form.licenseNumber.placeholder")} maxLength={8} {...field} /></FormControl><FormMessage /></FormItem>
                                        )} />
                                        <FormField control={form.control} name="licenseType" render={({ field }) => (
                                            <FormItem>
                                                <FormLabel>{t("drivers.form.licenseType")}</FormLabel>
                                                <Select onValueChange={field.onChange} value={field.value}>
                                                    <FormControl><SelectTrigger><SelectValue placeholder={t("drivers.form.licenseType.placeholder")} /></SelectTrigger></FormControl>
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
                                                <FormLabel>{t("drivers.form.expiryDate")}</FormLabel>
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
                                            <FormLabel>{t("drivers.edit.licenseImage")}</FormLabel>
                                            <div className="flex items-center gap-2">
                                                <Input type="file" aria-label={t("drivers.edit.licenseImage")} accept={driverUploadAccept("driver_license")} onChange={(e) => {
                                                    const file = e.target.files?.[0];
                                                    if (!file) return;
                                                    try { assertDriverUpload(file, "driver_license"); setTruckLicenseFile(file); } catch (error) {
                                                        if (error instanceof DriverUploadError) toast.error(t(`drivers.upload.${error.reason}`, { file: error.fileName }));
                                                        e.target.value = "";
                                                    }
                                                }} />
                                                {truckLicenseFile ? <FileText className="text-success h-5 w-5" /> : existingTruckLicenseImage && <CheckCircle2 className="text-primary h-5 w-5" />}
                                            </div>
                                            {!truckLicenseFile && existingTruckLicenseImage && <p className="text-xs text-muted-foreground mt-1">{t("drivers.edit.currentFileExists")}</p>}
                                        </FormItem>
                                    </div>
                                </div>
                            </CardContent>
                        </Card>

                        <Card>
                            <CardHeader>
                                <CardTitle className="flex items-center gap-2">
                                    <Truck className="h-5 w-5 text-primary" />
                                    {t("drivers.form.employmentDetails")}
                                </CardTitle>
                            </CardHeader>
                            <CardContent className="space-y-6">
                                <FormField control={form.control} name="employmentType" render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>{t("drivers.form.type")}</FormLabel>
                                        <Select onValueChange={field.onChange} value={field.value}>
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

                                <FormField control={form.control} name="hireDate" render={({ field }) => (
                                    <FormItem className="flex flex-col">
                                        <FormLabel>{t("driverComp.driver.hireDate")}</FormLabel>
                                        <DatePicker
                                            value={field.value ? new Date(field.value) : undefined}
                                            onChange={(date) => field.onChange(date ? format(date, "yyyy-MM-dd") : "")}
                                            fromYear={new Date().getFullYear() - 15}
                                            toYear={new Date().getFullYear()}
                                        />
                                        <FormMessage />
                                    </FormItem>
                                )} />

                                <FormField control={form.control} name="probationPassed" render={({ field }) => (
                                    <FormItem>
                                        <FormLabel className="flex items-center gap-2">
                                            <input type="checkbox" checked={!!field.value} onChange={(e) => field.onChange(e.target.checked)} />
                                            {t("driverComp.driver.probationPassed")}
                                        </FormLabel>
                                        <FormMessage />
                                    </FormItem>
                                )} />

                                <FormField control={form.control} name="status" render={({ field }) => (
                                    <FormItem>
                                        <FormLabel>{t("drivers.edit.status")}</FormLabel>
                                        <Select onValueChange={field.onChange} value={field.value}>
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

                        <Card>
                            <CardHeader>
                                <CardTitle className="flex items-center gap-2">
                                    <Building2 className="h-5 w-5 text-primary" />
                                    {t("drivers.form.customerDriverIds")}
                                </CardTitle>
                                <CardDescription>
                                    {t("drivers.form.customerDriverIdsDesc")}
                                </CardDescription>
                            </CardHeader>
                            <CardContent className="space-y-6">
                                <CustomerDriverIdsFields key={loaded?.id} form={form} existing={loaded?.customerDriverIds ?? []} />
                            </CardContent>
                        </Card>

                        {/* Auth Account Card */}
                        <Card>
                            <CardHeader>
                                <CardTitle className="flex items-center gap-2 text-base">
                                    <KeyRound className="h-4 w-4 text-primary" />
                                    {t("drivers.form.loginAccount")}
                                </CardTitle>
                            </CardHeader>
                            <CardContent>
                                {createdLogin ? (
                                    <TemporaryPasswordNotice email={createdLogin.user.email ?? accountEmail} temporaryPassword={createdLogin.temporaryPassword} />
                                ) : loaded?.login || loaded?.userId || loaded?.legacyAuthUid ? (
                                    <Alert variant="success">
                                        <ShieldCheck className="h-5 w-5" />
                                        <AlertTitle>{t("drivers.form.loginAccount.linked")}</AlertTitle>
                                        <AlertDescription className="space-y-1">
                                            <p className="text-xs font-mono break-all">{t("drivers.form.loginAccount.linkedUid", { uid: loaded?.login?.email || loaded?.login?.userId || loaded?.userId || loaded?.legacyAuthUid || "" })}</p>
                                            <p className="text-xs">{t("drivers.form.loginAccount.securityNote")}</p>
                                        </AlertDescription>
                                    </Alert>
                                ) : (
                                    <div className="space-y-4">
                                        <Alert variant="warning">
                                            <ShieldOff className="h-5 w-5" />
                                            <AlertDescription>{t("drivers.form.loginAccount.noAccount")}</AlertDescription>
                                        </Alert>
                                        {!showCreateAccount ? (
                                            <Button type="button" variant="outline" size="sm" onClick={() => setShowCreateAccount(true)}>
                                                <KeyRound className="h-4 w-4 mr-2" />
                                                {t("drivers.form.loginAccount.createBtn")}
                                            </Button>
                                        ) : (
                                            <div className="space-y-3 p-4 border rounded-lg">
                                                <div className="space-y-1">
                                                    <label className="text-sm font-medium">{t("drivers.form.loginAccount.emailLabel")}</label>
                                                    <Input
                                                        type="email"
                                                        placeholder={t("drivers.form.email.placeholder")}
                                                        value={accountEmail}
                                                        onChange={e => setAccountEmail(e.target.value)}
                                                    />
                                                    <p className="text-xs text-muted-foreground">{t("masterData.drivers.login.temporaryExplain")}</p>
                                                </div>
                                                <div className="flex gap-2">
                                                    <Button
                                                        type="button"
                                                        size="sm"
                                                        onClick={handleCreateAccount}
                                                        disabled={isCreatingAccount || !accountEmail.trim()}
                                                    >
                                                        {isCreatingAccount ? (
                                                            <><Loader2 className="h-4 w-4 animate-spin mr-2" />{t("drivers.form.saving")}</>
                                                        ) : (
                                                            t("drivers.form.loginAccount.createBtn")
                                                        )}
                                                    </Button>
                                                    <Button type="button" variant="ghost" size="sm" onClick={() => setShowCreateAccount(false)}>
                                                        {t("drivers.form.cancel")}
                                                    </Button>
                                                </div>
                                            </div>
                                        )}
                                    </div>
                                )}
                            </CardContent>
                        </Card>

                        <div data-form-action-bar className="sticky bottom-0 z-sticky flex flex-col gap-3 border-t border-border bg-background p-4 md:flex-row md:items-center md:justify-end">
                            <Button type="button" variant="outline" asChild>
                                <Link href={`/app/drivers/view?id=${driverId}`} prefetch={false}>{t("drivers.form.cancel")}</Link>
                            </Button>
                            <Button
                                type="submit"
                                className="flex items-center gap-2"
                                disabled={isSubmitting}
                            >
                                {isSubmitting ? (
                                    <>
                                        <Loader2 className="h-4 w-4 animate-spin" />
                                        {t("drivers.form.saving")}
                                    </>
                                ) : (
                                    <>
                                        <Save className="h-4 w-4" />
                                        {t("drivers.edit.saveChanges")}
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

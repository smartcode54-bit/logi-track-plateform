"use client";

import { useForm, type FieldPath } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { createInvalidHandler } from "@/lib/formInvalidHandler";
import { subcontractorSchema, SubcontractorFormValues, subcontractorDefaultValues } from "@/validate/subcontractorSchema";
import { useSaveSubcontractor } from "../../api/useSubcontractors";
import { applyServerFieldErrors, toastSaveError } from "@/lib/formServerErrors";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import {
    Form,
    FormControl,
    FormField,
    FormItem,
    FormLabel,
    FormMessage,
    FormDescription,
} from "@/components/ui/form";
import { RequiredMark } from "@/components/ui/required-mark";
import { Input } from "@/components/ui/input";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { ArrowLeft, Truck, CheckCircle2 } from "lucide-react";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { Checkbox } from "@/components/ui/checkbox";
import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";

export default function NewSubcontractorWizard() {
    const router = useRouter();
    const { t } = useLanguage();
    const [currentStep, setCurrentStep] = useState(1);
    const { create } = useSaveSubcontractor();
    const isSubmitting = create.isPending;
    const [serviceRegionsText, setServiceRegionsText] = useState(() => (subcontractorDefaultValues.serviceRegions ?? []).join(", "));

    const STEPS = [
        { id: 1, title: t("subcontractors.step1.title"), description: t("subcontractors.step1.description") },
        { id: 2, title: t("subcontractors.step2.title"), description: t("subcontractors.step2.description") },
        { id: 3, title: t("subcontractors.step3.title"), description: t("subcontractors.step3.description") },
        { id: 4, title: t("subcontractors.ui4.reviewTitle"), description: t("subcontractors.ui4.documentsAfterCreate") },
    ];

    const form = useForm<SubcontractorFormValues>({
        resolver: zodResolver(subcontractorSchema),
        defaultValues: subcontractorDefaultValues,
        mode: "onChange",
    });

    const onSubmit = async (data: SubcontractorFormValues) => {
        const fleetSize = Number(data.fleetSize ?? 0);
        if (!Number.isFinite(fleetSize) || !Number.isInteger(fleetSize) || fleetSize < 0) {
            form.setError("fleetSize", { type: "validate", message: t("subcontractors.form.totalTrucks.integer") });
            return;
        }
        try {
            const values = {
                ...data,
                fleetSize,
                serviceRegions: serviceRegionsText.split(",").map((region) => region.trim()).filter(Boolean),
            };
            delete values.documents;
            const id = await create.mutateAsync({ values });
            toast.success(t("subcontractors.toast.registerSuccess"));
            router.push(`/app/subcontractors/${id}/edit`);
        } catch (error) {
            applyServerFieldErrors(form, error, t, { nameTh: "name", legalType: "type" });
            toastSaveError(error, t, "subcontractors.toast.registerError");
        }
    };

    const nextStep = async () => {
        let fieldsToValidate: FieldPath<SubcontractorFormValues>[] = [];

        if (currentStep === 1) {
            fieldsToValidate = ['name', 'type', 'idCardNumber', 'taxId', 'website', 'address'];
        } else if (currentStep === 2) {
            fieldsToValidate = ['contactPerson', 'designation', 'phone', 'email', 'lineGroupId'];
        } else if (currentStep === 3) {
            form.setValue("serviceRegions", serviceRegionsText.split(",").map((region) => region.trim()).filter(Boolean));
            fieldsToValidate = ['fleetSize', 'dispatchCenter', 'serviceRegions', 'vehicleTypes'];
        }

        const isValid = await form.trigger(fieldsToValidate);
        if (isValid && currentStep === 3) {
            const fleetSize = Number(form.getValues("fleetSize") ?? 0);
            if (!Number.isFinite(fleetSize) || !Number.isInteger(fleetSize) || fleetSize < 0) {
                form.setError("fleetSize", { type: "validate", message: t("subcontractors.form.totalTrucks.integer") });
                return;
            }
        }
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
        <div className="flex flex-col gap-section">
            {/* Sticky Header */}
            <div className="sticky top-16 z-raised border-b bg-background pb-4">
                <PageHeader
                    title={t("nav.partners")}
                    recordName={t("subcontractors.new.title")}
                    description={`${t("subcontractors.new.step")} ${currentStep} ${t("subcontractors.new.of")} ${STEPS.length}: ${STEPS[currentStep - 1].title}`}
                    back={{ href: "/app/subcontractors" }}
                />
            </div>

            <div className="flex flex-1 items-stretch">
                {/* Sidebar Steps */}
                <div className="w-56 shrink-0 border-r border-border bg-muted p-4 hidden md:block">
                    <div className="space-y-6">
                        {STEPS.map((step) => {
                            const isActive = step.id === currentStep;
                            const isCompleted = step.id < currentStep;

                            return (
                                <div key={step.id} className={cn("flex items-start gap-3 relative", isActive ? "text-foreground" : "text-muted-foreground")}>
                                    <div className={cn(
                                        "flex items-center justify-center w-8 h-8 rounded-md border text-sm font-semibold z-raised bg-background",
                                        isActive ? "border-primary text-primary" : "",
                                        isCompleted ? "border-primary bg-primary text-primary-foreground" : "border-control-edge text-muted-foreground"
                                    )}>
                                        {isCompleted ? <CheckCircle2 className="h-5 w-5" /> : step.id}
                                    </div>
                                    {step.id !== STEPS.length && (
                                        <div className="absolute left-4 top-8 bottom-[-24px] w-px bg-border" />
                                    )}
                                    <div className="pt-1">
                                        <p className={cn("text-sm font-medium", isActive ? "text-primary" : "")}>{step.title}</p>
                                        <p className="text-xs text-muted-foreground">{step.description}</p>
                                    </div>
                                </div>
                            );
                        })}
                    </div>

                    {/* Tips Card */}
                    <div className="mt-8 p-4 bg-muted border border-border rounded-lg">
                        <div className="flex items-center gap-2 mb-2 text-primary ">
                            <span className="text-sm font-medium">{t("subcontractors.tips")}</span>
                        </div>
                        <p className="text-xs text-muted-foreground leading-relaxed">
                            {t("subcontractors.tips.taxId")}
                        </p>
                    </div>
                </div>

                {/* Main Form Area */}
                <div className="min-w-0 flex-1 md:pl-6">
                    <div className="max-w-3xl">
                        <Form {...form}>
                            <form onSubmit={form.handleSubmit(onSubmit, createInvalidHandler(form, t))} className="space-y-8">
                                {/* Step 1: Company Profile */}
                                {currentStep === 1 && (
                                    <div className="space-y-6">
                                        <Card>
                                            <CardHeader>
                                                <CardTitle className="text-base">{t("subcontractors.form.companyProfile")}</CardTitle>
                                                <CardDescription>{t("subcontractors.form.companyProfile.desc")}</CardDescription>
                                            </CardHeader>
                                            <CardContent className="space-y-6">
                                                <div className="grid md:grid-cols-2 gap-6">
                                                    <FormField
                                                        control={form.control}
                                                        name="name"
                                                        render={({ field }) => (
                                                            <FormItem>
                                                                 <FormLabel>{t("subcontractors.form.legalName")} <RequiredMark /></FormLabel>
                                                                 <FormControl>
                                                                     <Input aria-required="true" placeholder={t("subcontractors.form.legalName.placeholder")} {...field} />
                                                                 </FormControl>
                                                                 <FormMessage />
                                                            </FormItem>
                                                        )}
                                                    />
                                                    <FormField
                                                        control={form.control}
                                                        name="type"
                                                        render={({ field }) => (
                                                            <FormItem>
                                                                 <FormLabel>{t("subcontractors.form.type")}</FormLabel>
                                                                 <Select onValueChange={field.onChange} defaultValue={field.value}>
                                                                     <FormControl>
                                                                         <SelectTrigger>
                                                                             <SelectValue placeholder={t("subcontractors.form.type.placeholder")} />
                                                                         </SelectTrigger>
                                                                     </FormControl>
                                                                     <SelectContent>
                                                                         <SelectItem value="individual">{t("subcontractors.form.type.individual")}</SelectItem>
                                                                         <SelectItem value="company">{t("subcontractors.form.type.company")}</SelectItem>
                                                                     </SelectContent>
                                                                 </Select>
                                                                 <FormMessage />
                                                            </FormItem>
                                                        )}
                                                    />
                                                </div>

                                                <div className="grid md:grid-cols-2 gap-6">
                                                    {form.watch("type") === "company" && (
                                                        <FormField
                                                            control={form.control}
                                                            name="taxId"
                                                            render={({ field }) => (
                                                                <FormItem>
                                                                     <FormLabel>{t("subcontractors.form.taxId")} <RequiredMark /></FormLabel>
                                                                     <FormControl>
                                                                         <Input aria-required="true" placeholder={t("subcontractors.form.taxId.placeholder")} {...field} />
                                                                     </FormControl>
                                                                     <FormMessage />
                                                                </FormItem>
                                                            )}
                                                        />
                                                    )}
                                                    {form.watch("type") === "individual" && (
                                                        <FormField
                                                            control={form.control}
                                                            name="idCardNumber"
                                                            render={({ field }) => (
                                                                <FormItem>
                                                                     <FormLabel>{t("subcontractors.form.nationalId")} <RequiredMark /></FormLabel>
                                                                     <FormControl>
                                                                         <Input aria-required="true" placeholder={t("subcontractors.form.nationalId.placeholder")} {...field} />
                                                                     </FormControl>
                                                                     <FormMessage />
                                                                </FormItem>
                                                            )}
                                                        />
                                                    )}
                                                </div>

                                                <FormField
                                                    control={form.control}
                                                    name="website"
                                                    render={({ field }) => (
                                                        <FormItem>
                                                             <FormLabel>{t("subcontractors.form.website")}</FormLabel>
                                                             <FormControl>
                                                                 <Input placeholder={t("subcontractors.form.website.placeholder")} {...field} />
                                                             </FormControl>
                                                             <FormMessage />
                                                        </FormItem>
                                                    )}
                                                />

                                                <FormField
                                                    control={form.control}
                                                    name="address"
                                                    render={({ field }) => (
                                                        <FormItem>
                                                             <FormLabel>{t("subcontractors.form.address")}</FormLabel>
                                                             <FormControl>
                                                                 <Textarea
                                                                     placeholder={t("subcontractors.form.address.placeholder")}
                                                                     className="min-h-[100px]"
                                                                     {...field}
                                                                 />
                                                             </FormControl>
                                                             <FormMessage />
                                                        </FormItem>
                                                    )}
                                                />
                                            </CardContent>
                                        </Card>
                                    </div>
                                )}

                                {/* Step 2: Contact Details */}
                                {currentStep === 2 && (
                                    <div className="space-y-6">
                                        <Card>
                                            <CardHeader>
                                                <CardTitle className="text-base">{t("subcontractors.form.contactInfo")}</CardTitle>
                                                <CardDescription>{t("subcontractors.form.contactInfo.desc")}</CardDescription>
                                            </CardHeader>
                                            <CardContent className="space-y-6">
                                                <FormField
                                                    control={form.control}
                                                    name="contactPerson"
                                                    render={({ field }) => (
                                                        <FormItem>
                                                             <FormLabel>{t("subcontractors.form.fullName")} <RequiredMark /></FormLabel>
                                                             <FormControl>
                                                                 <Input aria-required="true" placeholder={t("subcontractors.form.fullName.placeholder")} {...field} />
                                                             </FormControl>
                                                             <FormMessage />
                                                        </FormItem>
                                                    )}
                                                />

                                                <FormField
                                                    control={form.control}
                                                    name="designation"
                                                    render={({ field }) => (
                                                        <FormItem>
                                                             <FormLabel>{t("subcontractors.form.designation")}</FormLabel>
                                                             <FormControl>
                                                                 <Input placeholder={t("subcontractors.form.designation.placeholder")} {...field} />
                                                             </FormControl>
                                                             <FormMessage />
                                                        </FormItem>
                                                    )}
                                                />

                                                <div className="grid md:grid-cols-2 gap-6">
                                                    <FormField
                                                        control={form.control}
                                                        name="phone"
                                                        render={({ field }) => (
                                                            <FormItem>
                                                                 <FormLabel>{t("subcontractors.form.mobilePhone")} <RequiredMark /></FormLabel>
                                                                 <FormControl>
                                                                     <Input aria-required="true" placeholder={t("subcontractors.form.mobilePhone.placeholder")} {...field} />
                                                                 </FormControl>
                                                                 <FormMessage />
                                                            </FormItem>
                                                        )}
                                                    />
                                                    <FormField
                                                        control={form.control}
                                                        name="email"
                                                        render={({ field }) => (
                                                            <FormItem>
                                                                 <FormLabel>{t("subcontractors.form.workEmail")}</FormLabel>
                                                                 <FormControl>
                                                                     <Input placeholder={t("subcontractors.form.workEmail.placeholder")} {...field} />
                                                                 </FormControl>
                                                                 <FormMessage />
                                                            </FormItem>
                                                        )}
                                                    />
                                                </div>

                                                <FormField
                                                    control={form.control}
                                                    name="lineGroupId"
                                                    render={({ field }) => (
                                                        <FormItem>
                                                             <FormLabel>{t("subcontractors.form.lineGroupId")}</FormLabel>
                                                             <FormControl>
                                                                 <Input placeholder={t("subcontractors.form.lineGroupId.placeholder")} {...field} value={field.value ?? ""} />
                                                             </FormControl>
                                                             <FormDescription>{t("subcontractors.form.lineGroupId.desc")}</FormDescription>
                                                             <FormMessage />
                                                        </FormItem>
                                                    )}
                                                />
                                            </CardContent>
                                        </Card>
                                    </div>
                                )}

                                {/* Step 3: Fleet & Service Area */}
                                {currentStep === 3 && (
                                    <div className="space-y-6">
                                        <Card>
                                            <CardHeader>
                                                <CardTitle className="text-base">{t("subcontractors.form.fleetService")}</CardTitle>
                                                <CardDescription>{t("subcontractors.form.fleetService.desc")}</CardDescription>
                                            </CardHeader>
                                            <CardContent className="space-y-8">
                                                <div>
                                                    <h3 className="text-sm font-semibold mb-4 flex items-center gap-2"><Truck className="w-4 h-4 text-primary" /> {t("subcontractors.form.fleetCapacity")}</h3>
                                                    <div className="grid md:grid-cols-2 gap-6">
                                                        <FormField
                                                            control={form.control}
                                                            name="fleetSize"
                                                            render={({ field }) => (
                                                                <FormItem>
                                                                     <FormLabel>{t("subcontractors.form.totalTrucks")}</FormLabel>
                                                                     <FormControl>
                                                                         <Input type="number" min={0} step={1} {...field} value={Number(field.value ?? 0)} />
                                                                     </FormControl>
                                                                     <FormDescription>{t("subcontractors.form.totalTrucks.desc")}</FormDescription>
                                                                     <FormMessage />
                                                                </FormItem>
                                                            )}
                                                        />
                                                        <FormField
                                                            control={form.control}
                                                            name="dispatchCenter"
                                                            render={({ field }) => (
                                                                <FormItem>
                                                                     <FormLabel>{t("subcontractors.form.dispatchCenter")}</FormLabel>
                                                                     <FormControl>
                                                                         <Input {...field} />
                                                                     </FormControl>
                                                                     <FormMessage />
                                                                </FormItem>
                                                            )}
                                                        />
                                                    </div>
                                                </div>

                                                <FormField
                                                    control={form.control}
                                                    name="serviceRegions"
                                                    render={({ field }) => (
                                                        <FormItem>
                                                            <FormLabel>{t("subcontractors.form.serviceRegions")}</FormLabel>
                                                            <FormControl>
                                                                <Input value={serviceRegionsText}
                                                                    onChange={(event) => setServiceRegionsText(event.target.value)}
                                                                    onBlur={() => {
                                                                        field.onChange(serviceRegionsText.split(",").map((region) => region.trim()).filter(Boolean));
                                                                        field.onBlur();
                                                                    }} />
                                                            </FormControl>
                                                            <FormDescription>{t("subcontractors.form.serviceRegions.search")}</FormDescription>
                                                            <FormMessage />
                                                        </FormItem>
                                                    )}
                                                />

                                                <div>
                                                    <h3 className="text-sm font-semibold mb-4 flex items-center gap-2"><Truck className="w-4 h-4 text-primary" /> {t("subcontractors.form.vehicleTypes")}</h3>
                                                    <FormField
                                                        control={form.control}
                                                        name="vehicleTypes"
                                                        render={() => (
                                                            <FormItem>
                                                                 <div className="grid md:grid-cols-3 gap-4">
                                                                     {[
                                                                         { id: "4-wheeler", label: t("subcontractors.form.vehicle.4wheeler"), sub: t("subcontractors.form.vehicle.4wheeler.desc") },
                                                                         { id: "6-wheeler", label: t("subcontractors.form.vehicle.6wheeler"), sub: t("subcontractors.form.vehicle.6wheeler.desc") },
                                                                         { id: "10-wheeler", label: t("subcontractors.form.vehicle.10wheeler"), sub: t("subcontractors.form.vehicle.10wheeler.desc") },
                                                                         { id: "cold-chain", label: t("subcontractors.form.vehicle.coldChain"), sub: t("subcontractors.form.vehicle.coldChain.desc") },
                                                                         { id: "flatbed", label: t("subcontractors.form.vehicle.flatbed"), sub: t("subcontractors.form.vehicle.flatbed.desc") },
                                                                         { id: "hazmat", label: t("subcontractors.form.vehicle.hazmat"), sub: t("subcontractors.form.vehicle.hazmat.desc") },
                                                                     ].map((item) => (
                                                                         <FormField
                                                                             key={item.id}
                                                                             control={form.control}
                                                                             name="vehicleTypes"
                                                                             render={({ field }) => {
                                                                                 return (
                                                                                     <FormItem
                                                                                         key={item.id}
                                                                                         className={cn(
                                                                                             "flex flex-row items-start space-x-3 space-y-0 rounded-md border p-4 shadow-sm transition-all cursor-pointer hover:border-primary",
                                                                                             field.value?.includes(item.id) ? "border-primary bg-muted " : "border-border "
                                                                                         )}
                                                                                     >
                                                                                         <FormControl>
                                                                                             <Checkbox
                                                                                                 checked={field.value?.includes(item.id)}
                                                                                                 onCheckedChange={(checked) => {
                                                                                                     const currentValues = field.value || [];
                                                                                                     return checked === true
                                                                                                         ? field.onChange([...currentValues, item.id])
                                                                                                         : field.onChange(
                                                                                                             currentValues.filter(
                                                                                                                 (value) => value !== item.id
                                                                                                             )
                                                                                                         )
                                                                                                 }}
                                                                                             />
                                                                                         </FormControl>
                                                                                         <div className="space-y-1 leading-none">
                                                                                             <FormLabel className="font-semibold cursor-pointer">
                                                                                                 {item.label}
                                                                                             </FormLabel>
                                                                                             <FormDescription>
                                                                                                 {item.sub}
                                                                                             </FormDescription>
                                                                                         </div>
                                                                                     </FormItem>
                                                                                 )
                                                                             }}
                                                                         />
                                                                     ))}
                                                                 </div>
                                                            </FormItem>
                                                        )}
                                                    />
                                                </div>
                                            </CardContent>
                                        </Card>
                                    </div>
                                )}

                                {/* Step 4: Review */}
                                {currentStep === 4 && (
                                    <Card>
                                        <CardHeader>
                                            <CardTitle className="text-base">{t("subcontractors.ui4.reviewTitle")}</CardTitle>
                                            <CardDescription>{t("subcontractors.ui4.documentsAfterCreate")}</CardDescription>
                                        </CardHeader>
                                        <CardContent className="space-y-6">
                                            <section className="space-y-2">
                                                <div className="flex items-center justify-between gap-3">
                                                    <h2 className="text-sm font-medium">{t("subcontractors.form.companyDetails")}</h2>
                                                    <Button variant="link" size="sm" type="button" aria-label={t("subcontractors.form.editCompanyDetails")} onClick={() => setCurrentStep(1)}>{t("subcontractors.form.edit")}</Button>
                                                </div>
                                                <p className="text-sm font-medium">{form.getValues("name") || t("subcontractors.form.notEntered")}</p>
                                                <p className="text-sm text-muted-foreground">{t("subcontractors.form.taxId")}: {form.getValues("taxId") || form.getValues("idCardNumber") || "-"}</p>
                                                <p className="break-words text-sm text-muted-foreground">{form.getValues("address") || t("subcontractors.form.noAddress")}</p>
                                            </section>
                                            <section className="space-y-2 border-t border-border pt-4">
                                                <div className="flex items-center justify-between gap-3">
                                                    <h2 className="text-sm font-medium">{t("subcontractors.form.primaryContact")}</h2>
                                                    <Button variant="link" size="sm" type="button" aria-label={t("subcontractors.form.editContactDetails")} onClick={() => setCurrentStep(2)}>{t("subcontractors.form.edit")}</Button>
                                                </div>
                                                <p className="text-sm font-medium">{form.getValues("contactPerson") || "-"}</p>
                                                <p className="break-words text-sm text-muted-foreground">{form.getValues("email") || "-"}</p>
                                                <p className="text-sm text-muted-foreground">{form.getValues("phone") || "-"}</p>
                                            </section>
                                            <section className="space-y-2 border-t border-border pt-4">
                                                <div className="flex items-center justify-between gap-3">
                                                    <h2 className="text-sm font-medium">{t("subcontractors.form.operationalInfo")}</h2>
                                                    <Button variant="link" size="sm" type="button" aria-label={t("subcontractors.form.editOperationalInfo")} onClick={() => setCurrentStep(3)}>{t("subcontractors.form.edit")}</Button>
                                                </div>
                                                <dl className="grid grid-cols-2 gap-2 text-sm">
                                                    <dt className="text-muted-foreground">{t("subcontractors.table.fleetSize")}</dt>
                                                    <dd className="text-right font-medium">{String(form.getValues("fleetSize"))} {t("subcontractors.form.trucks")}</dd>
                                                    <dt className="text-muted-foreground">{t("subcontractors.form.serviceAreas")}</dt>
                                                    <dd className="break-words text-right font-medium">{form.getValues("serviceRegions")?.filter(Boolean).join(", ") || t("subcontractors.ui4.notSet")}</dd>
                                                </dl>
                                            </section>
                                        </CardContent>
                                    </Card>
                                )}
                            </form>
                        </Form>
                    </div>

                    {/* Sticky Footer for Navigation */}
                    <div className="sticky bottom-0 z-sticky border-t border-border bg-background py-4 flex flex-col gap-3 md:flex-row md:justify-between md:items-center">
                        <Button
                            type="button"
                            variant="outline"
                            onClick={currentStep === 1 ? () => router.push('/app/subcontractors') : prevStep}
                        >
                            {currentStep === 1 ? t("subcontractors.form.cancel") : t("subcontractors.form.back")}
                        </Button>

                        <Button
                            type="button"
                            onClick={currentStep === 4 ? form.handleSubmit(onSubmit, createInvalidHandler(form, t)) : nextStep}
                            disabled={isSubmitting}
                        >
                            {isSubmitting ? t("subcontractors.form.saving") : currentStep === 4 ? t("subcontractors.form.completeRegistration") : t("subcontractors.form.nextStep")}
                            {currentStep !== 4 && <ArrowLeft className="ml-2 h-4 w-4 rotate-180" />}
                        </Button>
                    </div>
                </div>
            </div>
        </div>
    );
}

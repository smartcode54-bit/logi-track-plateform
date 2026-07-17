"use client";

import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { createInvalidHandler } from "@/lib/formInvalidHandler";
import { subcontractorSchema, SubcontractorFormValues, SubcontractorValidatedData, subcontractorDefaultValues } from "@/validate/subcontractorSchema";
import { createSubcontractor } from "../../services/subcontractorService";
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
import { ArrowLeft, Check, Upload, Building, Phone, Truck, FileText, Globe, Smartphone, Mail, MapPin, CheckCircle2 } from "lucide-react";
import Link from "next/link";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { Checkbox } from "@/components/ui/checkbox";
import { useLanguage } from "@/context/language";

export default function NewSubcontractorWizard() {
    const router = useRouter();
    const { t } = useLanguage();
    const [currentStep, setCurrentStep] = useState(1);
    const [isSubmitting, setIsSubmitting] = useState(false);

    const STEPS = [
        { id: 1, title: t("subcontractors.step1.title"), description: t("subcontractors.step1.description") },
        { id: 2, title: t("subcontractors.step2.title"), description: t("subcontractors.step2.description") },
        { id: 3, title: t("subcontractors.step3.title"), description: t("subcontractors.step3.description") },
        { id: 4, title: t("subcontractors.step4.title"), description: t("subcontractors.step4.description") },
    ];

    const form = useForm<SubcontractorFormValues>({
        resolver: zodResolver(subcontractorSchema),
        defaultValues: subcontractorDefaultValues,
        mode: "onChange",
    });

    const onSubmit = async (data: SubcontractorFormValues) => {
        try {
            setIsSubmitting(true);
            await createSubcontractor(data as unknown as SubcontractorValidatedData);
            toast.success(t("subcontractors.toast.registerSuccess"));
            router.push("/app/subcontractors");
        } catch (error) {
            console.error(error);
            toast.error(t("subcontractors.toast.registerError"));
        } finally {
            setIsSubmitting(false);
        }
    };

    const nextStep = async () => {
        let fieldsToValidate: any[] = [];

        if (currentStep === 1) {
            fieldsToValidate = ['name', 'type', 'idCardNumber', 'taxId', 'website', 'address'];
        } else if (currentStep === 2) {
            fieldsToValidate = ['contactPerson', 'designation', 'phone', 'email'];
        } else if (currentStep === 3) {
            fieldsToValidate = ['fleetSize', 'dispatchCenter', 'serviceRegions', 'vehicleTypes'];
        }

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
            {/* Sticky Header */}
            <div className="flex items-center px-8 py-6 border-b bg-background sticky top-0 z-10">
                <Button variant="ghost" size="icon" asChild className="mr-4">
                    <Link href="/app/subcontractors" prefetch={false}>
                        <ArrowLeft className="h-5 w-5" />
                    </Link>
                </Button>
                <div>
                    <h1 className="text-2xl font-bold">{t("subcontractors.new.title")}</h1>
                    <p className="text-muted-foreground text-sm">{t("subcontractors.new.step")} {currentStep} {t("subcontractors.new.of")} {STEPS.length}: {STEPS[currentStep - 1].title}</p>
                </div>
            </div>

            <div className="flex flex-1 overflow-hidden">
                {/* Sidebar Steps */}
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

                    {/* Tips Card */}
                    <div className="mt-8 p-4 bg-blue-950/20 border border-blue-900/30 rounded-lg">
                        <div className="flex items-center gap-2 mb-2 text-blue-600 dark:text-blue-400">
                            <div className="h-4 w-4 font-bold border rounded-full flex items-center justify-center text-[10px]">i</div>
                            <span className="text-xs font-bold uppercase tracking-wider">{t("subcontractors.tips")}</span>
                        </div>
                        <p className="text-xs text-muted-foreground leading-relaxed">
                            {t("subcontractors.tips.taxId")}
                        </p>
                    </div>
                </div>

                {/* Main Form Area */}
                <div className="flex-1 overflow-y-auto p-6 md:p-8 relative">
                    <div className="max-w-3xl mx-auto pb-24">
                        <Form {...form}>
                            <form onSubmit={form.handleSubmit(onSubmit, createInvalidHandler(form, t))} className="space-y-8">
                                {/* Step 1: Company Profile */}
                                {currentStep === 1 && (
                                    <div className="space-y-6">
                                        <Card className="border-none shadow-sm">
                                            <CardHeader>
                                                <CardTitle>{t("subcontractors.form.companyProfile")}</CardTitle>
                                                <CardDescription>{t("subcontractors.form.companyProfile.desc")}</CardDescription>
                                            </CardHeader>
                                            <CardContent className="space-y-6">
                                                <div className="grid md:grid-cols-2 gap-6">
                                                    <FormField
                                                        control={form.control}
                                                        name="name"
                                                        render={({ field }) => (
                                                            <FormItem>
                                                                 <FormLabel>{t("subcontractors.form.legalName")}</FormLabel>
                                                                 <FormControl>
                                                                     <Input placeholder={t("subcontractors.form.legalName.placeholder")} {...field} />
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
                                                                     <FormLabel>{t("subcontractors.form.taxId")}</FormLabel>
                                                                     <FormControl>
                                                                         <Input placeholder={t("subcontractors.form.taxId.placeholder")} {...field} />
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
                                                                     <FormLabel>{t("subcontractors.form.nationalId")}</FormLabel>
                                                                     <FormControl>
                                                                         <Input placeholder={t("subcontractors.form.nationalId.placeholder")} {...field} />
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
                                                                 <div className="relative">
                                                                     <Globe className="absolute left-3 top-2.5 h-5 w-5 text-muted-foreground" />
                                                                     <Input className="pl-10" placeholder={t("subcontractors.form.website.placeholder")} {...field} />
                                                                 </div>
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
                                        <Card className="border-none shadow-sm">
                                            <CardHeader>
                                                <CardTitle>{t("subcontractors.form.contactInfo")}</CardTitle>
                                                <CardDescription>{t("subcontractors.form.contactInfo.desc")}</CardDescription>
                                            </CardHeader>
                                            <CardContent className="space-y-6">
                                                <FormField
                                                    control={form.control}
                                                    name="contactPerson"
                                                    render={({ field }) => (
                                                        <FormItem>
                                                             <FormLabel>{t("subcontractors.form.fullName")}</FormLabel>
                                                             <FormControl>
                                                                 <Input placeholder={t("subcontractors.form.fullName.placeholder")} {...field} />
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
                                                                 <FormLabel>{t("subcontractors.form.mobilePhone")}</FormLabel>
                                                                 <FormControl>
                                                                     <div className="relative">
                                                                         <Smartphone className="absolute left-3 top-2.5 h-5 w-5 text-muted-foreground" />
                                                                         <Input className="pl-10" placeholder={t("subcontractors.form.mobilePhone.placeholder")} {...field} />
                                                                     </div>
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
                                                                     <div className="relative">
                                                                         <Mail className="absolute left-3 top-2.5 h-5 w-5 text-muted-foreground" />
                                                                         <Input className="pl-10" placeholder={t("subcontractors.form.workEmail.placeholder")} {...field} />
                                                                     </div>
                                                                 </FormControl>
                                                                 <FormMessage />
                                                            </FormItem>
                                                        )}
                                                    />
                                                </div>
                                            </CardContent>
                                        </Card>
                                    </div>
                                )}

                                {/* Step 3: Fleet & Service Area */}
                                {currentStep === 3 && (
                                    <div className="space-y-6">
                                        <Card className="border-none shadow-sm">
                                            <CardHeader>
                                                <CardTitle>{t("subcontractors.form.fleetService")}</CardTitle>
                                                <CardDescription>{t("subcontractors.form.fleetService.desc")}</CardDescription>
                                            </CardHeader>
                                            <CardContent className="space-y-8">
                                                <div>
                                                    <h3 className="text-sm font-semibold mb-4 flex items-center gap-2"><Truck className="w-4 h-4 text-blue-600" /> {t("subcontractors.form.fleetCapacity")}</h3>
                                                    <div className="grid md:grid-cols-2 gap-6">
                                                        <FormField
                                                            control={form.control}
                                                            name="fleetSize"
                                                            render={({ field }) => (
                                                                <FormItem>
                                                                     <FormLabel>{t("subcontractors.form.totalTrucks")}</FormLabel>
                                                                     <Select onValueChange={(val) => field.onChange(parseInt(val))} defaultValue={field.value?.toString()}>
                                                                         <FormControl>
                                                                             <SelectTrigger>
                                                                                 <SelectValue placeholder={t("subcontractors.form.totalTrucks.placeholder")} />
                                                                             </SelectTrigger>
                                                                         </FormControl>
                                                                         <SelectContent>
                                                                             <SelectItem value="5">1-5</SelectItem>
                                                                             <SelectItem value="10">6-10</SelectItem>
                                                                             <SelectItem value="20">11-20</SelectItem>
                                                                             <SelectItem value="50">21-50</SelectItem>
                                                                             <SelectItem value="100">50+</SelectItem>
                                                                         </SelectContent>
                                                                     </Select>
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
                                                                     <Select onValueChange={field.onChange} defaultValue={field.value}>
                                                                         <FormControl>
                                                                             <SelectTrigger>
                                                                                 <SelectValue placeholder={t("subcontractors.form.dispatchCenter.placeholder")} />
                                                                             </SelectTrigger>
                                                                         </FormControl>
                                                                         <SelectContent>
                                                                             <SelectItem value="Central Hub - North">{t("subcontractors.form.dispatchCenter.northHub")}</SelectItem>
                                                                             <SelectItem value="Central Hub - South">{t("subcontractors.form.dispatchCenter.southHub")}</SelectItem>
                                                                             <SelectItem value="East Coast Depot">{t("subcontractors.form.dispatchCenter.eastCoast")}</SelectItem>
                                                                         </SelectContent>
                                                                     </Select>
                                                                     <FormMessage />
                                                                </FormItem>
                                                            )}
                                                        />
                                                    </div>
                                                </div>

                                                <div>
                                                    <h3 className="text-sm font-semibold mb-4 flex items-center gap-2"><MapPin className="w-4 h-4 text-blue-600" /> {t("subcontractors.form.serviceRegions")}</h3>
                                                    <FormField
                                                        control={form.control}
                                                        name="serviceRegions"
                                                        render={({ field }) => (
                                                            <FormItem>
                                                                 <FormControl>
                                                                     <div className="space-y-3">
                                                                         <div className="relative">
                                                                             <Check className="absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" />
                                                                             <Input placeholder={t("subcontractors.form.serviceRegions.search")} className="pl-10" />
                                                                         </div>
                                                                         <div className="flex flex-wrap gap-2">
                                                                             {["California", "Texas", "Florida", "New York"].map(region => (
                                                                                 <div key={region} className="bg-blue-50 dark:bg-blue-900/30 text-blue-700 dark:text-blue-300 px-3 py-1 rounded-full text-sm font-medium flex items-center gap-2">
                                                                                     {region}
                                                                                     <button type="button" className="hover:text-blue-900 dark:hover:text-blue-100">×</button>
                                                                                 </div>
                                                                             ))}
                                                                             <Button variant="ghost" size="sm" className="text-blue-600">+ Add 12 more</Button>
                                                                         </div>
                                                                     </div>
                                                                 </FormControl>
                                                                 <FormMessage />
                                                            </FormItem>
                                                        )}
                                                    />
                                                </div>

                                                <div>
                                                    <h3 className="text-sm font-semibold mb-4 flex items-center gap-2"><Truck className="w-4 h-4 text-blue-600" /> {t("subcontractors.form.vehicleTypes")}</h3>
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
                                                                         { id: "cold-chain", label: t("subcontractors.form.vehicle.coldChain"), sub: t("subcontractors.form.vehicle.coldChain.desc"), icon: "❄️" },
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
                                                                                             "flex flex-row items-start space-x-3 space-y-0 rounded-md border p-4 shadow-sm transition-all cursor-pointer hover:border-blue-300",
                                                                                             field.value?.includes(item.id) ? "border-blue-500 bg-blue-50/30 dark:bg-blue-900/20" : "border-slate-200 dark:border-slate-800"
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
                                                                                                 {item.icon && <span className="mr-1">{item.icon}</span>}
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

                                {/* Step 4: Document Upload & Review */}
                                {currentStep === 4 && (
                                    <div className="space-y-6">
                                        <div className="grid grid-cols-1 lg:grid-cols-3 gap-8">
                                            <div className="lg:col-span-2 space-y-6">
                                                <Card className="border-none shadow-sm">
                                                    <CardHeader>
                                                        <CardTitle>{t("subcontractors.form.requiredDocs")}</CardTitle>
                                                    </CardHeader>
                                                    <CardContent className="space-y-8">
                                                        <div>
                                                            <h4 className="text-sm font-medium mb-4">{t("subcontractors.form.businessLicense")}</h4>
                                                            <div className="border-2 border-dashed border-slate-200 dark:border-slate-700 rounded-xl p-8 flex flex-col items-center justify-center text-center hover:bg-slate-50 dark:hover:bg-slate-900 transition-colors cursor-pointer">
                                                                <div className="h-10 w-10 bg-blue-100 dark:bg-blue-900/50 rounded-full flex items-center justify-center text-blue-600 dark:text-blue-400 mb-2">
                                                                     <Upload className="h-5 w-5" />
                                                                </div>
                                                                <p className="font-medium text-slate-900 dark:text-slate-100">{t("subcontractors.form.dragDrop")}</p>
                                                                <p className="text-xs text-slate-500 mt-1">{t("subcontractors.form.fileFormats")}</p>
                                                            </div>
                                                        </div>

                                                        <div>
                                                            <h4 className="text-sm font-medium mb-4">{t("subcontractors.form.insuranceCert")}</h4>
                                                            <div className="bg-green-50 dark:bg-green-900/10 border border-green-100 dark:border-green-900/50 rounded-xl p-4 flex items-center justify-between">
                                                                <div className="flex items-center gap-3">
                                                                     <div className="h-8 w-8 bg-green-100 dark:bg-green-900/50 rounded-full flex items-center justify-center text-green-600 dark:text-green-400">
                                                                         <Check className="h-4 w-4" />
                                                                     </div>
                                                                     <div>
                                                                         <p className="text-sm font-medium text-green-800 dark:text-green-200">insurance_cert_2024.pdf</p>
                                                                         <p className="text-xs text-green-600 dark:text-green-400">{t("subcontractors.form.uploadSuccess")}</p>
                                                                     </div>
                                                                </div>
                                                                <Button variant="ghost" size="icon" className="text-slate-400 hover:text-red-500">
                                                                     <span className="sr-only">Remove</span>
                                                                     ×
                                                                </Button>
                                                            </div>
                                                        </div>
                                                    </CardContent>
                                                </Card>
                                            </div>

                                            <div>
                                                <Card className="border-none shadow-sm">
                                                    <CardHeader>
                                                        <CardTitle>{t("subcontractors.form.summaryReview")}</CardTitle>
                                                    </CardHeader>
                                                    <CardContent className="space-y-6">
                                                        <div>
                                                            <div className="flex justify-between items-center mb-2">
                                                                <h4 className="text-xs font-bold text-slate-500 uppercase tracking-wider">{t("subcontractors.form.companyDetails")}</h4>
                                                                <Button variant="link" size="sm" className="h-auto p-0 text-blue-600" onClick={() => setCurrentStep(1)}>{t("subcontractors.form.edit")}</Button>
                                                            </div>
                                                            <div className="space-y-1">
                                                                <p className="font-semibold text-slate-900 dark:text-slate-100">{form.getValues("name") || t("subcontractors.form.notEntered")}</p>
                                                                <p className="text-xs text-slate-500">Tax ID: {form.getValues("taxId") || form.getValues("idCardNumber") || "-"}</p>
                                                                <p className="text-xs text-slate-500">{form.getValues("address") || t("subcontractors.form.noAddress")}</p>
                                                            </div>
                                                        </div>

                                                        <div className="h-px bg-slate-100 dark:bg-slate-800" />

                                                        <div>
                                                            <div className="flex justify-between items-center mb-2">
                                                                <h4 className="text-xs font-bold text-slate-500 uppercase tracking-wider">{t("subcontractors.form.primaryContact")}</h4>
                                                                <Button variant="link" size="sm" className="h-auto p-0 text-blue-600" onClick={() => setCurrentStep(2)}>{t("subcontractors.form.edit")}</Button>
                                                            </div>
                                                            <div className="space-y-1">
                                                                <p className="font-semibold text-slate-900 dark:text-slate-100">{form.getValues("contactPerson") || "-"}</p>
                                                                <p className="text-xs text-slate-500">{form.getValues("email") || "-"}</p>
                                                                <p className="text-xs text-slate-500">{form.getValues("phone") || "-"}</p>
                                                            </div>
                                                        </div>

                                                        <div className="h-px bg-slate-100 dark:bg-slate-800" />

                                                        <div>
                                                            <div className="flex justify-between items-center mb-2">
                                                                <h4 className="text-xs font-bold text-slate-500 uppercase tracking-wider">{t("subcontractors.form.operationalInfo")}</h4>
                                                                <Button variant="link" size="sm" className="h-auto p-0 text-blue-600" onClick={() => setCurrentStep(3)}>{t("subcontractors.form.edit")}</Button>
                                                            </div>
                                                            <div className="grid grid-cols-2 gap-y-2 text-sm">
                                                                <span className="text-slate-500">{t("subcontractors.table.fleetSize")}</span>
                                                                <span className="text-right font-medium">{String(form.getValues("fleetSize"))} {t("subcontractors.form.trucks")}</span>
                                                                <span className="text-slate-500">{t("subcontractors.form.serviceAreas")}</span>
                                                                <span className="text-right font-medium">Midwest, Northeast</span>
                                                            </div>
                                                        </div>
                                                    </CardContent>
                                                </Card>
                                            </div>
                                        </div>
                                    </div>
                                )}
                            </form>
                        </Form>
                    </div>

                    {/* Sticky Footer for Navigation */}
                    <div className="fixed bottom-0 right-0 left-0 md:left-64 p-4 border-t bg-background/80 backdrop-blur-sm flex justify-between items-center z-20">
                        <Button
                            type="button"
                            variant="outline"
                            onClick={currentStep === 1 ? () => router.push('/app/subcontractors') : prevStep}
                            className="bg-background"
                        >
                            {currentStep === 1 ? t("subcontractors.form.cancel") : t("subcontractors.form.back")}
                        </Button>

                        <Button
                            type="button"
                            className="bg-blue-600 hover:bg-blue-700 text-white min-w-[140px]"
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

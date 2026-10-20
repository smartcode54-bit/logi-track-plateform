"use client";

import { Field as AccessibleField } from "@/features/accounting/components/go/shared";

import { RecordState } from "@/components/record-state";
import { PageHeader } from "@/components/page-header";
import { PagePermissionGuard } from "@/components/page-permission-guard";
import { CAPABILITIES } from "@/lib/capabilities";
import { useEffect, useRef, useState } from "react";
import { useForm } from "react-hook-form";
import { createInvalidHandler } from "@/lib/formInvalidHandler";
import { zodResolver } from "@hookform/resolvers/zod";
import { companyFormValues, type CompanyAsset } from "@/features/companies/api/companies";
import { useOwnerCompany, useSaveCompany } from "@/features/companies/api/useCompanies";
import { applyServerFieldErrors, toastSaveError } from "@/lib/formServerErrors";
import { CompanyFormSchema, type CompanyFormValues } from "@/validate/companySchema";
import { useLanguage } from "@/context/language";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Separator } from "@/components/ui/separator";
import { Badge } from "@/components/ui/badge";
import { Loader2, Upload, Building2, CreditCard, FileText, Settings2 } from "lucide-react";
import Image from "next/image";
import { toast } from "sonner";

// ── Types ─────────────────────────────────────────────────────────────────────

type AssetType = CompanyAsset;

interface AssetState {
  /** The stored image, or a local preview of a file staged for the next save. */
  url: string;
  /** A new file, uploaded when the form is saved (the Go keys embed the company id, §9.4). */
  file?: File;
}

// ── Page ─────────────────────────────────────────────────────────────────────

export default function CompanyProfilePage() {
  const { t } = useLanguage();
  // `['companies',{owner:true}]`, read fresh because this form writes it back (T25).
  const ownerQuery = useOwnerCompany({ fresh: true });
  const save = useSaveCompany();
  const loading = ownerQuery.isPending;
  const saving = save.isPending;
  const [companyId, setCompanyId] = useState<string | null>(null);
  const [assets, setAssets] = useState<Record<AssetType, AssetState>>({
    logo: { url: "" },
    stamp: { url: "" },
    signature: { url: "" },
  });

  const fileRefs = {
    logo: useRef<HTMLInputElement>(null),
    stamp: useRef<HTMLInputElement>(null),
    signature: useRef<HTMLInputElement>(null),
  };

  const form = useForm({
    resolver: zodResolver(CompanyFormSchema),
    defaultValues: {
      nameTh: "",
      nameEn: "",
      shortName: "",
      taxId: "",
      branchType: "headquarters",
      branchNumber: "",
      address: "",
      phone: "",
      email: "",
      bankName: "",
      accountNumber: "",
      accountName: "",
      withholdingTaxRate: 3,
      companyType: "owner",
      isActive: true,
    },
  });

  const {
    register,
    handleSubmit,
    setValue,
    watch,
    reset,
  } = form;

  const branchType = watch("branchType");

  // Reset once when the stored owner company arrives; a background refetch must not overwrite edits.
  const [loadedId, setLoadedId] = useState<string | null>(null);
  useEffect(() => {
    const company = ownerQuery.data;
    if (!company || loadedId === company.id) return;
    setCompanyId(company.id);
    reset(companyFormValues(company));
    setAssets({
      logo: { url: company.logoUrl ?? "" },
      stamp: { url: company.stampUrl ?? "" },
      signature: { url: company.signatureUrl ?? "" },
    });
    setLoadedId(company.id);
  }, [ownerQuery.data, loadedId, reset]);


  // A picked asset is staged and previewed; the save uploads it (with the company created first).
  const handleAssetUpload = (type: AssetType, file: File) => {
    setAssets((prev) => ({ ...prev, [type]: { url: URL.createObjectURL(file), file } }));
  };

  // Form submit
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const onSubmit = async (data: any) => {
    const formData = data as CompanyFormValues;
    const toastId = toast.loading(t("company.saving"));
    try {
      const files: Partial<Record<AssetType, File>> = {};
      for (const type of ["logo", "stamp", "signature"] as const) {
        if (assets[type].file) files[type] = assets[type].file;
      }
      const id = await save.mutateAsync({ id: companyId, values: formData, files });
      setCompanyId(id);
      setLoadedId(id);
      setAssets((prev) => ({
        logo: { url: prev.logo.url },
        stamp: { url: prev.stamp.url },
        signature: { url: prev.signature.url },
      }));
      toast.dismiss(toastId);
      toast.success(t("company.saved"));
    } catch (e) {
      toast.dismiss(toastId);
      applyServerFieldErrors(form, e, t);
      toastSaveError(e, t, "company.saveError");
    }
  };

  // Asset upload field component
  const AssetField = ({ type, label, hint }: { type: AssetType; label: string; hint: string }) => {
    const state = assets[type];
    return (
      <div className="flex flex-col gap-2">
        <Label>
          {label} <span className="text-muted-foreground font-normal text-xs">{t("masterData.optional")}</span>
        </Label>
        <div className="flex items-center gap-4">
          {state.url && (
            <div className="relative w-28 h-16 border rounded-md overflow-hidden bg-muted/40 shrink-0">
              <Image src={state.url} alt={label} fill className="object-contain" unoptimized />
            </div>
          )}
          <div className="flex flex-col gap-1.5">
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={saving}
              onClick={() => fileRefs[type].current?.click()}
            >
              {saving && state.file ? (
                <><Loader2 className="w-3 h-3 mr-1 animate-spin" />{t("company.uploading")}</>
              ) : (
                <><Upload className="w-3 h-3 mr-1" />{state.url ? t("company.asset.change") : t("company.asset.upload")}</>
              )}
            </Button>
            <p className="text-xs text-muted-foreground max-w-56">{hint}</p>
          </div>
        </div>
        <input
          ref={fileRefs[type]}
          type="file"
          accept="image/png,image/jpeg,image/webp"
          className="hidden"
          onChange={(e) => {
            const file = e.target.files?.[0];
            if (file) handleAssetUpload(type, file);
            e.target.value = "";
          }}
        />
      </div>
    );
  };

  if (ownerQuery.isError && ownerQuery.data === undefined) {
    return <PagePermissionGuard capability={CAPABILITIES.company_manage}>
      <RecordState error={ownerQuery.error} onRetry={() => void ownerQuery.refetch()} retrying={ownerQuery.isFetching} />
    </PagePermissionGuard>;
  }

  if (loading) {
    return (
      <PagePermissionGuard capability={CAPABILITIES.company_manage}>
        <div className="flex items-center justify-center h-64">
          <Loader2 className="w-6 h-6 animate-spin text-muted-foreground" />
        </div>
      </PagePermissionGuard>
    );
  }

  return (
    <PagePermissionGuard capability={CAPABILITIES.company_manage}>
      <div className="flex max-w-4xl flex-col gap-section">
        <PageHeader
          title={t("nav.companyProfile")}
          description={t("company.profile.subtitle")}
          status={
            companyId ? (
              <Badge variant="success">
                {t("company.status.active")}
              </Badge>
            ) : null
          }
        />

        <Form {...form}>
          <form onSubmit={handleSubmit(onSubmit, createInvalidHandler(form, t))} className="space-y-6">

            {/* ── Section 1: Corporate Identity ── */}
            <Card>
              <CardHeader>
                <div className="flex items-center gap-2">
                  <Building2 className="w-4 h-4 text-muted-foreground" />
                  <CardTitle className="text-base">{t("company.section.corporateIdentity")}</CardTitle>
                </div>
                <CardDescription>{t("company.section.corporateIdentityDesc")}</CardDescription>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                  <FormField
                    control={form.control}
                    name="nameTh"
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t("company.field.nameTh")} *</FormLabel>
                        <FormControl>
                          <Input
                            placeholder={t("company.placeholder.nameTh")}
                            {...field}
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  <div className="space-y-1.5">
                    <Label htmlFor="nameEn">{t("company.field.nameEn")}</Label>
                    <Input
                      id="nameEn"
                      placeholder={t("company.placeholder.nameEn")}
                      {...register("nameEn")}
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="shortName">{t("company.field.shortName")}</Label>
                    <Input
                      id="shortName"
                      placeholder={t("company.placeholder.shortName")}
                      {...register("shortName")}
                    />
                  </div>
                </div>

                <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                  <FormField
                    control={form.control}
                    name="taxId"
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t("company.field.taxId")} *</FormLabel>
                        <FormControl>
                          <Input
                            placeholder={t("company.placeholder.taxId")}
                            maxLength={13}
                            {...field}
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  <div className="grid grid-cols-2 gap-3">
                    <AccessibleField className="space-y-1.5" label={t("company.field.branchType")}>

                      <Select
                        value={branchType}
                        onValueChange={(v) => setValue("branchType", v as "headquarters" | "branch")}
                      >
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="headquarters">{t("company.branchType.headquarters")}</SelectItem>
                          <SelectItem value="branch">{t("company.branchType.branch")}</SelectItem>
                        </SelectContent>
                      </Select>
                    </AccessibleField>
                    {branchType === "branch" && (
                      <div className="space-y-1.5">
                        <Label htmlFor="branchNumber">{t("company.field.branchNumber")}</Label>
                        <Input
                          id="branchNumber"
                          placeholder={t("company.placeholder.branchNumber")}
                          maxLength={5}
                          {...register("branchNumber")}
                        />
                      </div>
                    )}
                  </div>
                </div>

                <FormField
                  control={form.control}
                  name="address"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t("company.field.address")} *</FormLabel>
                      <FormControl>
                        <Input
                          placeholder={t("company.placeholder.address")}
                          {...field}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                  <div className="space-y-1.5">
                    <Label htmlFor="phone">{t("company.field.phone")}</Label>
                    <Input id="phone" type="tel" {...register("phone")} />
                  </div>
                  <FormField
                    control={form.control}
                    name="email"
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t("company.field.email")}</FormLabel>
                        <FormControl>
                          <Input type="email" {...field} />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </div>

                <div className="space-y-1.5">
                  <Label htmlFor="signatoryName">{t("company.field.signatoryName")}</Label>
                  <Input
                    id="signatoryName"
                    placeholder={t("company.placeholder.signatoryName")}
                    {...register("signatoryName")}
                  />
                  <p className="text-xs text-muted-foreground">{t("masterData.companies.signatoryHint")}</p>
                </div>
              </CardContent>
            </Card>

            {/* ── Section 2: Documents & Assets ── */}
            <Card>
              <CardHeader>
                <div className="flex items-center gap-2">
                  <FileText className="w-4 h-4 text-muted-foreground" />
                  <CardTitle className="text-base">{t("company.section.documents")}</CardTitle>
                </div>
                <CardDescription>{t("company.section.documentsDesc")}</CardDescription>
              </CardHeader>
              <CardContent className="space-y-6">
                <AssetField
                  type="logo"
                  label={t("company.field.logo")}
                  hint={t("company.asset.logo.hint")}
                />
                <Separator />
                <AssetField
                  type="stamp"
                  label={t("company.field.stamp")}
                  hint={t("company.asset.stamp.hint")}
                />
                <Separator />
                <AssetField
                  type="signature"
                  label={t("company.field.signature")}
                  hint={t("company.asset.signature.hint")}
                />
              </CardContent>
            </Card>

            {/* ── Section 3: Banking ── */}
            <Card>
              <CardHeader>
                <div className="flex items-center gap-2">
                  <CreditCard className="w-4 h-4 text-muted-foreground" />
                  <CardTitle className="text-base">{t("company.section.banking")}</CardTitle>
                </div>
                <CardDescription>{t("company.section.bankingDesc")}</CardDescription>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                  <div className="space-y-1.5">
                    <Label htmlFor="bankName">{t("company.field.bankName")}</Label>
                    <Input
                      id="bankName"
                      placeholder={t("company.placeholder.bankName")}
                      {...register("bankName")}
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="accountNumber">{t("company.field.accountNumber")}</Label>
                    <Input
                      id="accountNumber"
                      placeholder={t("company.placeholder.accountNumber")}
                      {...register("accountNumber")}
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="accountName">{t("company.field.accountName")}</Label>
                    <Input
                      id="accountName"
                      placeholder={t("company.placeholder.accountName")}
                      {...register("accountName")}
                    />
                  </div>
                </div>
                <div className="max-w-xs space-y-1.5">
                  <Label htmlFor="withholdingTaxRate">{t("company.field.withholdingTax")}</Label>
                  <Input
                    id="withholdingTaxRate"
                    type="number"
                    step="0.5"
                    min="0"
                    max="100"
                    {...register("withholdingTaxRate", { valueAsNumber: true })}
                  />
                  <p className="text-xs text-muted-foreground">{t("masterData.companies.whtHint")}</p>
                </div>
              </CardContent>
            </Card>

            {/* ── Section 4: Status (owner type locked) ── */}
            <Card>
              <CardHeader>
                <div className="flex items-center gap-2">
                  <Settings2 className="w-4 h-4 text-muted-foreground" />
                  <CardTitle className="text-base">{t("masterData.companies.statusTitle")}</CardTitle>
                </div>
              </CardHeader>
              <CardContent>
                <div className="flex items-center gap-3">
                  <Switch
                    id="isActive"
                    checked={watch("isActive")}
                    onCheckedChange={(v) => setValue("isActive", v)}
                  />
                  <Label htmlFor="isActive">{t("company.field.isActive")}</Label>
                </div>
                <input type="hidden" value="owner" {...register("companyType")} />
              </CardContent>
            </Card>

            {/* Save button */}
            <div className="flex justify-end">
              <Button type="submit" disabled={saving} className="min-w-32">
                {saving ? (
                  <><Loader2 className="w-4 h-4 mr-2 animate-spin" />{t("company.saving")}</>
                ) : (
                  t("company.save")
                )}
              </Button>
            </div>
          </form>
        </Form>
      </div>
    </PagePermissionGuard>
  );
}

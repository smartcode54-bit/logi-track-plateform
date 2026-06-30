"use client";

import { PagePermissionGuard } from "@/components/page-permission-guard";
import { CAPABILITIES } from "@/lib/capabilities";
import { useEffect, useRef, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import {
  getOwnerCompany,
  createCompany,
  updateCompany,
  uploadCompanyAsset,
} from "@/features/companies/api/companies";
import { CompanyFormSchema, type CompanyFormValues } from "@/validate/companySchema";
import { useLanguage } from "@/context/language";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
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

type AssetType = "logo" | "stamp" | "signature";

interface AssetState {
  url: string;
  uploading: boolean;
}

// ── Page ─────────────────────────────────────────────────────────────────────

export default function CompanyProfilePage() {
  const { t } = useLanguage();
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [companyId, setCompanyId] = useState<string | null>(null);
  const [assets, setAssets] = useState<Record<AssetType, AssetState>>({
    logo: { url: "", uploading: false },
    stamp: { url: "", uploading: false },
    signature: { url: "", uploading: false },
  });

  const fileRefs = {
    logo: useRef<HTMLInputElement>(null),
    stamp: useRef<HTMLInputElement>(null),
    signature: useRef<HTMLInputElement>(null),
  };

  const {
    register,
    handleSubmit,
    setValue,
    watch,
    reset,
    formState: { errors },
  } = useForm({
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

  const branchType = watch("branchType");

  // Load existing owner company
  useEffect(() => {
    setLoading(true);
    getOwnerCompany()
      .then((company) => {
        if (company) {
          setCompanyId(company.id);
          // eslint-disable-next-line @typescript-eslint/no-unused-vars
          const { id: _id, createdAt: _c, updatedAt: _u, ...formData } = company;
          reset(formData as CompanyFormValues);
          setAssets({
            logo: { url: company.logoUrl ?? "", uploading: false },
            stamp: { url: company.stampUrl ?? "", uploading: false },
            signature: { url: company.signatureUrl ?? "", uploading: false },
          });
        }
      })
      .catch((e) => {
        console.error("[company-profile] load error:", e);
        toast.error("ไม่สามารถโหลดข้อมูลบริษัทได้");
      })
      .finally(() => setLoading(false));
  }, [reset]);

  // Handle asset upload
  const handleAssetUpload = async (type: AssetType, file: File) => {
    // Need a company doc ID to upload; create a placeholder owner company if none exists
    let cId = companyId;
    if (!cId) {
      try {
        cId = await createCompany({
          nameTh: watch("nameTh") || "บริษัท (กำลังสร้าง)",
          taxId: "0000000000000",
          branchType: "headquarters",
          address: "-",
          withholdingTaxRate: 3,
          companyType: "owner",
          isActive: true,
        });
        setCompanyId(cId);
      } catch {
        toast.error(t("company.uploadError"));
        return;
      }
    }

    setAssets((prev) => ({ ...prev, [type]: { ...prev[type], uploading: true } }));
    try {
      const url = await uploadCompanyAsset(cId, type, file);
      // Update form value + state
      const fieldMap: Record<AssetType, keyof CompanyFormValues> = {
        logo: "logoUrl",
        stamp: "stampUrl",
        signature: "signatureUrl",
      };
      setValue(fieldMap[type], url);
      setAssets((prev) => ({ ...prev, [type]: { url, uploading: false } }));
      toast.success(`อัปโหลด${type === "logo" ? "โลโก้" : type === "stamp" ? "ตราประทับ" : "ลายเซ็น"}สำเร็จ`);
    } catch {
      toast.error(t("company.uploadError"));
      setAssets((prev) => ({ ...prev, [type]: { ...prev[type], uploading: false } }));
    }
  };

  // Form submit
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const onSubmit = async (data: any) => {
    const formData = data as CompanyFormValues;
    setSaving(true);
    const toastId = toast.loading(t("company.saving"));
    try {
      // Merge current asset URLs into formData
      formData.logoUrl = assets.logo.url || formData.logoUrl;
      formData.stampUrl = assets.stamp.url || formData.stampUrl;
      formData.signatureUrl = assets.signature.url || formData.signatureUrl;

      if (companyId) {
        await updateCompany(companyId, formData);
      } else {
        const newId = await createCompany(formData);
        setCompanyId(newId);
      }
      toast.dismiss(toastId);
      toast.success(t("company.saved"));
    } catch (e) {
      console.error("[company-profile] save error:", e);
      toast.dismiss(toastId);
      toast.error(t("company.saveError"));
    } finally {
      setSaving(false);
    }
  };

  // Asset upload field component
  const AssetField = ({ type, label, hint }: { type: AssetType; label: string; hint: string }) => {
    const state = assets[type];
    return (
      <div className="flex flex-col gap-2">
        <Label>
          {label} <span className="text-muted-foreground font-normal text-xs">(ไม่บังคับ)</span>
        </Label>
        <div className="flex items-center gap-4">
          {state.url && (
            <div className="relative w-28 h-16 border rounded-md overflow-hidden bg-muted/40 shrink-0">
              <Image src={state.url} alt={label} fill style={{ objectFit: "contain" }} unoptimized />
            </div>
          )}
          <div className="flex flex-col gap-1.5">
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={state.uploading}
              onClick={() => fileRefs[type].current?.click()}
            >
              {state.uploading ? (
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
      <div className="p-6 max-w-4xl mx-auto space-y-6">
        {/* Header */}
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold">{t("company.profile.title")}</h1>
            <p className="text-muted-foreground text-sm mt-1">{t("company.profile.subtitle")}</p>
          </div>
          {companyId && (
            <Badge variant="outline" className="text-green-600 border-green-300">
              {t("company.status.active")}
            </Badge>
          )}
        </div>

        <form onSubmit={handleSubmit(onSubmit)} className="space-y-6">

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
              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-1.5">
                  <Label htmlFor="nameTh">{t("company.field.nameTh")} *</Label>
                  <Input
                    id="nameTh"
                    placeholder={t("company.placeholder.nameTh")}
                    {...register("nameTh")}
                  />
                  {errors.nameTh && <p className="text-xs text-red-500">{errors.nameTh.message}</p>}
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="nameEn">{t("company.field.nameEn")}</Label>
                  <Input
                    id="nameEn"
                    placeholder={t("company.placeholder.nameEn")}
                    {...register("nameEn")}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="shortName">{t("company.field.shortName", "ชื่อย่อ (เช่น WRT)")}</Label>
                  <Input
                    id="shortName"
                    placeholder={t("company.placeholder.shortName", "WRT")}
                    {...register("shortName")}
                  />
                </div>
              </div>

              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-1.5">
                  <Label htmlFor="taxId">{t("company.field.taxId")} *</Label>
                  <Input
                    id="taxId"
                    placeholder={t("company.placeholder.taxId")}
                    maxLength={13}
                    {...register("taxId")}
                  />
                  {errors.taxId && <p className="text-xs text-red-500">{errors.taxId.message}</p>}
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <div className="space-y-1.5">
                    <Label>{t("company.field.branchType")}</Label>
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
                  </div>
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

              <div className="space-y-1.5">
                <Label htmlFor="address">{t("company.field.address")} *</Label>
                <Input
                  id="address"
                  placeholder={t("company.placeholder.address")}
                  {...register("address")}
                />
                {errors.address && <p className="text-xs text-red-500">{errors.address.message}</p>}
              </div>

              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-1.5">
                  <Label htmlFor="phone">{t("company.field.phone")}</Label>
                  <Input id="phone" type="tel" {...register("phone")} />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="email">{t("company.field.email")}</Label>
                  <Input id="email" type="email" {...register("email")} />
                  {errors.email && <p className="text-xs text-red-500">{errors.email.message}</p>}
                </div>
              </div>

              <div className="space-y-1.5">
                <Label htmlFor="signatoryName">{t("company.field.signatoryName")}</Label>
                <Input
                  id="signatoryName"
                  placeholder={t("company.placeholder.signatoryName")}
                  {...register("signatoryName")}
                />
                <p className="text-xs text-muted-foreground">ชื่อที่แสดงใต้ลายเซ็นในใบวางบิล</p>
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
              <div className="grid grid-cols-3 gap-4">
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
                <p className="text-xs text-muted-foreground">ตัวอย่าง: 3 = หัก 3%</p>
              </div>
            </CardContent>
          </Card>

          {/* ── Section 4: Status (owner type locked) ── */}
          <Card>
            <CardHeader>
              <div className="flex items-center gap-2">
                <Settings2 className="w-4 h-4 text-muted-foreground" />
                <CardTitle className="text-base">สถานะบริษัท</CardTitle>
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
      </div>
    </PagePermissionGuard>
  );
}

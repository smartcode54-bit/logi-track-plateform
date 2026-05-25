"use client";

import { PagePermissionGuard } from "@/components/page-permission-guard";
import { CAPABILITIES } from "@/lib/capabilities";
import { useEffect, useState } from "react";
import { getCompanies, type CompanyWithId } from "@/features/companies/api/companies";
import { useLanguage } from "@/context/language";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Loader2, Plus, Building2, Pencil } from "lucide-react";
import Link from "next/link";
import { format } from "date-fns";
import { toast } from "sonner";

export default function CompaniesPage() {
  const { t } = useLanguage();
  const [companies, setCompanies] = useState<CompanyWithId[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    getCompanies()
      .then(setCompanies)
      .catch((e) => {
        console.error("[companies] load error:", e);
        toast.error("ไม่สามารถโหลดข้อมูลบริษัทได้");
      })
      .finally(() => setLoading(false));
  }, []);

  const subCount = companies.filter((c) => c.companyType === "subcontractor").length;
  const activeCount = companies.filter((c) => c.isActive).length;

  const formatDate = (ts: unknown) => {
    try {
      if (!ts) return "-";
      const date = typeof (ts as { toDate?: () => Date }).toDate === "function"
        ? (ts as { toDate: () => Date }).toDate()
        : new Date(ts as string);
      return format(date, "dd/MM/yyyy");
    } catch {
      return "-";
    }
  };

  return (
    <PagePermissionGuard capability={CAPABILITIES.company_view}>
      <div className="p-6 space-y-6">
        {/* Header */}
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold">{t("company.title")}</h1>
            <p className="text-muted-foreground text-sm mt-1">{t("company.subtitle")}</p>
          </div>
          <Button asChild>
            <Link href="/app/companies/new">
              <Plus className="w-4 h-4 mr-2" />
              {t("company.new")}
            </Link>
          </Button>
        </div>

        {/* Stats */}
        <div className="grid grid-cols-3 gap-4">
          <Card>
            <CardContent className="pt-4">
              <p className="text-xs text-muted-foreground">ทั้งหมด</p>
              <p className="text-2xl font-bold">{companies.length}</p>
            </CardContent>
          </Card>
          <Card>
            <CardContent className="pt-4">
              <p className="text-xs text-muted-foreground">Subcontractor</p>
              <p className="text-2xl font-bold">{subCount}</p>
            </CardContent>
          </Card>
          <Card>
            <CardContent className="pt-4">
              <p className="text-xs text-muted-foreground">ใช้งานอยู่</p>
              <p className="text-2xl font-bold text-green-600">{activeCount}</p>
            </CardContent>
          </Card>
        </div>

        {/* Table */}
        <Card>
          <CardHeader>
            <CardTitle className="text-base flex items-center gap-2">
              <Building2 className="w-4 h-4" />
              รายชื่อบริษัท
            </CardTitle>
            <CardDescription>Owner 1 รายการ + Subcontractor {subCount} รายการ</CardDescription>
          </CardHeader>
          <CardContent className="p-0">
            {loading ? (
              <div className="flex items-center justify-center h-32">
                <Loader2 className="w-5 h-5 animate-spin text-muted-foreground" />
              </div>
            ) : companies.length === 0 ? (
              <div className="flex flex-col items-center justify-center h-32 text-muted-foreground text-sm gap-2">
                <Building2 className="w-8 h-8 opacity-30" />
                <p>{t("company.createFirst")}</p>
                <Button asChild variant="outline" size="sm">
                  <Link href="/app/settings/company-profile">ตั้งค่าโปรไฟล์บริษัทหลัก</Link>
                </Button>
              </div>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("company.table.name")}</TableHead>
                    <TableHead>{t("company.table.taxId")}</TableHead>
                    <TableHead>{t("company.table.type")}</TableHead>
                    <TableHead>{t("company.table.status")}</TableHead>
                    <TableHead>{t("company.table.createdAt")}</TableHead>
                    <TableHead className="text-right">{t("company.table.actions")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {companies.map((company) => (
                    <TableRow key={company.id}>
                      <TableCell>
                        <div>
                          <p className="font-medium">{company.nameTh}</p>
                          {company.nameEn && <p className="text-xs text-muted-foreground">{company.nameEn}</p>}
                        </div>
                      </TableCell>
                      <TableCell className="font-mono text-xs">{company.taxId}</TableCell>
                      <TableCell>
                        <Badge
                          variant={company.companyType === "owner" ? "default" : "outline"}
                          className={company.companyType === "owner" ? "bg-blue-600" : ""}
                        >
                          {company.companyType === "owner" ? t("company.type.owner") : t("company.type.subcontractor")}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        <Badge
                          variant={company.isActive ? "default" : "secondary"}
                          className={company.isActive ? "bg-green-600" : ""}
                        >
                          {company.isActive ? t("company.status.active") : t("company.status.inactive")}
                        </Badge>
                      </TableCell>
                      <TableCell className="text-xs text-muted-foreground">
                        {formatDate(company.createdAt)}
                      </TableCell>
                      <TableCell className="text-right">
                        <Button asChild variant="ghost" size="sm">
                          <Link href={company.companyType === "owner" ? "/app/settings/company-profile" : `/app/companies/${company.id}/edit`}>
                            <Pencil className="w-3 h-3 mr-1" />
                            {t("company.edit")}
                          </Link>
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      </div>
    </PagePermissionGuard>
  );
}

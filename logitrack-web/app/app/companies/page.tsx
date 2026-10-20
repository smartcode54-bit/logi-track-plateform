"use client";

import { usePermission } from "@/hooks/usePermission";
import { PagePermissionGuard } from "@/components/page-permission-guard";
import { CAPABILITIES } from "@/lib/capabilities";
import { useCompanies } from "@/features/companies/api/useCompanies";
import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Loader2, Building2, Pencil } from "lucide-react";
import Link from "next/link";
import { format } from "date-fns";

export default function CompaniesPage() {
  const { t } = useLanguage();
  const { hasPermission: canManage } = usePermission(CAPABILITIES.company_manage);
  // `['companies',{view:'all'}]` (T25): Go `GET /v1/companies` or Firestore by the `masterdata` flag.
  const { companies, isPending: loading, isError } = useCompanies();

  const subCount = companies.filter((c) => c.companyType === "subcontractor").length;
  const activeCount = companies.filter((c) => c.isActive).length;

  const formatDate = (ts: string | null) => {
    try {
      return ts ? format(new Date(ts), "dd/MM/yyyy") : "-";
    } catch {
      return "-";
    }
  };

  return (
    <PagePermissionGuard capability={CAPABILITIES.company_view}>
      <div className="flex flex-col gap-section">
        <PageHeader
          title={t("nav.companies")}
          description={t("company.subtitle")}
          actions={
            canManage ? <Button asChild>
              <Link href="/app/settings/company-profile">
                <Pencil className="w-4 h-4 mr-2" aria-hidden />
                {t("nav.companyProfile")}
              </Link>
            </Button> : null
          }
        />

        {/* Stats */}
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
          <Card>
            <CardContent className="pt-4">
              <p className="text-xs text-muted-foreground">{t("masterData.companies.total")}</p>
              <p className="text-2xl font-semibold">{companies.length}</p>
            </CardContent>
          </Card>
          <Card>
            <CardContent className="pt-4">
              <p className="text-xs text-muted-foreground">{t("company.type.subcontractor")}</p>
              <p className="text-2xl font-semibold">{subCount}</p>
            </CardContent>
          </Card>
          <Card>
            <CardContent className="pt-4">
              <p className="text-xs text-muted-foreground">{t("masterData.companies.active")}</p>
              <p className="text-2xl font-semibold text-success">{activeCount}</p>
            </CardContent>
          </Card>
        </div>

        {/* Table */}
        <Card>
          <CardHeader>
            <CardTitle className="text-base flex items-center gap-2">
              <Building2 className="w-4 h-4" />
              {t("masterData.companies.listTitle")}
            </CardTitle>
            <CardDescription>{t("masterData.companies.listDescription", { owner: companies.length - subCount, sub: subCount })}</CardDescription>
          </CardHeader>
          <CardContent className="p-0">
            {loading ? (
              <div className="flex items-center justify-center h-32">
                <Loader2 className="w-5 h-5 animate-spin text-muted-foreground" />
              </div>
            ) : isError ? (
              <div className="flex items-center justify-center h-32 text-sm text-destructive">{t("masterData.companies.loadError")}</div>
            ) : companies.length === 0 ? (
              <div className="flex flex-col items-center justify-center h-32 text-muted-foreground text-sm gap-2">
                <Building2 className="w-8 h-8 opacity-30" />
                <p>{t("company.createFirst")}</p>
                <Button asChild variant="outline" size="sm">
                  <Link href="/app/settings/company-profile">{t("masterData.companies.setupOwner")}</Link>
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
                          variant="outline"
                        >
                          {company.companyType === "owner" ? t("company.type.owner") : t("company.type.subcontractor")}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        <Badge
                          variant={company.isActive ? "success" : "secondary"}
                        >
                          {company.isActive ? t("company.status.active") : t("company.status.inactive")}
                        </Badge>
                      </TableCell>
                      <TableCell className="text-xs text-muted-foreground">
                        {formatDate(company.createdAt)}
                      </TableCell>
                      <TableCell className="text-right">
                        {canManage && company.companyType === "owner" ? <Button asChild variant="ghost" size="sm">
                          <Link href="/app/settings/company-profile">
                            <Pencil className="w-3 h-3 mr-1" />
                            {t("company.edit")}
                          </Link>
                        </Button> : null}
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

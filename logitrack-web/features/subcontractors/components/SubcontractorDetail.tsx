"use client";

import { useParams } from "next/navigation";
import Link from "next/link";
import { Edit, Loader2 } from "lucide-react";
import { useSubcontractor } from "../api/useSubcontractors";
import { useTruckPages } from "@/features/trucks/api/useTrucks";
import { useMe } from "@/features/auth/api/useMe";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { formatLicensePlate } from "@/lib/utils";
import { carrierStatusTone, toneBadgeVariant, truckStatusTone } from "@/lib/statusPresentation";
import { routeAllowed } from "@/lib/routeCapabilities";
import { apiErrorText } from "@/lib/apiError";
import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
import { RecordState, recordUnavailable } from "@/components/record-state";

function ProfileField({ label, children }: { label: string; children: React.ReactNode }) {
    return (
        <div className="grid gap-1 border-b border-border py-3 last:border-0">
            <dt className="text-sm text-muted-foreground">{label}</dt>
            <dd className="break-words text-sm text-foreground">{children}</dd>
        </div>
    );
}

function documentHref(url: string | null): string | undefined {
    if (!url) return undefined;
    try {
        const parsed = new URL(url);
        return ["https:", "http:"].includes(parsed.protocol) ? parsed.href : undefined;
    } catch {
        return undefined;
    }
}

export default function SubcontractorDetail() {
    const params = useParams();
    const { t, language } = useLanguage();
    const id = params?.id as string;
    const me = useMe();
    const query = useSubcontractor(id ?? "");
    const subcontractor = query.data ?? null;
    const truckPages = useTruckPages({ tenantId: id ?? "" }, { enabled: Boolean(id) });
    const trucks = truckPages.trucks.slice(0, 3);
    const editHref = `/app/subcontractors/${id}/edit`;
    const canEdit = routeAllowed(me.data?.capabilities ?? [], editHref);

    if (!id || recordUnavailable(query)) {
        return <RecordState error={query.error} onRetry={() => void query.refetch()} retrying={query.isFetching} />;
    }

    if (query.isPending || !subcontractor) {
        return (
            <div className="flex min-h-[400px] items-center justify-center" role="status" aria-busy="true">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" aria-hidden />
                <span className="sr-only">{t("common.loading")}</span>
            </div>
        );
    }

    const statusLabel = ["active", "pending", "suspended"].includes(subcontractor.status)
        ? t(`subcontractors.edit.status.${subcontractor.status}`) : subcontractor.status;
    const notSet = t("subcontractors.ui4.notSet");

    return (
        <div className="flex min-w-0 flex-col gap-section">
            <PageHeader
                title={t("nav.partners")}
                recordName={subcontractor.nameTh}
                back={{ href: "/app/subcontractors" }}
                status={<Badge variant={toneBadgeVariant(carrierStatusTone(subcontractor.status))}>{statusLabel}</Badge>}
                description={subcontractor.createdAt
                    ? `${t("subcontractors.detail.joined")} ${new Date(subcontractor.createdAt).toLocaleDateString(language === "th" ? "th-TH" : "en-GB")}`
                    : undefined}
                actions={canEdit ? (
                    <Button asChild>
                        <Link href={editHref} prefetch={false}>
                            <Edit className="h-4 w-4" aria-hidden /> {t("subcontractors.detail.editProfile")}
                        </Link>
                    </Button>
                ) : undefined}
            />
            <div className="grid gap-section md:grid-cols-2">
                <section className="rounded-xl border border-border bg-card p-6">
                    <h2 className="text-base font-semibold">{t("subcontractors.detail.companyDetails")}</h2>
                    <dl>
                        <ProfileField label={t("subcontractors.detail.legalName")}>{subcontractor.nameTh}</ProfileField>
                        <ProfileField label={t("subcontractors.form.taxId")}>{subcontractor.taxId || notSet}</ProfileField>
                        <ProfileField label={t("subcontractors.detail.businessType")}>
                            {subcontractor.legalType ? t(`subcontractors.form.type.${subcontractor.legalType}`) : notSet}
                        </ProfileField>
                        <ProfileField label={t("subcontractors.detail.headquarters")}>{subcontractor.address || notSet}</ProfileField>
                        <ProfileField label={t("subcontractors.form.dispatchCenter")}>{subcontractor.dispatchCenter || notSet}</ProfileField>
                        <ProfileField label={t("subcontractors.form.serviceRegions")}>{subcontractor.serviceRegions.join(", ") || notSet}</ProfileField>
                    </dl>
                </section>
                <section className="rounded-xl border border-border bg-card p-6">
                    <h2 className="text-base font-semibold">{t("subcontractors.detail.pointOfContact")}</h2>
                    <dl>
                        <ProfileField label={t("subcontractors.form.fullName")}>{subcontractor.contactPerson || notSet}</ProfileField>
                        <ProfileField label={t("subcontractors.form.designation")}>{subcontractor.designation || notSet}</ProfileField>
                        <ProfileField label={t("subcontractors.edit.email")}>
                            {subcontractor.email ? <a className="text-primary underline underline-offset-4" href={`mailto:${subcontractor.email}`}>{subcontractor.email}</a> : notSet}
                        </ProfileField>
                        <ProfileField label={t("subcontractors.edit.phone")}>
                            {subcontractor.phone ? <a className="text-primary underline underline-offset-4" href={`tel:${subcontractor.phone}`}>{subcontractor.phone}</a> : notSet}
                        </ProfileField>
                    </dl>
                </section>
            </div>
            <section className="min-w-0 space-y-4">
                <h2 className="text-base font-semibold">{t("subcontractors.ui4.fleetPreview", { count: trucks.length })}</h2>
                {truckPages.error && <Alert variant="destructive"><AlertDescription>{apiErrorText(truckPages.error, t)}</AlertDescription></Alert>}
                <div className="overflow-hidden rounded-xl border border-border bg-card">
                    <Table>
                        <TableHeader><TableRow>
                            <TableHead>{t("subcontractors.detail.truckType")}</TableHead>
                            <TableHead>{t("subcontractors.detail.plate")}</TableHead>
                            <TableHead>{t("subcontractors.table.status")}</TableHead>
                        </TableRow></TableHeader>
                        <TableBody>
                            {trucks.map((truck) => (
                                <TableRow key={truck.id}>
                                    <TableCell>{truck.type || notSet}</TableCell>
                                    <TableCell className="font-mono">{formatLicensePlate(truck.licensePlate)}</TableCell>
                                    <TableCell><Badge variant={toneBadgeVariant(truckStatusTone(truck.status))}>
                                        {truck.status === "in_transit" ? t("trucks.status.inTransit")
                                            : truck.status === "insurance_claim" ? t("trucks.detail.status.insuranceClaim")
                                            : ["active", "maintenance", "inactive"].includes(truck.status) ? t(`trucks.status.${truck.status}`)
                                            : ["available", "sold"].includes(truck.status) ? t(`trucks.detail.status.${truck.status}`) : truck.status}
                                    </Badge></TableCell>
                                </TableRow>
                            ))}
                            {trucks.length === 0 && <TableRow><TableCell colSpan={3} className="py-8 text-center text-muted-foreground">
                                {truckPages.isPending ? t("common.loading") : truckPages.error ? notSet : t("subcontractors.detail.noTrucks")}
                            </TableCell></TableRow>}
                        </TableBody>
                    </Table>
                </div>
            </section>
            <section className="space-y-4">
                <div className="flex flex-wrap items-center justify-between gap-4">
                    <h2 className="text-base font-semibold">{t("subcontractors.edit.documents")}</h2>
                    {canEdit && <Button variant="outline" asChild><Link href={editHref} prefetch={false}>{t("subcontractors.ui4.manageDocuments")}</Link></Button>}
                </div>
                {subcontractor.documents.length === 0 ? (
                    <p className="rounded-xl border border-border bg-card p-6 text-sm text-muted-foreground">{t("subcontractors.ui4.documentsEmpty")}</p>
                ) : (
                    <ul className="divide-y divide-border rounded-xl border border-border bg-card">
                        {subcontractor.documents.map((document, index) => {
                            const href = documentHref(document.url);
                            const documentName = document.kind === "id_card" ? t("subcontractors.edit.idCardCopy")
                                : document.kind === "company_doc" ? t("subcontractors.edit.companyDocs")
                                : t("subcontractors.file.document");
                            return <li key={document.fileId} className="flex flex-wrap items-center justify-between gap-3 p-4 text-sm">
                                <span className="min-w-0 break-words">{documentName}</span>
                                {href ? <a href={href} target="_blank" rel="noopener noreferrer" aria-label={t("subcontractors.ui4.openDocumentNumber", { number: index + 1 })} className="text-primary underline underline-offset-4">{t("subcontractors.ui4.openDocument")}</a>
                                    : <span className="text-muted-foreground">{t("subcontractors.ui4.documentUnavailable")}</span>}
                            </li>;
                        })}
                    </ul>
                )}
            </section>
        </div>
    );
}

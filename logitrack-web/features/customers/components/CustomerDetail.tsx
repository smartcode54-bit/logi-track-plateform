"use client";

import { useParams } from "next/navigation";
import { useCustomer } from "@/features/customers/api/useCustomers";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Loader2, Edit, Mail, Phone, User, MapPin, CreditCard, Clock, FileText } from "lucide-react";
import Link from "next/link";
import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
import { RecordState } from "@/components/record-state";

function InfoRow({ label, value }: { label: string; value?: string | number | null }) {
    if (!value && value !== 0) return null;
    return (
        <div className="flex gap-3 py-2 border-b last:border-0">
            <span className="text-sm text-muted-foreground w-40 shrink-0">{label}</span>
            <span className="text-sm font-medium">{String(value)}</span>
        </div>
    );
}

export default function CustomerDetail() {
    // A real dynamic segment since TW2 (standalone server; no Hosting placeholder rewrite).
    const { id = "" } = useParams<{ id: string }>();
    const { t } = useLanguage();
    // `['customers','detail',id]` (T25): Go or Firestore by the `masterdata` flag.
    const query = useCustomer(id);
    const customer = query.data ?? null;

    // No id, no customer, or the read failed: not found or outside the reader's scope (Go answers 404 for both), or a
    // load error with a retry (UI3D).
    if (!id || query.isError) {
        return <RecordState error={query.error} onRetry={() => void query.refetch()} retrying={query.isFetching} />;
    }

    if (query.isPending || !customer) {
        return (
            <div className="flex justify-center py-24" role="status" aria-busy="true">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" aria-hidden />
                <span className="sr-only">{t("common.loading")}</span>
            </div>
        );
    }

    const hasBillingInfo = !!(
        customer.taxId || customer.address || customer.branchType ||
        customer.contactName || customer.contactPhone || customer.billingEmail ||
        customer.paymentTermsDays != null || customer.invoiceNote
    );

    const branchLabel = customer.branchType
        ? `${t(`customers.form.branchType.${customer.branchType}`)}${customer.branchNumber ? ` ${customer.branchNumber}` : ""}`
        : undefined;

    return (
        <div className="flex max-w-3xl flex-col gap-section">
            <PageHeader
                title={t("nav.customers")}
                recordName={customer.name}
                back={{ href: "/app/customers" }}
                description={
                    <span className="flex items-center gap-2">
                        {customer.logoUrl ? (
                            <img src={customer.logoUrl} alt="" className="h-6 w-6 rounded-full border object-cover" />
                        ) : null}
                        <span className="font-mono">{customer.code}</span>
                    </span>
                }
                actions={
                    <Button asChild>
                        <Link href={`/app/customers/${customer.id}/edit`} prefetch={false}>
                            <Edit className="h-4 w-4" aria-hidden />
                            {t("customers.action.edit")}
                        </Link>
                    </Button>
                }
            />

            {customer.description && (
                <Card>
                    <CardContent className="pt-6">
                        <p className="text-muted-foreground">{customer.description}</p>
                    </CardContent>
                </Card>
            )}

            {/* ── Billing Information ── */}
            <Card>
                <CardHeader>
                    <CardTitle className="flex items-center gap-2">
                        <CreditCard className="h-4 w-4" />
                        {t("customers.detail.billing.title")}
                    </CardTitle>
                </CardHeader>
                <CardContent>
                    {hasBillingInfo ? (
                        <div className="space-y-1">
                            {customer.taxId && (
                                <div className="flex gap-3 py-2 border-b">
                                    <span className="text-sm text-muted-foreground w-40 shrink-0">{t("customers.detail.billing.taxId")}</span>
                                    <span className="text-sm font-mono font-medium">{customer.taxId}</span>
                                </div>
                            )}
                            {branchLabel && (
                                <InfoRow label={t("customers.detail.billing.branchType")} value={branchLabel} />
                            )}
                            {customer.address && (
                                <div className="flex gap-3 py-2 border-b">
                                    <span className="text-sm text-muted-foreground w-40 shrink-0 flex items-center gap-1">
                                        <MapPin className="h-3 w-3" />{t("customers.detail.billing.address")}
                                    </span>
                                    <span className="text-sm whitespace-pre-wrap">{customer.address}</span>
                                </div>
                            )}
                            {customer.contactName && (
                                <div className="flex gap-3 py-2 border-b">
                                    <span className="text-sm text-muted-foreground w-40 shrink-0 flex items-center gap-1">
                                        <User className="h-3 w-3" />{t("customers.detail.billing.contactName")}
                                    </span>
                                    <span className="text-sm font-medium">{customer.contactName}</span>
                                </div>
                            )}
                            {customer.contactPhone && (
                                <div className="flex gap-3 py-2 border-b">
                                    <span className="text-sm text-muted-foreground w-40 shrink-0 flex items-center gap-1">
                                        <Phone className="h-3 w-3" />{t("customers.detail.billing.contactPhone")}
                                    </span>
                                    <span className="text-sm font-medium">{customer.contactPhone}</span>
                                </div>
                            )}
                            {customer.billingEmail && (
                                <div className="flex gap-3 py-2 border-b">
                                    <span className="text-sm text-muted-foreground w-40 shrink-0 flex items-center gap-1">
                                        <Mail className="h-3 w-3" />{t("customers.detail.billing.billingEmail")}
                                    </span>
                                    <a href={`mailto:${customer.billingEmail}`} className="text-sm font-medium text-blue-500 hover:underline">
                                        {customer.billingEmail}
                                    </a>
                                </div>
                            )}
                            {customer.paymentTermsDays != null && (
                                <div className="flex gap-3 py-2 border-b">
                                    <span className="text-sm text-muted-foreground w-40 shrink-0 flex items-center gap-1">
                                        <Clock className="h-3 w-3" />{t("customers.detail.billing.paymentTermsDays")}
                                    </span>
                                    <span className="text-sm font-medium">
                                        {customer.paymentTermsDays} {t("customers.detail.billing.paymentTermsDaysSuffix")}
                                    </span>
                                </div>
                            )}
                            {customer.invoiceNote && (
                                <div className="flex gap-3 py-2">
                                    <span className="text-sm text-muted-foreground w-40 shrink-0 flex items-center gap-1">
                                        <FileText className="h-3 w-3" />{t("customers.detail.billing.invoiceNote")}
                                    </span>
                                    <span className="text-sm whitespace-pre-wrap">{customer.invoiceNote}</span>
                                </div>
                            )}
                        </div>
                    ) : (
                        <p className="text-sm text-muted-foreground">{t("customers.detail.billing.noInfo")}</p>
                    )}
                </CardContent>
            </Card>

            {/* ── Driver ID types ── */}
            <Card>
                <CardHeader>
                    <CardTitle>{t("customers.form.driverIdTypes")}</CardTitle>
                </CardHeader>
                <CardContent>
                    {(customer.driverIdTypes?.length ?? 0) > 0 ? (
                        <ul className="space-y-2">
                            {customer.driverIdTypes.map((dt, i) => (
                                <li key={i} className="flex items-center gap-2 py-2 border-b last:border-0">
                                    <span className="font-mono text-sm text-muted-foreground w-24">{dt.key}</span>
                                    <span className="font-medium">{dt.label}</span>
                                </li>
                            ))}
                        </ul>
                    ) : (
                        <p className="text-muted-foreground text-sm">-</p>
                    )}
                </CardContent>
            </Card>
        </div>
    );
}

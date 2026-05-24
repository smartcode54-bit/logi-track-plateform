"use client";

import { useEffect, useState } from "react";
import { useParams, usePathname } from "next/navigation";
import { getCustomerById, CustomerData } from "@/features/customers/api/customers";
import { getCustomerIdFromPathname } from "@/features/customers/utils/customerRouteId";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ArrowLeft, Building2, Loader2, Edit, Mail, Phone, User, MapPin, CreditCard, Clock, FileText } from "lucide-react";
import Link from "next/link";
import { useLanguage } from "@/context/language";

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
    const params = useParams();
    const pathname = usePathname();
    const { t } = useLanguage();
    const id =
        getCustomerIdFromPathname(pathname) ?? (params?.id as string | undefined) ?? "";
    const [customer, setCustomer] = useState<CustomerData | null>(null);
    const [loading, setLoading] = useState(true);

    useEffect(() => {
        const fetchData = async () => {
            if (!id) return;
            setLoading(true);
            try {
                const data = await getCustomerById(id);
                setCustomer(data);
            } catch (error) {
                console.error("Error loading customer", error);
            } finally {
                setLoading(false);
            }
        };
        fetchData();
    }, [id]);

    if (loading) {
        return (
            <div className="flex justify-center py-24">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
            </div>
        );
    }

    if (!customer) {
        return (
            <div className="container py-12 text-center">
                <h2 className="text-xl font-semibold mb-4">{t("customers.detail.notFound")}</h2>
                <Button asChild variant="outline">
                    <Link href="/app/customers" prefetch={false}>{t("customers.detail.backToList")}</Link>
                </Button>
            </div>
        );
    }

    const hasBillingInfo = !!(
        customer.taxId || customer.address || customer.branchType ||
        customer.contactName || customer.contactPhone || customer.billingEmail ||
        customer.paymentTermsDays != null || customer.invoiceNote
    );

    const branchLabel = customer.branchType
        ? `${customer.branchType}${customer.branchNumber ? ` ${customer.branchNumber}` : ""}`
        : undefined;

    return (
        <div className="container max-w-3xl py-8 space-y-6">
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-4">
                    <Button variant="ghost" size="icon" asChild>
                        <Link href="/app/customers" prefetch={false}>
                            <ArrowLeft className="h-5 w-5" />
                        </Link>
                    </Button>
                    <div>
                        <h1 className="text-2xl font-bold flex items-center gap-3">
                            {customer.logoUrl ? (
                                <img src={customer.logoUrl} alt={customer.name} className="h-10 w-10 rounded-full object-cover border shadow-sm" />
                            ) : (
                                <Building2 className="h-6 w-6" />
                            )}
                            {customer.name}
                        </h1>
                        <p className="text-muted-foreground font-mono">{customer.code}</p>
                    </div>
                </div>
                <Button asChild>
                    <Link href={`/app/customers/${customer.id}/edit`} prefetch={false} className="gap-2">
                        <Edit className="h-4 w-4" />
                        {t("customers.action.edit")}
                    </Link>
                </Button>
            </div>

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
                            {customer.driverIdTypes!.map((dt, i) => (
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

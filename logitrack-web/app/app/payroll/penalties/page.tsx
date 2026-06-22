"use client";

import { useEffect, useState } from "react";
import { collection, getDocs } from "firebase/firestore";
import { format } from "date-fns";
import { Loader2, Plus, Ban } from "lucide-react";
import { toast } from "sonner";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { useLanguage } from "@/context/language";
import { useAuth } from "@/context/auth";
import { listPenalties, createPenalty, cancelPenalty } from "@/features/driver-compensation/api/penalties";
import { getActiveCompensationConfig } from "@/features/driver-compensation/api/config";
import type { DriverPenalty } from "@/validate/driverPenaltySchema";

interface DriverOpt { authId: string; name: string }
interface TypeOpt { code: string; name: string; defaultAmountThb: number }

export default function PenaltiesPage() {
    const { t } = useLanguage();
    const auth = useAuth();
    const user = auth?.currentUser ?? null;

    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [penalties, setPenalties] = useState<DriverPenalty[]>([]);
    const [drivers, setDrivers] = useState<DriverOpt[]>([]);
    const [types, setTypes] = useState<TypeOpt[]>([]);

    const [driverId, setDriverId] = useState("");
    const [typeCode, setTypeCode] = useState("");
    const [amount, setAmount] = useState(0);
    const [installments, setInstallments] = useState(1);
    const [reason, setReason] = useState("");
    const [incurredAt, setIncurredAt] = useState(format(new Date(), "yyyy-MM-dd"));

    async function refreshList() {
        setPenalties(await listPenalties());
    }

    useEffect(() => {
        (async () => {
            try {
                const drvSnap = await getDocs(collection(db, COLLECTIONS.DRIVERS));
                const drvs: DriverOpt[] = [];
                drvSnap.forEach((d) => {
                    const data = d.data();
                    if (data.authId) {
                        drvs.push({
                            authId: data.authId,
                            name: `${data.firstName ?? ""} ${data.lastName ?? ""}`.trim() || data.fullNameTh || data.authId,
                        });
                    }
                });
                setDrivers(drvs.sort((a, b) => a.name.localeCompare(b.name)));

                const cfg = await getActiveCompensationConfig();
                setTypes((cfg?.penaltyTypes ?? []).map((p) => ({ code: p.code, name: p.nameEn, defaultAmountThb: p.defaultAmountThb })));

                await refreshList();
            } catch (e) {
                console.error(e);
            } finally {
                setLoading(false);
            }
        })();
    }, []);

    function onTypeChange(code: string) {
        setTypeCode(code);
        const tt = types.find((x) => x.code === code);
        if (tt && !amount) setAmount(tt.defaultAmountThb);
    }

    async function handleAdd() {
        if (!driverId || !typeCode || amount <= 0) {
            toast.error(t("driverComp.penalty.invalid"));
            return;
        }
        setSaving(true);
        try {
            const driver = drivers.find((d) => d.authId === driverId);
            const type = types.find((x) => x.code === typeCode);
            await createPenalty(
                {
                    driverId,
                    driverName: driver?.name,
                    typeCode,
                    typeName: type?.name,
                    totalThb: Math.round(amount),
                    installmentsTotal: Math.max(1, Math.round(installments)),
                    reason,
                    incurredAt: new Date(incurredAt),
                },
                user?.uid,
            );
            toast.success(t("driverComp.penalty.saved"));
            setAmount(0);
            setReason("");
            setInstallments(1);
            await refreshList();
        } catch (e) {
            console.error(e);
            toast.error(t("driverComp.penalty.saveError"));
        } finally {
            setSaving(false);
        }
    }

    async function handleCancel(id: string) {
        try {
            await cancelPenalty(id);
            await refreshList();
        } catch (e) {
            console.error(e);
            toast.error(t("driverComp.penalty.saveError"));
        }
    }

    if (loading) {
        return <div className="flex items-center justify-center p-12"><Loader2 className="h-6 w-6 animate-spin" /></div>;
    }

    return (
        <div className="space-y-6 p-4 md:p-6 max-w-5xl">
            <div>
                <h1 className="text-2xl font-semibold">{t("driverComp.penalty.title")}</h1>
                <p className="text-sm text-muted-foreground">{t("driverComp.penalty.subtitle")}</p>
            </div>

            <Card>
                <CardContent className="grid grid-cols-1 gap-3 pt-6 md:grid-cols-6">
                    <select className="h-9 rounded-md border bg-background px-2 text-sm md:col-span-2" value={driverId} onChange={(e) => setDriverId(e.target.value)}>
                        <option value="">{t("driverComp.penalty.driver")}</option>
                        {drivers.map((d) => <option key={d.authId} value={d.authId}>{d.name}</option>)}
                    </select>
                    <select className="h-9 rounded-md border bg-background px-2 text-sm" value={typeCode} onChange={(e) => onTypeChange(e.target.value)}>
                        <option value="">{t("driverComp.penalty.type")}</option>
                        {types.map((x) => <option key={x.code} value={x.code}>{x.name}</option>)}
                    </select>
                    <Input type="number" placeholder={t("driverComp.penalty.amount")} value={amount || ""} onChange={(e) => setAmount(Number(e.target.value))} />
                    <Input type="number" placeholder={t("driverComp.penalty.installments")} value={installments} onChange={(e) => setInstallments(Number(e.target.value))} />
                    <Input type="date" value={incurredAt} onChange={(e) => setIncurredAt(e.target.value)} />
                    <Input className="md:col-span-5" placeholder={t("driverComp.penalty.reason")} value={reason} onChange={(e) => setReason(e.target.value)} />
                    <Button onClick={handleAdd} disabled={saving}>
                        {saving ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Plus className="mr-2 h-4 w-4" />}
                        {t("driverComp.penalty.add")}
                    </Button>
                </CardContent>
            </Card>

            <Table>
                <TableHeader>
                    <TableRow>
                        <TableHead>{t("driverComp.penalty.driver")}</TableHead>
                        <TableHead>{t("driverComp.penalty.type")}</TableHead>
                        <TableHead>{t("driverComp.penalty.amount")}</TableHead>
                        <TableHead>{t("driverComp.penalty.remaining")}</TableHead>
                        <TableHead>{t("driverComp.penalty.status")}</TableHead>
                        <TableHead />
                    </TableRow>
                </TableHeader>
                <TableBody>
                    {penalties.map((p) => (
                        <TableRow key={p.id}>
                            <TableCell>{p.driverName || p.driverId}</TableCell>
                            <TableCell>{p.typeName || p.typeCode}</TableCell>
                            <TableCell>{p.totalThb.toLocaleString()}</TableCell>
                            <TableCell>{p.remainingThb.toLocaleString()}</TableCell>
                            <TableCell><Badge variant="outline">{p.status}</Badge></TableCell>
                            <TableCell>
                                {(p.status === "pending" || p.status === "partially_deducted") && (
                                    <Button variant="ghost" size="icon" onClick={() => handleCancel(p.id!)}>
                                        <Ban className="h-4 w-4" />
                                    </Button>
                                )}
                            </TableCell>
                        </TableRow>
                    ))}
                    {penalties.length === 0 && (
                        <TableRow><TableCell colSpan={6} className="text-center text-muted-foreground">{t("driverComp.penalty.none")}</TableCell></TableRow>
                    )}
                </TableBody>
            </Table>
        </div>
    );
}

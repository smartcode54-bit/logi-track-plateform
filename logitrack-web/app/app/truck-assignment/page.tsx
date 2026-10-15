"use client";

import { useCallback, useMemo, useState } from "react";
import {
    Truck,
    User,
    ClipboardList,
    ArrowRight,
    RotateCcw
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";

import { DriverSelect, TruckSelect } from "@/components/entity-pickers";
import { nodeText, type SearchSelectOption } from "@/components/ui/search-select";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import { toast } from "sonner";

import { useAuth } from "@/context/auth";
import type { DriverPick, TruckPick } from "@/features/accounting/api/pickers.go";
import { useActiveAssignments, useAssignmentWrites, useTruckStatusCounts } from "@/features/trucks/api/useTrucks";
import { trucksInService } from "@/features/trucks/api/trucks";
import type { TruckAssignmentDTO } from "@/features/trucks/api/truckAssignments";
import { AssignmentHistory } from "@/features/trucks/components/AssignmentHistory";
import { useDriverStatusCounts } from "@/features/drivers/api/useDrivers";
import { toastSaveError } from "@/lib/formServerErrors";

import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";

/** A picked driver or truck: its id and what the picker showed (name / plate, licence / truck type). */
interface Picked {
    id: string;
    label: string;
    detail: string;
}

const picked = (id: string, option: SearchSelectOption | null): Picked | null =>
    id && option ? { id, label: option.text ?? nodeText(option.label), detail: nodeText(option.description) } : null;

export default function TruckAssignmentPage() {
    const { t } = useLanguage();
    const auth = useAuth();
    const currentUser = auth?.currentUser;
    // `['truckAssignments', {status:'active'}]` (T25), Go `GET /v1/truck-assignments`; a write invalidates the
    // assignments, trucks and drivers. The pickers search on the server (UI2C): `DriverSelect` lists every
    // driver, an inactive one or one holding a truck shown but not pickable (Go refuses only those: an on_duty
    // driver without a truck, set on the driver form or loaded from the legacy app, is assignable),
    // `TruckSelect` the own fleet's trucks, so no driver or truck list is read to pick. The counters: `meta.statusCounts` of the drivers list and of
    // the own trucks (UI8: "total trucks" is every own truck not in maintenance, from a one-row page). The history
    // (`['truckAssignments', {view:'history', status, q}]`) is its own virtualised, server-filtered list
    // (`AssignmentHistory`, TW8).
    const active = useActiveAssignments();
    const driverCounts = useDriverStatusCounts();
    const truckCounts = useTruckStatusCounts("own");
    const { assign, revoke } = useAssignmentWrites();
    const isLoading = active.isPending || driverCounts.isPending || truckCounts.isPending;
    const isSubmitting = assign.isPending;
    const isRevoking = revoke.isPending;

    const stats = {
        totalTrucks: trucksInService(truckCounts.counts),
        // Available = active or on_duty, less the drivers holding a truck (Go sets a holder on_duty).
        availableDrivers: Math.max(
            0,
            (driverCounts.counts.active ?? 0) + (driverCounts.counts.on_duty ?? 0) - new Set(active.assignments.map((a) => a.driverId)).size
        ),
        activeAssignments: active.assignments.length,
    };

    // A driver who holds an active assignment cannot take another (Go: 409 driver_assigned): shown, not
    // pickable, with the truck it holds. Several drivers may share a truck; a truck in maintenance is not offered.
    const heldPlate = useMemo(() => new Map(active.assignments.map((a) => [a.driverId, a.truckPlate ?? ""])), [active.assignments]);
    const describeDriver = useCallback(
        (d: DriverPick): Partial<SearchSelectOption> | undefined => {
            if (heldPlate.has(d.id) || d.currentAssignment) {
                const plate = heldPlate.get(d.id) || d.currentAssignment?.truckPlate || "-";
                return { disabled: true, description: t("assignments.hint.assigned", { plate }) };
            }
            if (d.status === "inactive") return { disabled: true, description: t("assignments.hint.inactive") };
            return d.licenseType ? { description: d.licenseType } : undefined;
        },
        [heldPlate, t]
    );
    const describeTruck = useCallback(
        (tr: TruckPick): Partial<SearchSelectOption> | undefined => {
            if (tr.status === "maintenance") return { disabled: true, description: t("assignments.hint.maintenance") };
            const kind = tr.type || tr.model;
            return kind ? { description: kind } : undefined;
        },
        [t]
    );

    const [selectedDriver, setSelectedDriver] = useState<Picked | null>(null);
    const [selectedTruck, setSelectedTruck] = useState<Picked | null>(null);

    const [revokeDialogOpen, setRevokeDialogOpen] = useState(false);
    const [assignmentToRevoke, setAssignmentToRevoke] = useState<TruckAssignmentDTO | null>(null);

    const handleDeploy = async () => {
        if (!selectedDriver || !selectedTruck) {
            toast.error(t('assignments.toast.selectBoth'));
            return;
        }
        const driver = selectedDriver;
        const truck = selectedTruck;

        try {
            // TODO: QA Insight - Block Dispatch if Insurance is OVERDUE
            await assign.mutateAsync({
                truckId: truck.id,
                driverId: driver.id,
                driverName: driver.label,
                truckPlate: truck.label,
                truckModel: truck.detail,
                adminName: currentUser?.displayName || "Admin",
            });
            toast.success(t('assignments.toast.deploySuccess'));
            setSelectedDriver(null);
            setSelectedTruck(null);
        } catch (error) {
            toastSaveError(error, t, 'assignments.toast.deployError');
        }
    };

    const openRevokeDialog = useCallback((assignment: TruckAssignmentDTO) => {
        setAssignmentToRevoke(assignment);
        setRevokeDialogOpen(true);
    }, []);


    const handleConfirmRevoke = async () => {
        if (!assignmentToRevoke) return;
        try {
            await revoke.mutateAsync({ id: assignmentToRevoke.id, truckId: assignmentToRevoke.truckId, driverId: assignmentToRevoke.driverId });
            toast.success(t('assignments.toast.revokeSuccess'));
            setRevokeDialogOpen(false);
        } catch (error) {
            toastSaveError(error, t, 'assignments.toast.revokeError');
        }
    };

    return (
        <div className="flex flex-col gap-section">
            <PageHeader title={t("nav.truckAssignment")} description={t('assignments.subtitle')} />

            {/* Stats Cards */}
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <Card className="bg-card/50 border-border/50 shadow-sm relative overflow-hidden group">
                    <CardContent className="p-6">
                        <div className="flex justify-between items-start">
                            <div className="space-y-2">
                                <p className="text-xs font-semibold text-muted-foreground">{t('assignments.totalTrucks')}</p>
                                <div className="flex items-baseline gap-2">
                                    <span className="text-3xl font-bold font-mono text-foreground">
                                        {isLoading ? "-" : stats.totalTrucks}
                                    </span>
                                </div>
                            </div>
                            <div className="p-2 bg-primary/10 rounded-lg text-primary transition-transform">
                                <Truck className="h-5 w-5" />
                            </div>
                        </div>
                    </CardContent>
                </Card>

                <Card className="bg-card/50 border-border/50 shadow-sm relative overflow-hidden group">
                    <CardContent className="p-6">
                        <div className="flex justify-between items-start">
                            <div className="space-y-2">
                                <p className="text-xs font-semibold text-muted-foreground">{t('assignments.availableDrivers')}</p>
                                <div className="flex items-baseline gap-2">
                                    <span className="text-3xl font-bold font-mono text-foreground">
                                        {isLoading ? "-" : stats.availableDrivers}
                                    </span>
                                </div>
                            </div>
                            <div className="p-2 bg-primary/10 rounded-lg text-primary transition-transform">
                                <User className="h-5 w-5" />
                            </div>
                        </div>
                    </CardContent>
                </Card>

                <Card className="bg-card/50 border-border/50 shadow-sm relative overflow-hidden group">
                    <CardContent className="p-6">
                        <div className="flex justify-between items-start">
                            <div className="space-y-2">
                                <p className="text-xs font-semibold text-muted-foreground">{t('assignments.activeAssignments')}</p>
                                <div className="flex items-baseline gap-2">
                                    <span className="text-3xl font-bold font-mono text-foreground">
                                        {isLoading ? "-" : stats.activeAssignments}
                                    </span>
                                </div>
                            </div>
                            <div className="p-2 bg-primary/10 rounded-lg text-primary transition-transform">
                                <ClipboardList className="h-5 w-5" />
                            </div>
                        </div>
                    </CardContent>
                </Card>
            </div>

            {/* New Assignment Section */}
            <div className="rounded-xl border border-border/50 bg-card overflow-hidden">
                <div className="p-6 border-b border-border/50">
                    <h2 className="text-lg font-semibold flex items-center gap-2">
                        <ArrowRight className="h-4 w-4 text-primary" />
                        {t('assignments.new')}
                    </h2>
                </div>
                <div className="p-8">
                    <div className="flex flex-col lg:flex-row items-stretch gap-6">
                        {/* Driver Select */}
                        <div className="flex-1 space-y-4">
                            <label className="text-sm font-medium text-foreground">{t('assignments.selectDriver')}</label>

                            <DriverSelect
                                aria-label={t('assignments.selectDriver')}
                                details
                                describe={describeDriver}
                                value={selectedDriver?.id ?? ""}
                                selectedLabel={selectedDriver?.label}
                                onValueChange={(v, option) => setSelectedDriver(picked(v, option))}
                                placeholder={t('assignments.searchDriver')}
                                searchPlaceholder={t('assignments.searchDriverPlaceholder')}
                                emptyText={t('assignments.noDrivers')}
                                className="h-12 text-base bg-background/50 border-border/50"
                            />

                            <div className="h-24 rounded-lg border border-dashed border-border/60 flex items-center gap-4 px-4 bg-muted/20">
                                <div className="h-10 w-10 rounded-full bg-muted flex items-center justify-center text-muted-foreground">
                                    <User className="h-5 w-5" />
                                </div>
                                <span className="text-sm text-muted-foreground italic">
                                    {selectedDriver
                                        ? `${selectedDriver.label}${selectedDriver.detail ? ` - ${selectedDriver.detail}` : ''}`
                                        : t('assignments.noDriverSelected')
                                    }
                                </span>
                            </div>
                        </div>

                        {/* Arrow */}
                        <div className="hidden lg:flex items-center justify-center pt-8">
                            <div className="h-10 w-10 rounded-full bg-primary/10 text-primary flex items-center justify-center">
                                <ArrowRight className="h-5 w-5" />
                            </div>
                        </div>

                        {/* Truck Select */}
                        <div className="flex-1 space-y-4">
                            <label className="text-sm font-medium text-foreground">{t('assignments.selectTruck')}</label>

                            <TruckSelect
                                aria-label={t('assignments.selectTruck')}
                                ownership="own"
                                describe={describeTruck}
                                value={selectedTruck?.id ?? ""}
                                selectedLabel={selectedTruck?.label}
                                onValueChange={(v, option) => setSelectedTruck(picked(v, option))}
                                placeholder={t('assignments.searchTruck')}
                                searchPlaceholder={t('assignments.searchTruckPlaceholder')}
                                emptyText={t('assignments.noTrucks')}
                                className="h-12 text-base bg-background/50 border-border/50"
                            />

                            <div className="h-24 rounded-lg border border-dashed border-border/60 flex items-center gap-4 px-4 bg-muted/20">
                                <div className="h-10 w-10 rounded-full bg-muted flex items-center justify-center text-muted-foreground">
                                    <Truck className="h-5 w-5" />
                                </div>
                                <span className="text-sm text-muted-foreground italic">
                                    {selectedTruck
                                        ? `${selectedTruck.detail ? `${selectedTruck.detail} - ` : ''}${selectedTruck.label}`
                                        : t('assignments.noTruckSelected')
                                    }
                                </span>
                            </div>
                        </div>
                    </div>

                    <div className="mt-8 flex flex-col items-center gap-4">
                        <div className="flex flex-col sm:flex-row items-center gap-3 w-full max-w-sm">
                            <Button
                                size="lg"
                                className="w-full sm:flex-1 h-12 text-base font-semibold"
                                onClick={handleDeploy}
                                disabled={isSubmitting}
                            >
                                {isSubmitting ? t('assignments.deploying') : t('assignments.deploy')}
                            </Button>
                            <Button
                                type="button"
                                variant="outline"
                                size="lg"
                                className="w-full sm:w-auto h-12 text-base"
                                onClick={() => {
                                    setSelectedDriver(null);
                                    setSelectedTruck(null);
                                }}
                                disabled={isSubmitting}
                            >
                                <RotateCcw className="h-4 w-4 mr-2" />
                                {t('assignments.resetForm')}
                            </Button>
                        </div>

                    </div>
                </div>
            </div>

            {/* History: server-filtered keyset pages in a virtualised table (TW8) */}
            <AssignmentHistory onRevoke={openRevokeDialog} />
            {/* Revoke Confirmation Dialog */}
            <Dialog open={revokeDialogOpen} onOpenChange={setRevokeDialogOpen}>
                <DialogContent>
                    <DialogHeader>
                        <DialogTitle>{t('assignments.revoke.title')}</DialogTitle>
                        <DialogDescription>
                            {t('assignments.revoke.desc')}
                            <br />
                            {t('masterData.assignments.revokeEffect', { plate: assignmentToRevoke?.truckPlate ?? "-", driver: assignmentToRevoke?.driverName ?? "-" })}
                        </DialogDescription>
                    </DialogHeader>
                    <DialogFooter>
                        <Button variant="outline" onClick={() => setRevokeDialogOpen(false)} disabled={isRevoking}>
                            {t('assignments.revoke.cancel')}
                        </Button>
                        <Button variant="destructive" onClick={handleConfirmRevoke} disabled={isRevoking}>
                            {isRevoking ? t('assignments.revoke.revoking') : t('assignments.revoke.confirm')}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
}

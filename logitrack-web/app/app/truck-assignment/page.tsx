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

import { SearchSelect } from "@/components/ui/search-select";
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
import { useActiveAssignments, useAssignmentWrites, useTruckOptions } from "@/features/trucks/api/useTrucks";
import type { TruckAssignmentDTO } from "@/features/trucks/api/truckAssignments";
import { AssignmentHistory } from "@/features/trucks/components/AssignmentHistory";
import { useDriverOptions } from "@/features/drivers/api/useDrivers";
import { toastSaveError } from "@/lib/formServerErrors";

import { useLanguage } from "@/context/language";

export default function TruckAssignmentPage() {
    const { t } = useLanguage();
    const auth = useAuth();
    const currentUser = auth?.currentUser;
    // `['truckAssignments', …]`, `['drivers','options']`, `['trucks','options']` (T25): Go
    // `GET /v1/truck-assignments` and the pickers, or Firestore by the `masterdata` flag; a write
    // invalidates the assignments, trucks and drivers instead of reloading four collections.
    // The history (`['truckAssignments', {view:'history', status, q}]`) is its own virtualised,
    // server-filtered list (`AssignmentHistory`, TW8).
    const active = useActiveAssignments();
    const driverOptions = useDriverOptions();
    const truckOptions = useTruckOptions();
    const { assign, revoke } = useAssignmentWrites();
    const isLoading = active.isPending || driverOptions.isPending || truckOptions.isPending;
    const isSubmitting = assign.isPending;
    const isRevoking = revoke.isPending;

    // A driver with an active assignment is not offered again (several drivers may share a truck).
    const drivers = useMemo(() => {
        const assigned = new Set(active.assignments.map((a) => a.driverId));
        return driverOptions.options
            .filter((d) => !assigned.has(d.id))
            .map((d) => ({ id: d.id, name: d.displayName, licenseType: d.licenseType ?? undefined }));
    }, [active.assignments, driverOptions.options]);
    const trucks = truckOptions.options;
    const stats = { totalTrucks: trucks.length, availableDrivers: drivers.length, activeAssignments: active.assignments.length };

    const [selectedDriver, setSelectedDriver] = useState<string>("");
    const [selectedTruck, setSelectedTruck] = useState<string>("");

    const [revokeDialogOpen, setRevokeDialogOpen] = useState(false);
    const [assignmentToRevoke, setAssignmentToRevoke] = useState<TruckAssignmentDTO | null>(null);

    const handleDeploy = async () => {
        if (!selectedDriver || !selectedTruck) {
            toast.error(t('assignments.toast.selectBoth'));
            return;
        }
        const driver = drivers.find(d => d.id === selectedDriver);
        const truck = trucks.find(t => t.id === selectedTruck);
        if (!driver || !truck) return;

        try {
            // TODO: QA Insight - Block Dispatch if Insurance is OVERDUE
            await assign.mutateAsync({
                truckId: truck.id,
                driverId: driver.id,
                driverName: driver.name,
                truckPlate: truck.plate,
                truckModel: truck.model,
                adminName: currentUser?.displayName || "Admin",
            });
            toast.success(t('assignments.toast.deploySuccess'));
            setSelectedDriver("");
            setSelectedTruck("");
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
        <div className="min-h-screen bg-background text-foreground p-6 space-y-6">
            {/* Header */}
            <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t('assignments.title')}</h1>
                    <p className="text-muted-foreground mt-1">
                        {t('assignments.subtitle')}
                    </p>
                </div>
                {/* Removed placeholder buttons */}
            </div>

            {/* Stats Cards */}
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <Card className="bg-card/50 border-border/50 shadow-sm relative overflow-hidden group">
                    <CardContent className="p-6">
                        <div className="flex justify-between items-start">
                            <div className="space-y-2">
                                <p className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">{t('assignments.totalTrucks')}</p>
                                <div className="flex items-baseline gap-2">
                                    <span className="text-3xl font-bold font-mono text-foreground">
                                        {isLoading ? "-" : stats.totalTrucks}
                                    </span>
                                    <span className="text-xs font-medium text-green-500">~+5%</span>
                                </div>
                            </div>
                            <div className="p-2 bg-blue-500/10 rounded-lg text-blue-500 group-hover:scale-110 transition-transform">
                                <Truck className="h-5 w-5" />
                            </div>
                        </div>
                    </CardContent>
                </Card>

                <Card className="bg-card/50 border-border/50 shadow-sm relative overflow-hidden group">
                    <CardContent className="p-6">
                        <div className="flex justify-between items-start">
                            <div className="space-y-2">
                                <p className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">{t('assignments.availableDrivers')}</p>
                                <div className="flex items-baseline gap-2">
                                    <span className="text-3xl font-bold font-mono text-foreground">
                                        {isLoading ? "-" : stats.availableDrivers}
                                    </span>
                                    <span className="text-xs font-medium text-orange-500">↘-2%</span>
                                </div>
                            </div>
                            <div className="p-2 bg-blue-500/10 rounded-lg text-blue-500 group-hover:scale-110 transition-transform">
                                <User className="h-5 w-5" />
                            </div>
                        </div>
                    </CardContent>
                </Card>

                <Card className="bg-card/50 border-border/50 shadow-sm relative overflow-hidden group">
                    <CardContent className="p-6">
                        <div className="flex justify-between items-start">
                            <div className="space-y-2">
                                <p className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">{t('assignments.activeAssignments')}</p>
                                <div className="flex items-baseline gap-2">
                                    <span className="text-3xl font-bold font-mono text-foreground">
                                        {isLoading ? "-" : stats.activeAssignments}
                                    </span>
                                    <span className="text-xs font-medium text-green-500">~+12%</span>
                                </div>
                            </div>
                            <div className="p-2 bg-blue-500/10 rounded-lg text-blue-500 group-hover:scale-110 transition-transform">
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
                        <ArrowRight className="h-4 w-4 text-blue-500" />
                        {t('assignments.new')}
                    </h2>
                </div>
                <div className="p-8">
                    <div className="flex flex-col lg:flex-row items-stretch gap-6">
                        {/* Driver Select */}
                        <div className="flex-1 space-y-4">
                            <label className="text-sm font-medium text-foreground">{t('assignments.selectDriver')}</label>

                            <SearchSelect
                                options={drivers.map(d => ({
                                    value: d.id,
                                    label: `${d.name} ${d.licenseType ? `- ${d.licenseType}` : ''}`
                                }))}
                                value={selectedDriver}
                                onValueChange={(v) => setSelectedDriver(v)}
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
                                    {drivers.find(d => d.id === selectedDriver)
                                        ? `${drivers.find(d => d.id === selectedDriver)?.name} ${drivers.find(d => d.id === selectedDriver)?.licenseType ? `- ${drivers.find(d => d.id === selectedDriver)?.licenseType}` : ''}`
                                        : t('assignments.noDriverSelected')
                                    }
                                </span>
                            </div>
                        </div>

                        {/* Arrow */}
                        <div className="hidden lg:flex items-center justify-center pt-8">
                            <div className="h-10 w-10 rounded-full bg-blue-600/10 text-blue-500 flex items-center justify-center">
                                <ArrowRight className="h-5 w-5" />
                            </div>
                        </div>

                        {/* Truck Select */}
                        <div className="flex-1 space-y-4">
                            <label className="text-sm font-medium text-foreground">{t('assignments.selectTruck')}</label>

                            <SearchSelect
                                options={trucks.map(t => ({
                                    value: t.id,
                                    label: `${t.type || t.model} - ${t.plate}`
                                }))}
                                value={selectedTruck}
                                onValueChange={(v) => setSelectedTruck(v)}
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
                                    {trucks.find(t => t.id === selectedTruck)
                                        ? `${trucks.find(t => t.id === selectedTruck)?.type || trucks.find(t => t.id === selectedTruck)?.model} - ${trucks.find(t => t.id === selectedTruck)?.plate}`
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
                                className="w-full sm:flex-1 bg-blue-600 hover:bg-blue-700 text-white shadow-lg shadow-blue-900/20 h-12 text-base font-semibold"
                                onClick={handleDeploy}
                                disabled={isSubmitting}
                            >
                                {isSubmitting ? t('assignments.deploying') : t('assignments.deploy')}
                            </Button>
                            <Button
                                type="button"
                                variant="outline"
                                size="lg"
                                className="w-full sm:w-auto h-12 text-base border-border/50 bg-background/50 hover:bg-muted/50"
                                onClick={() => {
                                    setSelectedDriver("");
                                    setSelectedTruck("");
                                }}
                                disabled={isSubmitting}
                            >
                                <RotateCcw className="h-4 w-4 mr-2" />
                                {t('assignments.resetForm')}
                            </Button>
                        </div>
                        <div className="flex items-center gap-2 text-xs text-muted-foreground">
                            <span className="relative flex h-2 w-2">
                                <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-green-400 opacity-75"></span>
                                <span className="relative inline-flex rounded-full h-2 w-2 bg-green-500"></span>
                            </span>
                            {t('assignments.syncing')}
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

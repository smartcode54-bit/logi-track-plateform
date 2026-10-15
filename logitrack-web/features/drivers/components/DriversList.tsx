"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { useDriverPages } from "@/features/drivers/api/useDrivers";
import type { DriverStatus } from "@/features/drivers/api/drivers";
import { useDebouncedValue } from "@/hooks/useDebouncedValue";
import { LoadMore } from "@/components/load-more";
import {
    Loader2,
    Users,
    Activity,
    CalendarOff,
    MoreHorizontal,
    Plus,
    MessageCircle
} from "lucide-react";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { driverStatusTone, toneBadgeVariant } from "@/lib/statusPresentation";
import { Card, CardContent } from "@/components/ui/card";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
import { chatWithDriverHref } from "@/features/chat/api/chats.go";


export default function DriversList() {
    const router = useRouter();
    const { t } = useLanguage();

    // Filters: sent to the server (`GET /v1/drivers?status&q`, T25).
    const [searchQuery, setSearchQuery] = useState("");
    const [statusFilter, setStatusFilter] = useState<"all" | DriverStatus>("all");
    const q = useDebouncedValue(searchQuery.trim());

    // Pagination over the loaded rows; "load more" fetches the next keyset page.
    const [currentPage, setCurrentPage] = useState(1);
    const itemsPerPage = 10;
    useEffect(() => setCurrentPage(1), [q, statusFilter]);

    // `['drivers', {status, q}]`: keyset pages of 100 (no 100-row cap), polled every 60 s while the
    // tab is visible (Appendix E §E.5 row 23, replacing the onSnapshot listener).
    const pages = useDriverPages({ status: statusFilter === "all" ? "" : statusFilter, q });
    const loading = pages.isPending;
    const filteredDrivers = pages.drivers;

    // Stat cards: the server's counts over every matching driver, not only the loaded rows.
    const counts = pages.statusCounts;
    const stats = {
        total: (counts.active ?? 0) + (counts.on_duty ?? 0) + (counts.inactive ?? 0),
        active: (counts.active ?? 0) + (counts.on_duty ?? 0),
        onLeave: counts.inactive ?? 0,
    };

    const paginatedDrivers = filteredDrivers.slice(
        (currentPage - 1) * itemsPerPage,
        currentPage * itemsPerPage
    );
    const totalPages = Math.ceil(filteredDrivers.length / itemsPerPage);

    return (
        <div className="flex flex-col gap-section">
            <PageHeader
                title={t("nav.drivers")}
                description={t("drivers.subtitle")}
                actions={
                    <>
                        <Button asChild>
                            <Link href="/app/drivers/new" prefetch={false}>
                                <Plus className="mr-2 h-4 w-4" aria-hidden />
                                {t("drivers.add")}
                            </Link>
                        </Button>
                    </>
                }
            />

            {/* Stats Cards */}
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <Card className="bg-card">
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">{t("drivers.stats.totalDrivers")}</p>
                            <div className="flex items-baseline gap-2 mt-1">
                                <h2 className="text-3xl font-bold">{stats.total}</h2>
                            </div>
                        </div>
                        <div className="h-12 w-12 rounded-full bg-muted flex items-center justify-center text-muted-foreground">
                            <Users className="h-6 w-6" />
                        </div>
                    </CardContent>
                </Card>
                <Card className="bg-card">
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">{t("drivers.stats.activeOnDuty")}</p>
                            <div className="flex items-baseline gap-2 mt-1">
                                <h2 className="text-3xl font-bold">{stats.active}</h2>
                            </div>
                        </div>
                        <div className="h-12 w-12 rounded-full bg-primary/10 flex items-center justify-center text-primary">
                            <Activity className="h-6 w-6" />
                        </div>
                    </CardContent>
                </Card>
                <Card className="bg-card">
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">{t("drivers.stats.inactive")}</p>
                            <div className="flex items-baseline gap-2 mt-1">
                                <h2 className="text-3xl font-bold">{stats.onLeave}</h2>
                            </div>
                        </div>
                        <div className="h-12 w-12 rounded-full bg-muted flex items-center justify-center text-muted-foreground">
                            <CalendarOff className="h-6 w-6" />
                        </div>
                    </CardContent>
                </Card>
            </div>

            {/* Main Content & Filters */}
            <div className="space-y-4">
                <div className="flex flex-col sm:flex-row gap-4 bg-card/50 p-4 rounded-lg border border-border/50">
                    <div className="relative flex-1">
                        <Users className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                        <Input
                            placeholder={t("drivers.search")}
                            className="pl-10 bg-background/50 border-border/50"
                            value={searchQuery}
                            onChange={(e) => setSearchQuery(e.target.value)}
                        />
                    </div>
                    <div className="flex gap-2 overflow-x-auto pb-2 sm:pb-0">
                        <Button
                            variant={statusFilter === "all" ? "secondary" : "ghost"}
                            onClick={() => setStatusFilter("all")}
                            className="whitespace-nowrap"
                        >
                            {t("drivers.filter.all")}
                        </Button>
                        <Button
                            variant={statusFilter === "on_duty" ? "secondary" : "ghost"}
                            onClick={() => setStatusFilter("on_duty")}
                            className="whitespace-nowrap"
                        >
                            {t("drivers.filter.onDuty")}
                        </Button>
                        <Button
                            variant={statusFilter === "active" ? "secondary" : "ghost"}
                            onClick={() => setStatusFilter("active")}
                            className="whitespace-nowrap"
                        >
                            {t("drivers.filter.active")}
                        </Button>
                        <Button
                            variant={statusFilter === "inactive" ? "secondary" : "ghost"}
                            onClick={() => setStatusFilter("inactive")}
                            className="whitespace-nowrap"
                        >
                            {t("drivers.filter.inactive")}
                        </Button>
                    </div>
                </div>

                <div className="border rounded-lg bg-card overflow-hidden">
                    <Table>
                        <TableHeader className="bg-muted/50">
                            <TableRow className="border-b border-border/50">
                                <TableHead>{t("drivers.table.driver")}</TableHead>
                                <TableHead>{t("drivers.table.contact")}</TableHead>
                                <TableHead>{t("drivers.table.employment")}</TableHead>
                                <TableHead>{t("drivers.table.assignedVehicle")}</TableHead>
                                <TableHead>{t("drivers.table.licenseId")}</TableHead>
                                <TableHead>{t("drivers.table.licenseType")}</TableHead>
                                <TableHead>{t("drivers.table.status")}</TableHead>
                                <TableHead className="text-right">{t("drivers.table.actions")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {loading ? (
                                <TableRow>
                                    <TableCell colSpan={6} className="h-32 text-center">
                                        <div className="flex flex-col items-center justify-center gap-2">
                                            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                                            <p className="text-sm text-muted-foreground">{t("drivers.loading")}</p>
                                        </div>
                                    </TableCell>
                                </TableRow>
                            ) : paginatedDrivers.length === 0 ? (
                                <TableRow>
                                    <TableCell colSpan={6} className="h-32 text-center">
                                        <p className="text-sm text-muted-foreground">{t("drivers.noData")}</p>
                                    </TableCell>
                                </TableRow>
                            ) : (
                                paginatedDrivers.map((driver) => (
                                    <TableRow key={driver.id} className="cursor-pointer hover:bg-muted/50 transition-colors border-b border-border/50" onClick={() => router.push(`/app/drivers/view?id=${driver.id}`)}>
                                        <TableCell>
                                            <div className="flex items-center gap-3">
                                                <Avatar className="h-9 w-9 border border-border">
                                                    <AvatarImage src={driver.profileUrl ?? undefined} alt={driver.firstName} />
                                                    <AvatarFallback>{(driver.firstName || driver.displayName).slice(0, 1).toUpperCase()}{driver.lastName.slice(0, 1).toUpperCase()}</AvatarFallback>
                                                </Avatar>
                                                <div className="flex flex-col">
                                                    <span className="font-medium text-sm text-foreground">{driver.firstName} {driver.lastName}</span>
                                                    <span className="text-xs text-muted-foreground">{driver.email}</span>
                                                </div>
                                            </div>
                                        </TableCell>
                                        <TableCell className="text-sm text-muted-foreground">{driver.mobile}</TableCell>
                                        <TableCell>
                                            <div className="flex flex-col">
                                                <span className="text-sm font-medium">{t(`masterData.drivers.employment.${driver.employmentType}`)}</span>
                                                {driver.employmentType === 'subcontractor' && driver.tenantNameTh && (
                                                    <span className="text-xs text-muted-foreground">{driver.tenantNameTh}</span>
                                                )}
                                            </div>
                                        </TableCell>
                                        <TableCell>
                                            {driver.currentAssignment ? (
                                                <div className="flex flex-col">
                                                    <span className="font-medium text-sm text-primary">{driver.currentAssignment.truckPlate}</span>
                                                    <span className="text-xs text-muted-foreground">{driver.currentAssignment.truckModel}</span>
                                                </div>
                                            ) : (
                                                <span className="text-muted-foreground text-xs italic">{t("drivers.table.unassigned")}</span>
                                            )}
                                        </TableCell>
                                        <TableCell>
                                            <span className="font-mono text-xs">{driver.truckLicenseId}</span>
                                        </TableCell>
                                        <TableCell>
                                            <span className="text-sm text-muted-foreground">{driver.licenseType || "-"}</span>
                                        </TableCell>
                                        <TableCell>
                                            <Badge variant={toneBadgeVariant(driverStatusTone(driver.status))}>
                                                <span className="w-1.5 h-1.5 rounded-full bg-current mr-1.5" />
                                                {t(`masterData.drivers.status.${driver.status}`)}
                                            </Badge>
                                        </TableCell>
                                        <TableCell className="text-right">
                                            <DropdownMenu>
                                                <DropdownMenuTrigger asChild>
                                                    <Button aria-label={t("common.actionsFor", { name: `${driver.firstName} ${driver.lastName}`.trim() })} variant="ghost" className="h-8 w-8 p-0">
                                                        <MoreHorizontal className="h-4 w-4" />
                                                    </Button>
                                                </DropdownMenuTrigger>
                                                <DropdownMenuContent align="end" onClick={(e) => e.stopPropagation()}>
                                                    <DropdownMenuLabel>{t("drivers.action.label")}</DropdownMenuLabel>
                                                    <DropdownMenuItem onClick={(e) => { e.stopPropagation(); router.push(`/app/drivers/view?id=${driver.id}`); }}>{t("drivers.action.viewProfile")}</DropdownMenuItem>
                                                    <DropdownMenuItem onClick={(e) => { e.stopPropagation(); router.push(`/app/drivers/edit?id=${driver.id}`); }}>{t("drivers.action.editDetails")}</DropdownMenuItem>
                                                    {(driver.legacyAuthUid || driver.userId) && (
                                                        <DropdownMenuItem onClick={(e) => { e.stopPropagation(); router.push(chatWithDriverHref(driver)); }} className="gap-2">
                                                            <MessageCircle className="h-4 w-4" />
                                                            {t("masterData.drivers.chat")}
                                                        </DropdownMenuItem>
                                                    )}
                                                </DropdownMenuContent>
                                            </DropdownMenu>
                                        </TableCell>
                                    </TableRow>
                                ))
                            )}
                        </TableBody>
                    </Table>

                    {/* Pagination Footer */}
                    <div className="px-4 pt-4 border-t border-border/50 bg-muted/20">
                        <LoadMore
                            loaded={filteredDrivers.length}
                            hasNextPage={pages.hasNextPage}
                            isFetchingNextPage={pages.isFetchingNextPage}
                            onLoadMore={() => void pages.fetchNextPage()}
                        />
                    </div>
                    <div className="flex flex-wrap items-center justify-between gap-4 px-4 py-4 bg-muted/20">
                        <div className="text-sm text-muted-foreground">
                            {t("drivers.pagination.showing")} {paginatedDrivers.length > 0 ? (currentPage - 1) * itemsPerPage + 1 : 0} {t("drivers.pagination.to")} {Math.min(currentPage * itemsPerPage, filteredDrivers.length)} {t("drivers.pagination.of")} {filteredDrivers.length} {t("drivers.pagination.entries")}
                        </div>
                        <div className="flex items-center gap-2">
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={() => setCurrentPage(p => Math.max(1, p - 1))}
                                disabled={currentPage === 1}
                            >
                                {t("drivers.pagination.previous")}
                            </Button>
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={() => setCurrentPage(p => Math.min(totalPages, p + 1))}
                                disabled={currentPage === totalPages || totalPages === 0}
                            >
                                {t("drivers.pagination.next")}
                            </Button>
                        </div>
                    </div>
                </div>
            </div>
        </div>
    );
}

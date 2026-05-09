"use client";

import { useLanguage } from "@/context/language";
import { SubcontractorData } from "../services/subcontractorService";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Badge } from "@/components/ui/badge";
import { MoreHorizontal, ArrowUp, ArrowDown, Loader2 } from "lucide-react";
import { useRouter } from "next/navigation";

interface SubcontractorTableProps {
    subcontractors: SubcontractorData[];
    loading: boolean;
    currentPage: number;
    setCurrentPage: (p: number | ((prev: number) => number)) => void;
    itemsPerPage: number;
    filteredCount: number;
    sortConfig: { key: keyof SubcontractorData; direction: 'asc' | 'desc' } | null;
    handleSort: (key: keyof SubcontractorData) => void;
}

export function SubcontractorTable(props: SubcontractorTableProps) {
    const { t } = useLanguage();
    const router = useRouter();

    const getStatusBadge = (status: string) => {
        switch (status) {
            case 'active':
                return <Badge className="bg-green-500/10 text-green-600 hover:bg-green-500/20 border-green-500/20 shadow-none uppercase text-[10px] tracking-wider px-2 py-0.5">{t("subcontractors.status.active")}</Badge>;
            case 'pending':
                return <Badge className="bg-orange-500/10 text-orange-600 hover:bg-orange-500/20 border-orange-500/20 shadow-none uppercase text-[10px] tracking-wider px-2 py-0.5">{t("subcontractors.status.onTrial")}</Badge>;
            case 'suspended':
                return <Badge className="bg-red-500/10 text-red-600 hover:bg-red-500/20 border-red-500/20 shadow-none uppercase text-[10px] tracking-wider px-2 py-0.5">{t("subcontractors.status.terminated")}</Badge>;
            default:
                return <Badge variant="secondary">{status}</Badge>;
        }
    };

    return (
        <div className="rounded-xl border border-border/50 bg-card overflow-hidden">
            <Table>
                <TableHeader className="bg-muted/30">
                    <TableRow className="hover:bg-transparent border-border/50">
                        <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase pl-6 cursor-pointer" onClick={() => props.handleSort('name')}>
                            <div className="flex items-center gap-1">
                                {t("subcontractors.table.companyName")} 
                                {props.sortConfig?.key === 'name' && (props.sortConfig.direction === 'asc' ? <ArrowUp className="w-3 h-3" /> : <ArrowDown className="w-3 h-3" />)}
                            </div>
                        </TableHead>
                        <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase cursor-pointer" onClick={() => props.handleSort('contactPerson')}>
                            <div className="flex items-center gap-1">
                                {t("subcontractors.table.primaryContact")} 
                                {props.sortConfig?.key === 'contactPerson' && (props.sortConfig.direction === 'asc' ? <ArrowUp className="w-3 h-3" /> : <ArrowDown className="w-3 h-3" />)}
                            </div>
                        </TableHead>
                        <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase cursor-pointer" onClick={() => props.handleSort('fleetSize')}>
                            <div className="flex items-center gap-1">
                                {t("subcontractors.table.fleetSize")} 
                                {props.sortConfig?.key === 'fleetSize' && (props.sortConfig.direction === 'asc' ? <ArrowUp className="w-3 h-3" /> : <ArrowDown className="w-3 h-3" />)}
                            </div>
                        </TableHead>
                        <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">{t("subcontractors.table.serviceArea")}</TableHead>
                        <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">{t("subcontractors.table.status")}</TableHead>
                        <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase text-right pr-6">{t("subcontractors.table.actions")}</TableHead>
                    </TableRow>
                </TableHeader>
                <TableBody>
                    {props.loading ? (
                        <TableRow>
                            <TableCell colSpan={6} className="h-32 text-center text-muted-foreground">
                                <Loader2 className="h-6 w-6 animate-spin mx-auto mb-2" />
                                {t("subcontractors.loading")}
                            </TableCell>
                        </TableRow>
                    ) : props.subcontractors.length === 0 ? (
                        <TableRow>
                            <TableCell colSpan={6} className="h-32 text-center text-muted-foreground">{t("subcontractors.noData")}</TableCell>
                        </TableRow>
                    ) : (
                        props.subcontractors.map((sub) => (
                            <TableRow key={sub.id} className="border-border/50 hover:bg-muted/30 transition-colors group">
                                <TableCell className="pl-6 py-4">
                                    <div className="flex gap-3 items-center">
                                        <div className="h-9 w-9 rounded-lg bg-blue-100 dark:bg-blue-900/40 text-blue-600 flex items-center justify-center font-bold text-sm">
                                            {sub.id.substring(0, 2).toUpperCase()}
                                        </div>
                                        <div>
                                            <div className="font-semibold text-foreground text-sm">{sub.name}</div>
                                            <div className="text-xs text-muted-foreground font-mono">ID: {sub.id.substring(0, 6).toUpperCase()}</div>
                                        </div>
                                    </div>
                                </TableCell>
                                <TableCell>
                                    <div className="space-y-0.5">
                                        <div className="text-sm font-medium text-foreground">{sub.contactPerson}</div>
                                        <div className="text-xs text-muted-foreground">{sub.email || sub.phone}</div>
                                    </div>
                                </TableCell>
                                <TableCell>
                                    <div className="flex items-center gap-2">
                                        <span className="font-mono font-bold text-foreground">{sub.fleetSize || 0}</span>
                                        <span className="text-xs text-muted-foreground">{t("subcontractors.table.vehicles")}</span>
                                    </div>
                                </TableCell>
                                <TableCell>
                                    <span className="text-sm text-foreground/80">{sub.serviceArea || t("subcontractors.table.global")}</span>
                                </TableCell>
                                <TableCell>
                                    {getStatusBadge(sub.status)}
                                </TableCell>
                                <TableCell className="text-right pr-6">
                                    <DropdownMenu>
                                        <DropdownMenuTrigger asChild>
                                            <Button variant="ghost" size="icon" className="h-8 w-8 hover:bg-muted opacity-0 group-hover:opacity-100 transition-opacity">
                                                <MoreHorizontal className="h-4 w-4" />
                                            </Button>
                                        </DropdownMenuTrigger>
                                        <DropdownMenuContent align="end">
                                            <DropdownMenuItem onClick={() => router.push(`/app/subcontractors/${sub.id}`)}>
                                                {t("subcontractors.action.viewProfile")}
                                            </DropdownMenuItem>
                                            <DropdownMenuItem>{t("subcontractors.action.manageContracts")}</DropdownMenuItem>
                                            <DropdownMenuItem className="text-destructive focus:text-destructive">{t("subcontractors.action.suspendPartner")}</DropdownMenuItem>
                                        </DropdownMenuContent>
                                    </DropdownMenu>
                                </TableCell>
                            </TableRow>
                        ))
                    )}
                </TableBody>
            </Table>

            {/* Pagination */}
            <div className="flex items-center justify-between px-6 py-4 border-t border-border/50 bg-muted/20">
                <div className="text-xs text-muted-foreground">
                    {t("subcontractors.pagination.showing")}{" "}
                    <span className="font-medium text-foreground">
                        {props.filteredCount === 0 ? 0 : (props.currentPage - 1) * props.itemsPerPage + 1}
                    </span>{" "}
                    {t("subcontractors.pagination.to")}{" "}
                    <span className="font-medium text-foreground">
                        {Math.min(props.currentPage * props.itemsPerPage, props.filteredCount)}
                    </span>{" "}
                    {t("subcontractors.pagination.of")}{" "}
                    <span className="font-medium text-foreground">{props.filteredCount}</span>{" "}
                    {t("subcontractors.pagination.results")}
                </div>
                <div className="flex items-center gap-2">
                    <Button
                        variant="outline"
                        size="sm"
                        className="h-8 bg-background/50"
                        onClick={() => props.setCurrentPage(p => Math.max(1, p - 1))}
                        disabled={props.currentPage === 1}
                    >
                        {t("subcontractors.pagination.previous")}
                    </Button>
                    <Button
                        variant="outline"
                        size="sm"
                        className="h-8 bg-background/50"
                        onClick={() => props.setCurrentPage(p => Math.min(Math.ceil(props.filteredCount / props.itemsPerPage), p + 1))}
                        disabled={props.currentPage >= Math.ceil(props.filteredCount / props.itemsPerPage)}
                    >
                        {t("subcontractors.pagination.next")}
                    </Button>
                </div>
            </div>
        </div>
    );
}

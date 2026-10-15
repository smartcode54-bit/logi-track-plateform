"use client";

import { useMemo, useState } from "react";
import { createColumnHelper } from "@tanstack/react-table";
import { MoreHorizontal, Search, Settings } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { LoadMore } from "@/components/load-more";
import { VirtualTable } from "@/components/virtual-table";
import { useLanguage } from "@/context/language";
import { useDebouncedValue } from "@/hooks/useDebouncedValue";
import { useAssignmentHistory } from "../api/useTrucks";
import type { AssignmentStatus, TruckAssignmentDTO } from "../api/truckAssignments";

const column = createColumnHelper<TruckAssignmentDTO>();

/** A history row with its avatar and badge (`py-4` cells), px: the virtualiser's estimate before it measures. */
export const HISTORY_ROW_HEIGHT = 57;

/**
 * The longest search Go takes (`GET /v1/truck-assignments?q`, Appendix B §B.2.8: more characters is
 * `422 q too_long`). `maxLength` counts UTF-16 units and Go counts characters, so this never sends more.
 */
export const ASSIGNMENT_SEARCH_MAX = 100;

interface AssignmentHistoryProps {
    /** Opens the page's revoke dialog for an active row. */
    onRevoke: (assignment: TruckAssignmentDTO) => void;
}

/**
 * The assignment history of `/app/truck-assignment` (TW8, developer-spec.md §10.10 W9):
 * `['truckAssignments', {view:'history', status, q}]` keyset pages of 100, newest first, with the search
 * and the status sent to Go (`GET /v1/truck-assignments?status&q`), so a search reaches rows that are not
 * loaded yet. The table is virtualised (TanStack Table v8 + Virtual): only the rows in view are in the
 * DOM however many pages are loaded, and scrolling near the end loads the next page.
 */
export function AssignmentHistory({ onRevoke }: AssignmentHistoryProps) {
    const { t } = useLanguage();
    const [search, setSearch] = useState("");
    const [status, setStatus] = useState<"all" | AssignmentStatus>("all");
    const q = useDebouncedValue(search.trim());
    const statusParam = status === "all" ? "" : status;
    const history = useAssignmentHistory({ status: statusParam, q });

    const columns = useMemo(
        () => [
            column.accessor("createdAt", {
                header: () => t("assignments.table.timestamp"),
                cell: (info) => {
                    const at = info.getValue();
                    return at ? new Date(at).toLocaleString() : "-";
                },
                meta: { headerClassName: "pl-6", cellClassName: "pl-6 font-medium text-foreground/80 py-4" },
            }),
            column.accessor("driverName", {
                header: () => t("assignments.table.driver"),
                cell: (info) => (
                    <div className="flex items-center gap-3">
                        <div className="h-7 w-7 rounded-full bg-primary/10 text-primary flex items-center justify-center text-xs font-bold ring-2 ring-background">
                            {(info.getValue() || "UK").substring(0, 2).toUpperCase()}
                        </div>
                        {info.getValue()}
                    </div>
                ),
            }),
            column.display({
                id: "vehicle",
                header: () => t("assignments.table.vehicle"),
                cell: ({ row }) => `${row.original.truckModel ?? ""} (${row.original.truckPlate ?? ""})`,
                meta: { cellClassName: "text-foreground/80" },
            }),
            column.accessor("adminLabel", {
                header: () => t("assignments.table.admin"),
                cell: (info) => info.getValue() ?? "-",
                meta: { cellClassName: "text-muted-foreground" },
            }),
            column.accessor("status", {
                header: () => t("assignments.table.status"),
                cell: (info) =>
                    info.getValue() === "active" ? (
                        <Badge variant="success">
                            {t("assignments.status.active")}
                        </Badge>
                    ) : (
                        <Badge variant="secondary">
                            {t("assignments.status.revoked")}
                        </Badge>
                    ),
            }),
            column.display({
                id: "actions",
                header: () => t("assignments.table.actions"),
                cell: ({ row }) => (
                    <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                            <Button aria-label={t("common.actionsForRow")} variant="ghost" size="icon" className="h-8 w-8">
                                <MoreHorizontal className="h-4 w-4" />
                            </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                            {row.original.status === "active" && (
                                <DropdownMenuItem
                                    className="text-destructive focus:text-destructive cursor-pointer"
                                    onClick={() => onRevoke(row.original)}
                                >
                                    {t("assignments.action.revoke")}
                                </DropdownMenuItem>
                            )}
                        </DropdownMenuContent>
                    </DropdownMenu>
                ),
                meta: { headerClassName: "text-right pr-6", cellClassName: "text-right pr-6" },
            }),
        ],
        [t, onRevoke]
    );

    return (
        <div className="rounded-xl border border-border/50 bg-card overflow-hidden">
            <div className="p-6 border-b border-border/50 flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <h2 className="text-lg font-semibold flex items-center gap-2">
                    <Settings className="h-4 w-4 text-primary" />
                    {t("assignments.history")}
                </h2>
                <div className="flex flex-col md:flex-row gap-3 w-full md:w-auto">
                    <div className="relative">
                        <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                        <Input
                            placeholder={t("assignments.filter.search")}
                            aria-label={t("assignments.filter.search")}
                            value={search}
                            maxLength={ASSIGNMENT_SEARCH_MAX}
                            onChange={(e) => setSearch(e.target.value)}
                            className="pl-9 w-full md:w-[250px] bg-background/50"
                        />
                    </div>
                    <Select value={status} onValueChange={(value) => setStatus(value as "all" | AssignmentStatus)}>
                        <SelectTrigger className="w-full md:w-[180px] bg-background/50" aria-label={t("assignments.filter.status")}>
                            <SelectValue placeholder={t("assignments.filter.status")} />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">{t("assignments.filter.all")}</SelectItem>
                            <SelectItem value="active">{t("assignments.status.active")}</SelectItem>
                            <SelectItem value="revoked">{t("assignments.status.revoked")}</SelectItem>
                        </SelectContent>
                    </Select>
                </div>
            </div>
            <VirtualTable
                data={history.rows}
                columns={columns}
                getRowId={(row) => row.id}
                aria-label={t("assignments.history")}
                estimateRowHeight={HISTORY_ROW_HEIGHT}
                infinite={history}
                loading={history.isPending}
                loadingState={t("assignments.table.loading")}
                emptyState={t("assignments.table.noHistory")}
                error={history.isError}
                errorState={t("masterData.list.loadError")}
                resetScrollKey={`${statusParam}|${q}`}
                headerRowClassName="bg-muted/30 hover:bg-muted/30 border-border/50"
                rowClassName="border-border/50 hover:bg-muted/30 transition-colors"
            />
            <div className="p-4 border-t border-border/50">
                <LoadMore
                    loaded={history.rows.length}
                    hasNextPage={history.hasNextPage}
                    isFetchingNextPage={history.isFetchingNextPage}
                    error={history.isFetchNextPageError}
                    onLoadMore={() => void history.fetchNextPage()}
                />
            </div>
        </div>
    );
}

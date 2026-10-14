"use client";

import { useMemo, useState } from "react";
import { useCustomerPages } from "@/features/customers/api/useCustomers";
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
import { Card, CardContent } from "@/components/ui/card";
import { Plus, Search, Loader2, MoreHorizontal } from "lucide-react";
import Link from "next/link";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useLanguage } from "@/context/language";
import { useDebouncedValue } from "@/hooks/useDebouncedValue";
import { LoadMore } from "@/components/load-more";

export default function CustomersList() {
    const { t } = useLanguage();
    const [searchQuery, setSearchQuery] = useState("");
    // `['customers', {q}]` (T25): searched on the server, keyset pages of 50 (no full scan).
    const q = useDebouncedValue(searchQuery.trim());
    const pages = useCustomerPages({ q });
    const loading = pages.isPending;
    const filtered = useMemo(() => pages.data?.pages.flatMap((p) => p.data ?? []) ?? [], [pages.data]);

    return (
        <div className="min-h-screen bg-background text-foreground p-6 space-y-6">
            <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">
                        {t("nav.customers")}
                    </h1>
                    <p className="text-muted-foreground mt-1">
                        {t("customers.subtitle")}
                    </p>
                </div>
                <Button asChild className="bg-blue-600 hover:bg-blue-700 text-white gap-2">
                    <Link href="/app/customers/new" prefetch={false}>
                        <Plus className="h-4 w-4" />
                        {t("customers.add")}
                    </Link>
                </Button>
            </div>

            <Card>
                <CardContent className="p-6">
                    <div className="flex items-center gap-4 mb-6">
                        <div className="relative flex-1 max-w-sm">
                            <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                            <Input
                                placeholder={t("customers.search")}
                                value={searchQuery}
                                onChange={(e) => setSearchQuery(e.target.value)}
                                className="pl-9"
                            />
                        </div>
                    </div>

                    {loading ? (
                        <div className="flex justify-center py-12">
                            <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                        </div>
                    ) : filtered.length === 0 ? (
                        <div className="text-center py-12 text-muted-foreground">
                            {pages.isError ? t("masterData.list.loadError") : t("customers.noData")}
                        </div>
                    ) : (<>
                        <Table>
                            <TableHeader>
                                <TableRow>
                                    <TableHead>{t("customers.table.code")}</TableHead>
                                    <TableHead>{t("customers.table.name")}</TableHead>
                                    <TableHead>{t("customers.table.driverIdTypes")}</TableHead>
                                    <TableHead className="w-[80px]">{t("customers.table.actions")}</TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {filtered.map((c) => (
                                    <TableRow key={c.id}>
                                        <TableCell className="font-mono font-medium">{c.code}</TableCell>
                                        <TableCell className="flex items-center gap-2">
                                            {c.logoUrl && (
                                                <img src={c.logoUrl} alt={c.name} className="h-6 w-6 rounded-full object-cover border" />
                                            )}
                                            <span>{c.name}</span>
                                        </TableCell>
                                        <TableCell className="text-sm text-muted-foreground">
                                            {(c.driverIdTypes || [])
                                                .map((dt) => dt.label || dt.key)
                                                .join(", ") || "-"}
                                        </TableCell>
                                        <TableCell>
                                            <DropdownMenu>
                                                <DropdownMenuTrigger asChild>
                                                    <Button variant="ghost" size="icon" className="h-8 w-8">
                                                        <MoreHorizontal className="h-4 w-4" />
                                                    </Button>
                                                </DropdownMenuTrigger>
                                                <DropdownMenuContent align="end">
                                                    <DropdownMenuItem asChild>
                                                        <Link href={`/app/customers/${c.id}`} prefetch={false}>
                                                            {t("customers.action.view")}
                                                        </Link>
                                                    </DropdownMenuItem>
                                                    <DropdownMenuItem asChild>
                                                        <Link href={`/app/customers/${c.id}/edit`} prefetch={false}>
                                                            {t("customers.action.edit")}
                                                        </Link>
                                                    </DropdownMenuItem>
                                                </DropdownMenuContent>
                                            </DropdownMenu>
                                        </TableCell>
                                    </TableRow>
                                ))}
                            </TableBody>
                        </Table>
                        <div className="mt-4">
                            <LoadMore
                                loaded={filtered.length}
                                hasNextPage={pages.hasNextPage}
                                isFetchingNextPage={pages.isFetchingNextPage}
                                onLoadMore={() => void pages.fetchNextPage()}
                            />
                        </div>
                    </>)}
                </CardContent>
            </Card>
        </div>
    );
}

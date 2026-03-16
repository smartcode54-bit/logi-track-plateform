"use client";

import { useState, useEffect, useMemo } from "react";
import { getCustomers, CustomerData } from "./actions.client";
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
import { Plus, Search, Loader2, MoreHorizontal, Building2 } from "lucide-react";
import Link from "next/link";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useLanguage } from "@/context/language";

export default function CustomersPage() {
    const { t } = useLanguage();
    const [customers, setCustomers] = useState<CustomerData[]>([]);
    const [loading, setLoading] = useState(true);
    const [searchQuery, setSearchQuery] = useState("");

    useEffect(() => {
        const fetchData = async () => {
            setLoading(true);
            try {
                const data = await getCustomers();
                setCustomers(data);
            } catch (error) {
                console.error("Failed to load customers", error);
            } finally {
                setLoading(false);
            }
        };
        fetchData();
    }, []);

    const filtered = useMemo(() => {
        if (!searchQuery.trim()) return customers;
        const q = searchQuery.toLowerCase();
        return customers.filter(
            (c) =>
                c.code?.toLowerCase().includes(q) ||
                c.name?.toLowerCase().includes(q)
        );
    }, [customers, searchQuery]);

    return (
        <div className="min-h-screen bg-background text-foreground p-6 space-y-6">
            <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">
                        {t("customers.title")}
                    </h1>
                    <p className="text-muted-foreground mt-1">
                        {t("customers.subtitle")}
                    </p>
                </div>
                <Button asChild className="bg-blue-600 hover:bg-blue-700 text-white gap-2">
                    <Link href="/admin/customers/new" prefetch={false}>
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
                            {t("customers.noData")}
                        </div>
                    ) : (
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
                                        <TableCell>{c.name}</TableCell>
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
                                                        <Link href={`/admin/customers/${c.id}`} prefetch={false}>
                                                            {t("customers.action.view")}
                                                        </Link>
                                                    </DropdownMenuItem>
                                                    <DropdownMenuItem asChild>
                                                        <Link href={`/admin/customers/${c.id}/edit`} prefetch={false}>
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
                    )}
                </CardContent>
            </Card>
        </div>
    );
}

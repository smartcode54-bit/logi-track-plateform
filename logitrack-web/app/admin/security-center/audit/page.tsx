"use client";

import { useState } from "react";
import { format } from "date-fns";
import { useLanguage } from "@/context/language";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import { Shield, TrendingUp, Users, ChevronDown, Search, RefreshCw, FileDown } from "lucide-react";
import { Input } from "@/components/ui/input";

// Placeholder audit data (replace with real data from Firestore/backend)
const PLACEHOLDER_AUDIT_LOGS = [
    { id: 1, timestamp: new Date("2023-10-24T14:32:11"), performedBy: "Marcus Chen", roleKey: "securityCenter.audit.roleLeadAdmin", actionKey: "securityCenter.audit.roleUpdated", targetRoleKey: "securityCenter.audit.roleRegionalManager", changeDetails: "VIEW_ONLY → FULL_ACCESS" },
    { id: 2, timestamp: new Date("2023-10-24T11:05:45"), performedBy: "Sarah Williams", roleKey: "securityCenter.audit.roleSecurityOps", actionKey: "securityCenter.audit.permissionRevoked", targetRoleKey: "securityCenter.audit.roleContractor", changeDetails: "DB_EXPORT → NONE" },
    { id: 3, timestamp: new Date("2023-10-23T09:12:00"), performedBy: "Alex Johnson", roleKey: "securityCenter.audit.roleSuperAdmin", actionKey: "securityCenter.audit.policyUpdated", targetRoleKey: "securityCenter.audit.roleAllUsers", changeDetails: "PWD_EXP_90D → PWD_EXP_30D" },
    { id: 4, timestamp: new Date("2023-10-23T08:45:33"), performedBy: "Michael Scott", roleKey: "securityCenter.audit.roleSystemAdmin", actionKey: "securityCenter.audit.userVerified", targetRoleKey: "securityCenter.overview.roleFleetDriver", changeDetails: "UNVERIFIED → VERIFIED" },
];

export default function SecurityAuditPage() {
    const { t } = useLanguage();
    const [dateRange, setDateRange] = useState("last7");
    const [adminFilter, setAdminFilter] = useState("all");
    const [actionFilter, setActionFilter] = useState("all");
    const [searchQuery, setSearchQuery] = useState("");
    const [currentPage, setCurrentPage] = useState(1);

    const logs = PLACEHOLDER_AUDIT_LOGS;
    const totalResults = 1284; // Placeholder
    const pageSize = 10;

    return (
        <div className="p-6 space-y-6">
            <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("securityCenter.audit.title")}</h1>
                    <p className="text-muted-foreground mt-1">
                        {t("securityCenter.audit.subtitle")}
                    </p>
                </div>
                <div className="flex gap-2">
                    <Button variant="outline" size="sm">
                        <FileDown className="mr-2 h-4 w-4" />
                        {t("securityCenter.audit.exportCsv")}
                    </Button>
                    <Button variant="outline" size="sm">
                        <RefreshCw className="mr-2 h-4 w-4" />
                        {t("securityCenter.audit.refreshLogs")}
                    </Button>
                </div>
            </div>

            {/* Key metrics */}
            <div className="grid gap-4 md:grid-cols-3">
                <Card>
                    <CardContent className="pt-6">
                        <div className="flex items-center justify-between">
                            <div>
                                <p className="text-sm font-medium text-muted-foreground">{t("securityCenter.audit.totalEvents")}</p>
                                <p className="text-2xl font-bold">1,284</p>
                                <p className="text-xs text-emerald-600">{t("securityCenter.audit.fromLastMonth")}</p>
                            </div>
                            <TrendingUp className="h-8 w-8 text-muted-foreground" />
                        </div>
                    </CardContent>
                </Card>
                <Card>
                    <CardContent className="pt-6">
                        <div className="flex items-center justify-between">
                            <div>
                                <p className="text-sm font-medium text-muted-foreground">{t("securityCenter.audit.unauthorizedAttempts")}</p>
                                <p className="text-2xl font-bold">0</p>
                                <p className="text-xs text-emerald-600">{t("securityCenter.audit.systemSecured")}</p>
                            </div>
                            <Shield className="h-8 w-8 text-muted-foreground" />
                        </div>
                    </CardContent>
                </Card>
                <Card>
                    <CardContent className="pt-6">
                        <div className="flex items-center justify-between">
                            <div>
                                <p className="text-sm font-medium text-muted-foreground">{t("securityCenter.audit.roleChanges24h")}</p>
                                <p className="text-2xl font-bold">12</p>
                                <p className="text-xs text-amber-600">{t("securityCenter.audit.activeAudits")}</p>
                            </div>
                            <Users className="h-8 w-8 text-muted-foreground" />
                        </div>
                    </CardContent>
                </Card>
            </div>

            {/* Filters */}
            <div className="space-y-4 rounded-lg border border-border/50 bg-card/50 p-4">
                <div className="flex items-center gap-2 text-sm font-medium text-muted-foreground">
                    <ChevronDown className="h-4 w-4" />
                    {t("securityCenter.audit.filters")}
                </div>
                <div className="flex flex-wrap gap-4">
                    <Select value={dateRange} onValueChange={setDateRange}>
                        <SelectTrigger className="w-[180px]">
                            <SelectValue placeholder={t("securityCenter.audit.dateRange")} />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="last7">{t("securityCenter.audit.last7Days")}</SelectItem>
                            <SelectItem value="last30">{t("securityCenter.audit.last30Days")}</SelectItem>
                            <SelectItem value="last90">{t("securityCenter.audit.last90Days")}</SelectItem>
                        </SelectContent>
                    </Select>
                    <Select value={adminFilter} onValueChange={setAdminFilter}>
                        <SelectTrigger className="w-[180px]">
                            <SelectValue placeholder={t("securityCenter.audit.adminUser")} />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">{t("securityCenter.audit.allAdmins")}</SelectItem>
                        </SelectContent>
                    </Select>
                    <Select value={actionFilter} onValueChange={setActionFilter}>
                        <SelectTrigger className="w-[180px]">
                            <SelectValue placeholder={t("securityCenter.audit.actionType")} />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">{t("securityCenter.audit.allActions")}</SelectItem>
                            <SelectItem value="role_updated">{t("securityCenter.audit.roleUpdated")}</SelectItem>
                            <SelectItem value="permission_revoked">{t("securityCenter.audit.permissionRevoked")}</SelectItem>
                            <SelectItem value="policy_updated">{t("securityCenter.audit.policyUpdated")}</SelectItem>
                            <SelectItem value="user_verified">{t("securityCenter.audit.userVerified")}</SelectItem>
                        </SelectContent>
                    </Select>
                    <div className="relative flex-1 min-w-[200px]">
                        <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                        <Input
                            placeholder={t("securityCenter.audit.searchLogs")}
                            className="pl-10"
                            value={searchQuery}
                            onChange={(e) => setSearchQuery(e.target.value)}
                        />
                    </div>
                    <Button variant="ghost" size="sm">{t("securityCenter.audit.clearFilters")}</Button>
                </div>
            </div>

            {/* Audit log table */}
            <div className="rounded-lg border border-border bg-card overflow-hidden">
                <Table>
                    <TableHeader>
                        <TableRow className="bg-muted/50">
                            <TableHead className="uppercase text-xs font-semibold">{t("securityCenter.audit.timestamp")}</TableHead>
                            <TableHead className="uppercase text-xs font-semibold">{t("securityCenter.audit.performedBy")}</TableHead>
                            <TableHead className="uppercase text-xs font-semibold">{t("securityCenter.audit.action")}</TableHead>
                            <TableHead className="uppercase text-xs font-semibold">{t("securityCenter.audit.targetRole")}</TableHead>
                            <TableHead className="uppercase text-xs font-semibold">{t("securityCenter.audit.changeDetails")}</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {logs.map((log) => (
                            <TableRow key={log.id}>
                                <TableCell className="text-sm text-muted-foreground">
                                    {format(log.timestamp, "MMM d, yyyy, HH:mm:ss a")}
                                </TableCell>
                                        <TableCell>
                                    <span className="font-medium">{log.performedBy}</span>
                                    <span className="text-muted-foreground text-xs ml-1">({t(log.roleKey)})</span>
                                </TableCell>
                                <TableCell>{t(log.actionKey)}</TableCell>
                                <TableCell>{t(log.targetRoleKey)}</TableCell>
                                <TableCell className="text-sm">{log.changeDetails}</TableCell>
                            </TableRow>
                        ))}
                    </TableBody>
                </Table>
                <div className="flex items-center justify-between px-4 py-4 border-t bg-muted/20 text-sm text-muted-foreground">
                    <span>{t("securityCenter.audit.showingResults").replace("{count}", String(logs.length)).replace("{total}", String(totalResults))}</span>
                    <div className="flex gap-2">
                        <Button variant="outline" size="sm" disabled>{t("securityCenter.audit.previous")}</Button>
                        <Button variant="outline" size="sm">1</Button>
                        <Button variant="outline" size="sm">2</Button>
                        <Button variant="outline" size="sm">3</Button>
                        <Button variant="outline" size="sm">{t("securityCenter.audit.next")}</Button>
                    </div>
                </div>
            </div>
        </div>
    );
}

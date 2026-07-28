"use client";

import { useState, useEffect } from "react";
import {
    Shield,
    Users,
    Headphones,
    Truck,
    Building2,
    Info,
    Loader2,
} from "lucide-react";
import { doc, getDoc, writeBatch } from "firebase/firestore";
import { httpsCallable } from "firebase/functions";
import { db, functions } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { fetchUserRoleCounts } from "@/lib/fetchSecurityOverviewStats";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { cn } from "@/lib/utils";
import { toast } from "sonner";
import { useLanguage } from "@/context/language";

const ROLES = ["ADMIN", "MANAGER", "OPERATION_STAFF", "OPERATOR", "DRIVER", "PARTNER", "CUSTOMER"] as const;
/** Map Matrix role key to Firestore doc ID (customClaims.role) */
const ROLE_TO_DOC_ID: Record<(typeof ROLES)[number], string> = {
    ADMIN: "admin",
    MANAGER: "manager",
    OPERATION_STAFF: "operation_staff",
    OPERATOR: "operator",
    DRIVER: "driver",
    PARTNER: "partner",
    CUSTOMER: "customer",
};
type RoleKey = (typeof ROLES)[number];

const ROLE_CARD_KEYS = [
    { key: "admins" as const, labelKey: "securityCenter.roles.admins", icon: Shield },
    { key: "managers" as const, labelKey: "securityCenter.roles.managers", icon: Users },
    { key: "operationStaff" as const, labelKey: "securityCenter.roles.operationStaff", icon: Headphones },
    { key: "operators" as const, labelKey: "securityCenter.roles.operators", icon: Headphones },
    { key: "drivers" as const, labelKey: "securityCenter.roles.drivers", icon: Truck },
    { key: "subcontractors" as const, labelKey: "securityCenter.roles.subcontractors", icon: Building2 },
] as const;

type RoleCountKey = (typeof ROLE_CARD_KEYS)[number]["key"];

type CapabilityPermission = Record<RoleKey, boolean>;

const FLEET_CAPABILITIES: { id: string; title: string; description: string; defaultPermissions: CapabilityPermission }[] = [
    {
        id: "create_truck",
        title: "Create & Register Truck",
        description: "Allows adding new vehicles to the active fleet database.",
        defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: false, OPERATOR: false, DRIVER: false, PARTNER: false, CUSTOMER: false },
    },
    {
        id: "edit_telemetry",
        title: "Edit Vehicle Telemetry",
        description: "Modify IoT sensor data and tracking frequency.",
        defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: true, OPERATOR: true, DRIVER: false, PARTNER: false, CUSTOMER: false },
    },
    {
        id: "view_live_map",
        title: "View Live Map",
        description: "Real-time GPS visibility for all fleet units.",
        defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: true, OPERATOR: true, DRIVER: true, PARTNER: true, CUSTOMER: true },
    },
    {
        id: "assign_driver",
        title: "Assign Driver to Task",
        description: "Dispatching capabilities for route management.",
        defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: true, OPERATOR: true, DRIVER: false, PARTNER: false, CUSTOMER: false },
    },
    {
        id: "maintenance_logs",
        title: "Generate Maintenance Logs",
        description: "Create compliance reports for vehicle inspections.",
        defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: true, OPERATOR: true, DRIVER: true, PARTNER: false, CUSTOMER: false },
    },
    {
        id: "delete_records",
        title: "Delete Historical Records",
        description: "Permanent removal of logs (Admin only).",
        defaultPermissions: { ADMIN: true, MANAGER: false, OPERATION_STAFF: false, OPERATOR: false, DRIVER: false, PARTNER: false, CUSTOMER: false },
    },
];

// Operations — สิทธิ์งานปฏิบัติการ (Mobile App / Driver)
const OPERATIONS_CAPABILITIES: { id: string; title: string; description: string; defaultPermissions: CapabilityPermission }[] = [
    {
        id: "view_assigned_tasks",
        title: "View Assigned Tasks",
        description: "ดูงานที่มอบหมายให้ตัวเอง",
        defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: true, OPERATOR: true, DRIVER: true, PARTNER: true, CUSTOMER: true },
    },
    {
        id: "checkin_task",
        title: "Check-in Task",
        description: "บันทึก Check-in พร้อมรูป",
        defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: true, OPERATOR: true, DRIVER: true, PARTNER: false, CUSTOMER: false },
    },
    {
        id: "create_trip_record",
        title: "Create Trip Record",
        description: "เริ่มเที่ยว Loading",
        defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: true, OPERATOR: true, DRIVER: true, PARTNER: false, CUSTOMER: false },
    },
    {
        id: "update_trip_record",
        title: "Update Trip Record (Own)",
        description: "อัปเดตเที่ยวของตนเอง",
        defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: true, OPERATOR: true, DRIVER: true, PARTNER: false, CUSTOMER: false },
    },
    {
        id: "submit_delivery",
        title: "Submit Delivery",
        description: "ส่งงานและอัปโหลดรูป",
        defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: true, OPERATOR: true, DRIVER: true, PARTNER: false, CUSTOMER: false },
    },
    {
        id: "send_chat",
        title: "Send Chat Message",
        description: "แชทกับ Admin",
        defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: true, OPERATOR: true, DRIVER: true, PARTNER: false, CUSTOMER: false },
    },
    {
        id: "read_broadcasts",
        title: "Read Broadcasts",
        description: "อ่านประกาศจาก Admin",
        defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: true, OPERATOR: true, DRIVER: true, PARTNER: true, CUSTOMER: true },
    },
    {
        id: "report_incident",
        title: "Report Incident",
        description: "รายงานเหตุการณ์",
        defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: true, OPERATOR: true, DRIVER: true, PARTNER: false, CUSTOMER: false },
    },
    {
        id: "submit_vehicle_expense",
        title: "Submit Vehicle Expense",
        description: "บันทึกค่าน้ำมัน / ค่าใช้จ่าย",
        defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: true, OPERATOR: true, DRIVER: true, PARTNER: false, CUSTOMER: false },
    },
];

// User Management permissions
const USER_MANAGEMENT_CAPABILITIES: { id: string; title: string; description: string; defaultPermissions: CapabilityPermission }[] = [
    { id: "users_view", title: "View Users", description: "View user list and profiles", defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: false, OPERATOR: false, DRIVER: false, PARTNER: false, CUSTOMER: false } },
    { id: "users_create", title: "Create User", description: "Create new user accounts", defaultPermissions: { ADMIN: true, MANAGER: false, OPERATION_STAFF: false, OPERATOR: false, DRIVER: false, PARTNER: false, CUSTOMER: false } },
    { id: "users_edit", title: "Edit User", description: "Edit user profile and settings", defaultPermissions: { ADMIN: true, MANAGER: false, OPERATION_STAFF: false, OPERATOR: false, DRIVER: false, PARTNER: false, CUSTOMER: false } },
    { id: "users_assign_role", title: "Assign Role", description: "Assign or change user role", defaultPermissions: { ADMIN: true, MANAGER: false, OPERATION_STAFF: false, OPERATOR: false, DRIVER: false, PARTNER: false, CUSTOMER: false } },
];

// Communication permissions
const COMMUNICATION_CAPABILITIES: { id: string; title: string; description: string; defaultPermissions: CapabilityPermission }[] = [
    { id: "chat_view", title: "View Chats", description: "View conversations with drivers", defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: true, OPERATOR: true, DRIVER: true, PARTNER: true, CUSTOMER: true } },
    { id: "chat_send", title: "Send Messages", description: "Send chat messages", defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: true, OPERATOR: true, DRIVER: true, PARTNER: true, CUSTOMER: true } },
    { id: "broadcasts_send", title: "Send Broadcasts", description: "Send announcements to drivers", defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: false, OPERATOR: false, DRIVER: false, PARTNER: false, CUSTOMER: false } },
    { id: "broadcasts_view", title: "View Broadcast History", description: "View sent broadcasts", defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: true, OPERATOR: true, DRIVER: true, PARTNER: true, CUSTOMER: true } },
];

// Financials permissions — Operation Staff has accounting; Operator does not
const FINANCIALS_CAPABILITIES: { id: string; title: string; description: string; defaultPermissions: CapabilityPermission }[] = [
    { id: "accounting_view_fuel", title: "View Fuel Accounting", description: "View fuel expense records", defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: true, OPERATOR: false, DRIVER: false, PARTNER: false, CUSTOMER: false } },
    { id: "accounting_edit_fuel", title: "Edit Fuel Records", description: "Edit fuel accounting", defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: false, OPERATOR: false, DRIVER: false, PARTNER: false, CUSTOMER: false } },
    { id: "accounting_view_other", title: "View Other Expenses", description: "View other expense records", defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: true, OPERATOR: false, DRIVER: false, PARTNER: false, CUSTOMER: false } },
    { id: "accounting_edit_other", title: "Edit Other Expenses", description: "Edit other accounting", defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: false, OPERATOR: false, DRIVER: false, PARTNER: false, CUSTOMER: false } },
    { id: "accounting_audit_expense", title: "Audit Vehicle Expense", description: "Review and approve/reject vehicle expenses", defaultPermissions: { ADMIN: true, MANAGER: true, OPERATION_STAFF: true, OPERATOR: false, DRIVER: false, PARTNER: false, CUSTOMER: false } },
];

// Security & Access Control permissions
const SECURITY_CAPABILITIES: { id: string; title: string; description: string; defaultPermissions: CapabilityPermission }[] = [
    { id: "security_view_overview", title: "View Security Overview", description: "View security dashboard and threat summary", defaultPermissions: { ADMIN: true, MANAGER: false, OPERATION_STAFF: false, OPERATOR: false, DRIVER: false, PARTNER: false, CUSTOMER: false } },
    { id: "security_manage_users", title: "Manage Users", description: "Create, edit and manage web admin accounts", defaultPermissions: { ADMIN: true, MANAGER: false, OPERATION_STAFF: false, OPERATOR: false, DRIVER: false, PARTNER: false, CUSTOMER: false } },
    { id: "security_manage_roles", title: "Manage Roles & Permissions", description: "Edit the Role & Permission Matrix", defaultPermissions: { ADMIN: true, MANAGER: false, OPERATION_STAFF: false, OPERATOR: false, DRIVER: false, PARTNER: false, CUSTOMER: false } },
    { id: "security_view_audit", title: "View Audit Logs", description: "View security audit events and permission changes", defaultPermissions: { ADMIN: true, MANAGER: false, OPERATION_STAFF: false, OPERATOR: false, DRIVER: false, PARTNER: false, CUSTOMER: false } },
    { id: "security_manage_api_keys", title: "Manage API Keys", description: "Create and revoke API keys for integrations", defaultPermissions: { ADMIN: true, MANAGER: false, OPERATION_STAFF: false, OPERATOR: false, DRIVER: false, PARTNER: false, CUSTOMER: false } },
    { id: "security_view_status", title: "View System Status", description: "View real-time service health and uptime", defaultPermissions: { ADMIN: true, MANAGER: false, OPERATION_STAFF: false, OPERATOR: false, DRIVER: false, PARTNER: false, CUSTOMER: false } },
    { id: "security_view_mobile_clients", title: "View Mobile Clients", description: "Driver app versions and last-seen per device (Security Center)", defaultPermissions: { ADMIN: true, MANAGER: false, OPERATION_STAFF: false, OPERATOR: false, DRIVER: false, PARTNER: true, CUSTOMER: false } },
    { id: "security_manage_mobile_release", title: "Manage Mobile Release", description: "Set the minimum driver-app version — can lock the whole fleet out of older builds", defaultPermissions: { ADMIN: true, MANAGER: false, OPERATION_STAFF: false, OPERATOR: false, DRIVER: false, PARTNER: false, CUSTOMER: false } },
];

function getDefaultPermissions(): Record<string, CapabilityPermission> {
    const allCaps = [
        ...FLEET_CAPABILITIES,
        ...OPERATIONS_CAPABILITIES,
        ...USER_MANAGEMENT_CAPABILITIES,
        ...COMMUNICATION_CAPABILITIES,
        ...FINANCIALS_CAPABILITIES,
        ...SECURITY_CAPABILITIES,
    ];
    const initial: Record<string, CapabilityPermission> = {};
    allCaps.forEach((c) => {
        initial[c.id] = { ...c.defaultPermissions };
    });
    return initial;
}

function MatrixTable({
    capabilities,
    permissions,
    onToggle,
    searchQuery,
    capabilitiesLabel = "Capabilities",
    roleLabels = {},
    t,
}: {
    capabilities: { id: string; title: string; description: string; defaultPermissions: CapabilityPermission }[];
    permissions: Record<string, CapabilityPermission>;
    onToggle: (capId: string, role: RoleKey, checked: boolean) => void;
    searchQuery: string;
    capabilitiesLabel?: string;
    roleLabels?: Record<string, string>;
    t: (key: string) => string;
}) {
    const getTitle = (c: { id: string; title: string }) =>
        t(`securityCenter.capabilities.${c.id}.title`) || c.title;
    const getDesc = (c: { id: string; description: string }) =>
        t(`securityCenter.capabilities.${c.id}.description`) || c.description;
    const filtered = capabilities.filter(
        (c) =>
            !searchQuery ||
            getTitle(c).toLowerCase().includes(searchQuery.toLowerCase()) ||
            getDesc(c).toLowerCase().includes(searchQuery.toLowerCase())
    );
    return (
        <Card>
            <div className="overflow-x-auto">
                <Table>
                    <TableHeader>
                        <TableRow className="bg-muted/50 hover:bg-muted/50">
                            <TableHead className="w-[320px] font-semibold">{capabilitiesLabel}</TableHead>
                            {ROLES.map((r) => (
                                <TableHead key={r} className="text-center font-semibold w-24">
                                    {roleLabels[r] ?? r}
                                </TableHead>
                            ))}
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {filtered.map((cap) => (
                            <TableRow key={cap.id}>
                                <TableCell className="align-top">
                                    <div className="py-2">
                                        <p className="font-medium">{getTitle(cap)}</p>
                                        <p className="text-xs text-muted-foreground mt-0.5">
                                            {getDesc(cap)}
                                        </p>
                                    </div>
                                </TableCell>
                                {ROLES.map((role) => (
                                    <TableCell key={role} className="text-center align-top py-4">
                                        <div className="flex justify-center">
                                            <Checkbox
                                                checked={permissions[cap.id]?.[role] ?? false}
                                                onCheckedChange={(checked) =>
                                                    onToggle(cap.id, role, !!checked)
                                                }
                                                className="h-5 w-5 data-[state=checked]:bg-primary data-[state=checked]:border-primary"
                                            />
                                        </div>
                                    </TableCell>
                                ))}
                            </TableRow>
                        ))}
                    </TableBody>
                </Table>
            </div>
        </Card>
    );
}

export default function RolePermissionMatrixPage() {
    const { t } = useLanguage();
    const roleLabels: Record<string, string> = {
        ADMIN: t("securityCenter.roles.roleAdmin"),
        MANAGER: t("securityCenter.roles.roleManager"),
        OPERATION_STAFF: t("securityCenter.roles.roleOperationStaff"),
        OPERATOR: t("securityCenter.roles.roleOperator"),
        DRIVER: t("securityCenter.roles.roleDriver"),
        PARTNER: t("securityCenter.roles.rolePartner"),
        CUSTOMER: t("securityCenter.roles.roleCustomer"),
    };
    const allCapabilities = [
        ...FLEET_CAPABILITIES,
        ...OPERATIONS_CAPABILITIES,
        ...USER_MANAGEMENT_CAPABILITIES,
        ...COMMUNICATION_CAPABILITIES,
        ...FINANCIALS_CAPABILITIES,
        ...SECURITY_CAPABILITIES,
    ];
    const [permissions, setPermissions] = useState<Record<string, CapabilityPermission>>(getDefaultPermissions);
    const [hasChanges, setHasChanges] = useState(false);
    const [searchQuery, setSearchQuery] = useState("");
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [roleCounts, setRoleCounts] = useState<Record<RoleCountKey, number>>({
        admins: 0,
        managers: 0,
        operationStaff: 0,
        operators: 0,
        drivers: 0,
        subcontractors: 0,
    });

    useEffect(() => {
        async function fetchCounts() {
            try {
                const c = await fetchUserRoleCounts();
                setRoleCounts({
                    admins: c.admins,
                    managers: c.managers,
                    operationStaff: c.operationStaff,
                    operators: c.operators,
                    drivers: c.drivers,
                    subcontractors: c.subcontractors,
                });
            } catch (e) {
                console.error("[RoleMatrix] Failed to fetch role counts:", e);
            }
        }
        fetchCounts();
    }, []);

    useEffect(() => {
        async function load() {
            try {
                const roleIds = Object.values(ROLE_TO_DOC_ID);
                const loaded: Record<string, Record<string, boolean>> = {};
                roleIds.forEach((id) => {
                    loaded[id] = {};
                });

                for (const roleId of roleIds) {
                    const ref = doc(db, COLLECTIONS.PERMISSIONS_CONFIG, roleId);
                    const snap = await getDoc(ref);
                    if (snap.exists() && snap.data()?.capabilities) {
                        loaded[roleId] = snap.data().capabilities as Record<string, boolean>;
                    }
                }

                const merged: Record<string, CapabilityPermission> = {};
                allCapabilities.forEach((cap) => {
                    merged[cap.id] = { ...cap.defaultPermissions };
                    ROLES.forEach((roleKey) => {
                        const docId = ROLE_TO_DOC_ID[roleKey];
                        if (loaded[docId] && cap.id in loaded[docId]) {
                            merged[cap.id][roleKey] = loaded[docId][cap.id];
                        }
                    });
                });
                setPermissions(merged);
            } catch (e) {
                console.error("[RoleMatrix] Load error:", e);
                toast.error(t("securityCenter.roles.failedLoad"));
            } finally {
                setLoading(false);
            }
        }
        load();
    }, []);

    const handleToggle = (capId: string, role: RoleKey, checked: boolean) => {
        setPermissions((prev) => ({
            ...prev,
            [capId]: {
                ...prev[capId],
                [role]: checked,
            },
        }));
        setHasChanges(true);
    };

    const handleSave = async () => {
        setSaving(true);
        try {
            const batch = writeBatch(db);
            ROLES.forEach((roleKey) => {
                const docId = ROLE_TO_DOC_ID[roleKey];
                const capabilities: Record<string, boolean> = {};
                allCapabilities.forEach((cap) => {
                    capabilities[cap.id] = permissions[cap.id]?.[roleKey] ?? false;
                });
                const ref = doc(db, COLLECTIONS.PERMISSIONS_CONFIG, docId);
                batch.set(ref, { capabilities, updatedAt: new Date().toISOString() }, { merge: true });
            });
            await batch.commit();
            setHasChanges(false);
            try {
                const logSecurityEvent = httpsCallable<
                    { type: string; summary?: string; details?: Record<string, unknown> },
                    { ok: boolean }
                >(functions, "logSecurityEvent");
                await logSecurityEvent({
                    type: "role_matrix_saved",
                    summary: "Role & permission matrix saved",
                    details: { roleDocsUpdated: ROLES.length },
                });
            } catch (logErr) {
                console.warn("[RoleMatrix] logSecurityEvent:", logErr);
            }
            toast.success(t("securityCenter.roles.savedSuccess"));
        } catch (e) {
            console.error("[RoleMatrix] Save error:", e);
            toast.error(t("securityCenter.roles.failedSave"));
        } finally {
            setSaving(false);
        }
    };

    const handleDiscard = () => {
        setPermissions(getDefaultPermissions());
        setHasChanges(false);
        toast.info(t("securityCenter.roles.changesDiscarded"));
    };

    if (loading) {
        return (
            <div className="p-6 flex items-center justify-center min-h-[400px]">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
            </div>
        );
    }

    return (
        <div className="p-6 space-y-6">
            {/* Header */}
            <div>
                <h1 className="text-3xl font-bold tracking-tight">{t("securityCenter.roles.title")}</h1>
                <p className="text-muted-foreground mt-1">
                    {t("securityCenter.roles.subtitle")}
                </p>
            </div>

            {/* Role Summary Cards */}
            <div className="grid gap-4 md:grid-cols-3 lg:grid-cols-6">
                {ROLE_CARD_KEYS.map((r) => {
                    const count = roleCounts[r.key];
                    const maxCount = Math.max(...Object.values(roleCounts), 1);
                    return (
                        <Card key={r.key} className="overflow-hidden">
                            <CardContent className="pt-6 pb-2">
                                <div className="flex items-center justify-between">
                                    <span className="text-2xl font-bold">{count}</span>
                                    <r.icon className="h-6 w-6 text-muted-foreground" />
                                </div>
                                <p className="text-sm font-medium text-muted-foreground mt-1">{t(r.labelKey)}</p>
                                <div className="mt-3 h-1 rounded-full bg-primary/20 overflow-hidden">
                                    <div
                                        className="h-full bg-primary rounded-full transition-all"
                                        style={{ width: `${Math.min(100, (count / maxCount) * 100)}%` }}
                                    />
                                </div>
                            </CardContent>
                        </Card>
                    );
                })}
            </div>

            {/* Search */}
            <div className="relative max-w-sm">
                <input
                    type="text"
                    placeholder={t("securityCenter.roles.searchPermissions")}
                    className="w-full h-9 rounded-md border border-input bg-muted/50 pl-9 pr-4 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                    value={searchQuery}
                    onChange={(e) => setSearchQuery(e.target.value)}
                />
                <svg
                    className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground"
                    xmlns="http://www.w3.org/2000/svg"
                    fill="none"
                    viewBox="0 0 24 24"
                    stroke="currentColor"
                >
                    <path
                        strokeLinecap="round"
                        strokeLinejoin="round"
                        strokeWidth={2}
                        d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"
                    />
                </svg>
            </div>

            {/* Tabs & Matrix */}
            <Tabs defaultValue="operations" className="space-y-4">
                <TabsList className="bg-transparent p-0 gap-0 border-b border-border/50 rounded-none">
                    <TabsTrigger
                        value="operations"
                        className={cn(
                            "rounded-none border-b-2 border-transparent bg-transparent px-4 pb-3 pt-2 font-semibold text-muted-foreground",
                            "data-[state=active]:border-primary data-[state=active]:text-primary data-[state=active]:shadow-none"
                        )}
                    >
                        {t("securityCenter.roles.fleetOperations")}
                    </TabsTrigger>
                    <TabsTrigger
                        value="fleet"
                        className={cn(
                            "rounded-none border-b-2 border-transparent bg-transparent px-4 pb-3 pt-2 font-semibold text-muted-foreground",
                            "data-[state=active]:border-primary data-[state=active]:text-primary data-[state=active]:shadow-none"
                        )}
                    >
                        {t("securityCenter.roles.fleetManagement")}
                    </TabsTrigger>
                    <TabsTrigger
                        value="users"
                        className={cn(
                            "rounded-none border-b-2 border-transparent bg-transparent px-4 pb-3 pt-2 font-semibold text-muted-foreground",
                            "data-[state=active]:border-primary data-[state=active]:text-primary data-[state=active]:shadow-none"
                        )}
                    >
                        {t("securityCenter.roles.userManagement")}
                    </TabsTrigger>
                    <TabsTrigger
                        value="communication"
                        className={cn(
                            "rounded-none border-b-2 border-transparent bg-transparent px-4 pb-3 pt-2 font-semibold text-muted-foreground",
                            "data-[state=active]:border-primary data-[state=active]:text-primary data-[state=active]:shadow-none"
                        )}
                    >
                        {t("securityCenter.roles.communication")}
                    </TabsTrigger>
                    <TabsTrigger
                        value="financials"
                        className={cn(
                            "rounded-none border-b-2 border-transparent bg-transparent px-4 pb-3 pt-2 font-semibold text-muted-foreground",
                            "data-[state=active]:border-primary data-[state=active]:text-primary data-[state=active]:shadow-none"
                        )}
                    >
                        {t("securityCenter.roles.financials")}
                    </TabsTrigger>
                    <TabsTrigger
                        value="security"
                        className={cn(
                            "rounded-none border-b-2 border-transparent bg-transparent px-4 pb-3 pt-2 font-semibold text-muted-foreground",
                            "data-[state=active]:border-primary data-[state=active]:text-primary data-[state=active]:shadow-none"
                        )}
                    >
                        {t("securityCenter.roles.security")}
                    </TabsTrigger>
                </TabsList>

                <TabsContent value="operations" className="mt-0">
                    <MatrixTable
                        capabilities={OPERATIONS_CAPABILITIES}
                        permissions={permissions}
                        onToggle={handleToggle}
                        searchQuery={searchQuery}
                        capabilitiesLabel={t("securityCenter.roles.capabilities")}
                        roleLabels={roleLabels}
                        t={t}
                    />
                </TabsContent>

                <TabsContent value="fleet" className="mt-0">
                    <MatrixTable
                        capabilities={FLEET_CAPABILITIES}
                        permissions={permissions}
                        onToggle={handleToggle}
                        searchQuery={searchQuery}
                        capabilitiesLabel={t("securityCenter.roles.capabilities")}
                        roleLabels={roleLabels}
                        t={t}
                    />
                </TabsContent>

                <TabsContent value="users" className="mt-0">
                    <MatrixTable
                        capabilities={USER_MANAGEMENT_CAPABILITIES}
                        permissions={permissions}
                        onToggle={handleToggle}
                        searchQuery={searchQuery}
                        capabilitiesLabel={t("securityCenter.roles.capabilities")}
                        roleLabels={roleLabels}
                        t={t}
                    />
                </TabsContent>

                <TabsContent value="communication" className="mt-0">
                    <MatrixTable
                        capabilities={COMMUNICATION_CAPABILITIES}
                        permissions={permissions}
                        onToggle={handleToggle}
                        searchQuery={searchQuery}
                        capabilitiesLabel={t("securityCenter.roles.capabilities")}
                        roleLabels={roleLabels}
                        t={t}
                    />
                </TabsContent>

                <TabsContent value="financials" className="mt-0">
                    <MatrixTable
                        capabilities={FINANCIALS_CAPABILITIES}
                        permissions={permissions}
                        onToggle={handleToggle}
                        searchQuery={searchQuery}
                        capabilitiesLabel={t("securityCenter.roles.capabilities")}
                        roleLabels={roleLabels}
                        t={t}
                    />
                </TabsContent>

                <TabsContent value="security" className="mt-0">
                    <MatrixTable
                        capabilities={SECURITY_CAPABILITIES}
                        permissions={permissions}
                        onToggle={handleToggle}
                        searchQuery={searchQuery}
                        capabilitiesLabel={t("securityCenter.roles.capabilities")}
                        roleLabels={roleLabels}
                        t={t}
                    />
                </TabsContent>
            </Tabs>

            {/* Footer Actions */}
            <div className="flex flex-col sm:flex-row justify-between items-start sm:items-center gap-4 pt-4">
                <div className="flex items-center gap-2 rounded-lg bg-primary/10 text-primary px-4 py-3 text-sm">
                    <Info className="h-4 w-4 shrink-0" />
                    <span>
                        {t("securityCenter.roles.changesApplied")}
                    </span>
                </div>
                <div className="flex gap-2 shrink-0">
                    <Button
                        variant="ghost"
                        onClick={handleDiscard}
                        disabled={!hasChanges}
                        className="text-muted-foreground"
                    >
                        {t("securityCenter.roles.discardChanges")}
                    </Button>
                    <Button onClick={handleSave} disabled={!hasChanges || saving}>
                        {saving ? (
                            <>
                                <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                                {t("securityCenter.roles.saving")}
                            </>
                        ) : (
                            t("securityCenter.roles.saveConfiguration")
                        )}
                    </Button>
                </div>
            </div>
        </div>
    );
}

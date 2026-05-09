"use client";

import { useEffect, useState } from "react";
import type { Functions } from "firebase/functions";
import { httpsCallable } from "firebase/functions";
import { collection, query, orderBy, limit, onSnapshot } from "firebase/firestore";
import { db, functions } from "@/firebase/client";
import { usePermission } from "@/hooks/usePermission";
import { CAPABILITIES } from "@/lib/capabilities";
import { COLLECTIONS } from "@/lib/collections";
import { ROLE_IDS } from "@/lib/roles";
import { useAuth } from "@/context/auth";
import { getCustomers } from "@/features/customers/api/customers";
import type { CustomerData } from "@/features/customers/api/customers";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Loader2, Shield, User, Plus, Edit, ArrowUpDown, ArrowUp, ArrowDown, Search, MoreHorizontal, X, Truck, LogOut } from "lucide-react";
import { toast } from "sonner";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useLanguage } from "@/context/language";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

type UserData = {
    uid: string;
    email: string;
    displayName: string;
    photoURL?: string;
    customClaims?: {
        admin?: boolean;
        role?: string;
        partnerScopeId?: string;
        [key: string]: any;
    };
    metadata: {
        lastSignInTime: string;
        creationTime: string;
        lastRefreshTime?: string | null;
    };
    providerData: string[];
    disabled?: boolean;
};

function PartnerScopeCell({
    user,
    functions: functionsInstance,
}: {
    user: UserData;
    functions: Functions;
}) {
    const { t } = useLanguage();
    const initial =
        typeof user.customClaims?.partnerScopeId === "string" ? user.customClaims.partnerScopeId : "";
    const [value, setValue] = useState(initial);

    useEffect(() => {
        setValue(typeof user.customClaims?.partnerScopeId === "string" ? user.customClaims.partnerScopeId : "");
    }, [user.uid, user.customClaims?.partnerScopeId]);

    const [saving, setSaving] = useState(false);
    const save = async () => {
        setSaving(true);
        try {
            const updateUserRole = httpsCallable(functionsInstance, "updateUserRole");
            await updateUserRole({
                targetUid: user.uid,
                role: "partner",
                partnerScopeId: value.trim(),
            });
            toast.success(t("users.partnerScopeSaved"));
        } catch {
            toast.error(t("users.partnerScopeSaveFailed"));
        } finally {
            setSaving(false);
        }
    };

    return (
        <div className="flex flex-col gap-1 min-w-[160px]">
            <Input
                className="h-8 text-xs"
                value={value}
                onChange={(e) => setValue(e.target.value)}
                placeholder={t("users.partnerScopePlaceholder")}
            />
            <Button type="button" size="sm" className="h-7 text-xs w-fit" onClick={save} disabled={saving}>
                {t("users.partnerScopeSave")}
            </Button>
        </div>
    );
}

function EditUserDialog({
    user,
    open,
    onOpenChange,
    functions: functionsInstance,
    customers,
    onSaveSuccess,
}: {
    user: UserData | null;
    open: boolean;
    onOpenChange: (open: boolean) => void;
    functions: Functions;
    customers: CustomerData[];
    onSaveSuccess?: () => void;
}) {
    const { t } = useLanguage();

    const [role, setRole] = useState("user");
    const [partnerScopeId, setPartnerScopeId] = useState("");
    const [customerScopeId, setCustomerScopeId] = useState("");
    const [saving, setSaving] = useState(false);

    useEffect(() => {
        if (open && user) {
            const currentRole = user.customClaims?.role || (user.customClaims?.admin ? "admin" : "user");
            setRole(currentRole);
            setPartnerScopeId(typeof user.customClaims?.partnerScopeId === "string" ? user.customClaims.partnerScopeId : "");
            setCustomerScopeId(typeof user.customClaims?.customerScopeId === "string" ? user.customClaims.customerScopeId : "");
        }
    }, [open, user]);

    const save = async () => {
        if (!user) return;
        setSaving(true);
        try {
            const updateUserRole = httpsCallable(functionsInstance, "updateUserRole");
            await updateUserRole({
                targetUid: user.uid,
                role,
                partnerScopeId: role === "partner" ? partnerScopeId.trim() : undefined,
                customerScopeId: role === "customer" ? customerScopeId.trim() : undefined,
            });
            toast.success(t("users.toast.roleUpdated"));
            onOpenChange(false);
            onSaveSuccess?.();
        } catch (error) {
            console.error("Error updating user:", error);
            toast.error(t("users.toast.roleUpdateFailed"));
        } finally {
            setSaving(false);
        }
    };

    if (!user) return null;

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="sm:max-w-[500px]">
                <DialogHeader>
                    <DialogTitle>{t("users.editRole")}</DialogTitle>
                    <DialogDescription>
                        {t("users.form.displayName")}: <span className="font-medium text-foreground">{user.displayName}</span>
                        <br />
                        {t("users.form.email")}: <span className="font-medium text-foreground">{user.email}</span>
                    </DialogDescription>
                </DialogHeader>

                <div className="space-y-4 py-4">
                    {/* Role */}
                    <div className="space-y-2">
                        <Label htmlFor="edit-role">{t("users.form.role")}</Label>
                        <Select value={role} onValueChange={setRole} disabled={saving}>
                            <SelectTrigger id="edit-role">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent position="popper" className="z-[1005]">
                                {ROLE_IDS.map((roleId) => (
                                    <SelectItem key={roleId} value={roleId}>
                                        {t(`users.role.${roleId}`)}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>

                    {/* Partner Scope */}
                    {role === "partner" && (
                        <div className="space-y-2">
                            <Label htmlFor="partner-scope">{t("users.partnerScope")}</Label>
                            <Input
                                id="partner-scope"
                                value={partnerScopeId}
                                onChange={(e) => setPartnerScopeId(e.target.value)}
                                placeholder={t("users.partnerScopePlaceholder")}
                                disabled={saving}
                            />
                        </div>
                    )}

                    {/* Customer Scope */}
                    {role === "customer" && (
                        <div className="space-y-2">
                            <Label htmlFor="customer-scope">{t("users.customerScope")}</Label>
                            <Select value={customerScopeId} onValueChange={setCustomerScopeId} disabled={saving}>
                                <SelectTrigger id="customer-scope">
                                    <SelectValue placeholder={t("users.customerScopePlaceholder")} />
                                </SelectTrigger>
                                <SelectContent position="popper" className="z-[1005]">
                                    {customers.map((customer) => (
                                        <SelectItem key={customer.id} value={customer.id}>
                                            {customer.name} ({customer.code})
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                    )}
                </div>

                <DialogFooter>
                    <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
                        {t("users.form.cancel")}
                    </Button>
                    <Button type="button" onClick={save} disabled={saving}>
                        {saving && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                        {t("users.form.save")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

export default function AdminUsersPage() {
    const { t } = useLanguage();
    const [users, setUsers] = useState<UserData[]>([]);
    const [loading, setLoading] = useState(true);
    const [isCreateOpen, setIsCreateOpen] = useState(false);
    const [isCreating, setIsCreating] = useState(false);

    // Edit Role State
    const [editRoleUser, setEditRoleUser] = useState<UserData | null>(null);
    const [editRoleValue, setEditRoleValue] = useState("");
    const [isEditRoleLoading, setIsEditRoleLoading] = useState(false);

    // Edit User State
    const [editUser, setEditUser] = useState<UserData | null>(null);

    // Form state
    const [newUserEmail, setNewUserEmail] = useState("");
    const [newUserPassword, setNewUserPassword] = useState("");
    const [newUserDisplayName, setNewUserDisplayName] = useState("");
    const [newUserRole, setNewUserRole] = useState("user");
    const [newUserPartnerScopeId, setNewUserPartnerScopeId] = useState("");
    const [newUserCustomerScopeId, setNewUserCustomerScopeId] = useState("");

    // Sorting State
    const [sortConfig, setSortConfig] = useState<{ key: string; direction: 'asc' | 'desc' } | null>(null);

    // Sync State
    const [isSyncing, setIsSyncing] = useState(false);
    const [revokeTarget, setRevokeTarget] = useState<UserData | null>(null);
    const [isRevoking, setIsRevoking] = useState(false);
    const { hasPermission: canManageUsers, loading: manageUsersPermLoading } = usePermission(
        CAPABILITIES.security_manage_users,
    );

    const auth = useAuth();
    const currentUser = auth?.currentUser;

    const [limitCount, setLimitCount] = useState(50);
    const [searchQuery, setSearchQuery] = useState("");
    const [roleFilter, setRoleFilter] = useState<string>("all");
    const [statusFilter, setStatusFilter] = useState<string>("all");
    const [customers, setCustomers] = useState<CustomerData[]>([]);

    // Status Logic - returns "active" | "inactive" for translation
    const calculateStatus = (lastSignInTime: string | null): "active" | "inactive" => {
        if (!lastSignInTime) return "inactive";
        const lastLogin = new Date(lastSignInTime);
        const thirtyDaysAgo = new Date();
        thirtyDaysAgo.setDate(thirtyDaysAgo.getDate() - 30);
        return lastLogin > thirtyDaysAgo ? "active" : "inactive";
    };

    // Fetch users from Firestore
    useEffect(() => {
        if (!currentUser) return;

        setLoading(true);
        const usersRef = collection(db, COLLECTIONS.USERS);
        // Order by lastLogin if available, otherwise uid
        const q = query(usersRef, orderBy("lastLogin", "desc"), limit(limitCount));

        const unsubscribe = onSnapshot(q, (snapshot) => {
            const usersData: UserData[] = [];
            snapshot.forEach((doc) => {
                const data = doc.data();
                usersData.push({
                    uid: doc.id,
                    email: data.email || "",
                    displayName: data.displayName || "",
                    photoURL: data.photoURL,
                    // Map Firestore 'role' to customClaims structure to match UI expectation
                    customClaims: {
                        role: data.role,
                        admin: data.role === "admin",
                        ...(typeof data.partnerScopeId === "string" && data.partnerScopeId.trim()
                            ? { partnerScopeId: data.partnerScopeId.trim() }
                            : {}),
                        ...(typeof data.customerScopeId === "string" && data.customerScopeId.trim()
                            ? { customerScopeId: data.customerScopeId.trim() }
                            : {}),
                    },
                    metadata: {
                        lastSignInTime: data.lastLogin || null,
                        creationTime: data.authCreationTime || null,
                    },
                    providerData: data.providerData || [], // Fallback if not scheduled
                    disabled: data.disabled || false,
                } as unknown as UserData);
            });
            setUsers(usersData);
            setLoading(false);
        }, (err) => {
            console.error("Error fetching users from Firestore:", err);
            setLoading(false);
            toast.error(t("users.toast.loadFailed"));
        });

        return () => unsubscribe();
    }, [currentUser, limitCount]);

    // Load customers for dropdown displays
    useEffect(() => {
        const loadCustomers = async () => {
            try {
                const data = await getCustomers();
                setCustomers(data);
            } catch (error) {
                console.error("Error loading customers:", error);
            }
        };
        loadCustomers();
    }, []);

    // Refresh users from Cloud Function (calls getUsers which fetches from Firebase Auth)
    const fetchUsers = async () => {
        try {
            const getUsers = httpsCallable(functions, 'getUsers');
            const result = await getUsers({}) as { data: { users: UserData[] } };
            const usersData = result.data.users.map(user => ({
                ...user,
                customClaims: {
                    role: user.customClaims?.role || (user.customClaims?.admin ? "admin" : "user"),
                    admin: user.customClaims?.admin || false,
                    ...(typeof user.customClaims?.partnerScopeId === "string" ? { partnerScopeId: user.customClaims.partnerScopeId } : {}),
                    ...(typeof user.customClaims?.customerScopeId === "string" ? { customerScopeId: user.customClaims.customerScopeId } : {}),
                },
                metadata: {
                    lastSignInTime: user.metadata?.lastSignInTime || null,
                    creationTime: user.metadata?.creationTime || null,
                },
            })) as UserData[];
            setUsers(usersData);
        } catch (err) {
            console.error("Error refreshing users:", err);
        }
    };

    const handleCreateUser = async (e: React.FormEvent) => {
        e.preventDefault();

        if (!newUserEmail || !newUserPassword || !newUserDisplayName) {
            toast.error(t("users.toast.fillRequired"));
            return;
        }

        try {
            setIsCreating(true);
            const createUser = httpsCallable(functions, 'createUser');
            await createUser({
                email: newUserEmail,
                password: newUserPassword,
                displayName: newUserDisplayName,
                role: newUserRole,
                ...(newUserRole === "partner" && newUserPartnerScopeId.trim()
                    ? { partnerScopeId: newUserPartnerScopeId.trim() }
                    : {}),
                ...(newUserRole === "customer" && newUserCustomerScopeId.trim()
                    ? { customerScopeId: newUserCustomerScopeId.trim() }
                    : {}),
            });

            toast.success(t("users.toast.created"));
            setIsCreateOpen(false);

            // Reset form
            setNewUserEmail("");
            setNewUserPassword("");
            setNewUserDisplayName("");
            setNewUserRole("user");
            setNewUserPartnerScopeId("");
            setNewUserCustomerScopeId("");

            // Refresh list
            fetchUsers();
        } catch (error: any) {
            console.error("Error creating user:", error);
            toast.error(`${t("users.toast.createFailed")}: ${error.message || t("users.toast.unknownError")}`);
        } finally {
            setIsCreating(false);
        }
    };

    const handleEditRole = async () => {
        if (!editRoleUser) return;

        try {
            setIsEditRoleLoading(true);
            const updateUserRole = httpsCallable(functions, 'updateUserRole');
            const existingScope = (editRoleUser.customClaims as { partnerScopeId?: string } | undefined)
                ?.partnerScopeId;
            await updateUserRole({
                targetUid: editRoleUser.uid,
                role: editRoleValue,
                partnerScopeId:
                    editRoleValue === "partner" && typeof existingScope === "string" ? existingScope : undefined,
            });

            toast.success(t("users.toast.roleUpdated"));
            setEditRoleUser(null);

            // Refresh list
            fetchUsers();

        } catch (error) {
            console.error("Error updating user role:", error);
            toast.error(t("users.toast.roleUpdateFailed"));
        } finally {
            setIsEditRoleLoading(false);
        }
    };

    const handleEditRoleSpecific = async (user: UserData, newRole: string) => {
        try {
            setIsEditRoleLoading(true);
            const updateUserRole = httpsCallable(functions, 'updateUserRole');
            const existingScope = (user.customClaims as { partnerScopeId?: string } | undefined)?.partnerScopeId;
            await updateUserRole({
                targetUid: user.uid,
                role: newRole,
                partnerScopeId:
                    newRole === "partner" && typeof existingScope === "string" ? existingScope : undefined,
            });
            toast.success(
                <div className="flex flex-col gap-0.5">
                    <span>Role updated to {newRole}</span>
                    <span className="text-xs text-muted-foreground">{t("users.roleUpdatedHint")}</span>
                </div>,
                { duration: 6000 }
            );
        } catch (error) {
            console.error("Error updating user role:", error);
            toast.error(t("users.toast.roleUpdateFailed"));
        } finally {
            setIsEditRoleLoading(false);
        }
    };

    const confirmRevokeSessions = async () => {
        if (!revokeTarget) return;
        setIsRevoking(true);
        try {
            const revoke = httpsCallable<{ targetUid: string }, { ok: boolean }>(functions, "revokeUserRefreshTokens");
            await revoke({ targetUid: revokeTarget.uid });
            toast.success(t("users.revokeSessionsSuccess"));
            setRevokeTarget(null);
        } catch (error: unknown) {
            console.error("[Users] revokeUserRefreshTokens:", error);
            toast.error(t("users.revokeSessionsFailed"));
        } finally {
            setIsRevoking(false);
        }
    };

    const openEditRole = (user: UserData) => {
        setEditRoleUser(user);
        setEditRoleValue(user.customClaims?.role || (user.customClaims?.admin ? "admin" : "user"));
    };

    const getRoleBadge = (user: UserData) => {
        const role = user.customClaims?.role || (user.customClaims?.admin ? "admin" : "user");

        switch (role) {
            case 'admin':
                return <Badge className="bg-green-700 hover:bg-slate-600"><Shield className="w-3 h-3 mr-1" /> {t("users.role.admin")}</Badge>;
            case 'manager':
                return <Badge className="bg-indigo-600 hover:bg-indigo-700"><User className="w-3 h-3 mr-1" /> {t("users.role.manager")}</Badge>;
            case 'operation_staff':
                return <Badge className="bg-teal-600 hover:bg-teal-700"><User className="w-3 h-3 mr-1" /> {t("users.role.operation_staff")}</Badge>;
            case 'operator':
                return <Badge className="bg-cyan-600 hover:bg-cyan-700"><User className="w-3 h-3 mr-1" /> {t("users.role.operator")}</Badge>;
            case 'driver':
                return <Badge className="bg-amber-600 hover:bg-amber-700"><Truck className="w-3 h-3 mr-1" /> {t("users.role.driver")}</Badge>;
            case 'partner':
                return <Badge className="bg-purple-500 hover:bg-purple-600"><User className="w-3 h-3 mr-1" /> {t("users.role.partner")}</Badge>;
            case 'customer':
                return <Badge className="bg-blue-500 hover:bg-blue-600"><User className="w-3 h-3 mr-1" /> {t("users.role.customer")}</Badge>;
            case 'subcontractor':
                return <Badge className="bg-orange-500 hover:bg-orange-600"><User className="w-3 h-3 mr-1" /> {t("users.role.subcontractor")}</Badge>;
            default:
                return <Badge variant="outline"><User className="w-3 h-3 mr-1" /> {t("users.role.user")}</Badge>;
        }
    };

    const handleSyncUsers = async () => {
        try {
            setIsSyncing(true);
            const syncExistingUsers = httpsCallable(functions, 'syncExistingUsers');
            const result = await syncExistingUsers();
            const data = result.data as { message: string };
            toast.success(data.message);
            fetchUsers();
        } catch (error: any) {
            console.error("Error syncing users:", error);
            toast.error(`${t("users.toast.syncFailed")}: ${error.message}`);
        } finally {
            setIsSyncing(false);
        }
    };

    const handleSort = (key: string) => {
        let direction: 'asc' | 'desc' = 'asc';
        if (sortConfig && sortConfig.key === key && sortConfig.direction === 'asc') {
            direction = 'desc';
        }
        setSortConfig({ key, direction });
    };

    const filteredUsers = users.filter((user) => {
        const query = searchQuery.toLowerCase();
        const role = user.customClaims?.role || (user.customClaims?.admin ? "admin" : "user");
        const status = calculateStatus(user.metadata.lastSignInTime);

        // Role filter
        if (roleFilter !== "all" && role !== roleFilter) return false;

        // Status filter
        if (statusFilter !== "all" && status !== statusFilter) return false;

        // Search filter
        if (query) {
            const matchesSearch =
                (user.displayName?.toLowerCase() || "").includes(query) ||
                (user.email?.toLowerCase() || "").includes(query) ||
                role.toLowerCase().includes(query) ||
                t(`users.status.${status}`).toLowerCase().includes(query);
            if (!matchesSearch) return false;
        }

        return true;
    });

    const sortedUsers = [...filteredUsers].sort((a, b) => {
        const currentSort = sortConfig;
        if (!currentSort) return 0;

        let aValue: any = a[currentSort.key as keyof UserData];
        let bValue: any = b[currentSort.key as keyof UserData];

        // Custom handling for nested/special fields
        if (currentSort.key === 'role') {
            aValue = a.customClaims?.role || (a.customClaims?.admin ? "admin" : "user");
            bValue = b.customClaims?.role || (b.customClaims?.admin ? "admin" : "user");
        } else if (currentSort.key === 'lastSignInTime') {
            aValue = a.metadata.lastSignInTime ? new Date(a.metadata.lastSignInTime).getTime() : 0;
            bValue = b.metadata.lastSignInTime ? new Date(b.metadata.lastSignInTime).getTime() : 0;
        }

        if (aValue < bValue) return currentSort.direction === 'asc' ? -1 : 1;
        if (aValue > bValue) return currentSort.direction === 'asc' ? 1 : -1;
        return 0;
    });

    const getSortIcon = (key: string) => {
        if (!sortConfig || sortConfig.key !== key) return <ArrowUpDown className="ml-2 h-4 w-4" />;
        return sortConfig.direction === 'asc' ? <ArrowUp className="ml-2 h-4 w-4" /> : <ArrowDown className="ml-2 h-4 w-4" />;
    };

    if (loading && users.length === 0) {
        return (
            <div className="flex h-[50vh] items-center justify-center">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
            </div>
        );
    }

    return (
        <div className="p-6 space-y-6">
            <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("users.title")}</h1>
                    <p className="text-muted-foreground mt-2">
                        {t("users.subtitle")}
                    </p>
                </div>
                <div className="flex flex-wrap gap-2">
                    <Button onClick={handleSyncUsers} variant="outline" disabled={isSyncing} className="gap-2">
                        {isSyncing ? <Loader2 className="h-4 w-4 animate-spin" /> : <Loader2 className="h-4 w-4" />}
                        {t("users.refresh")}
                    </Button>
                    <Dialog open={isCreateOpen} onOpenChange={setIsCreateOpen}>
                        <DialogTrigger asChild>
                            <Button>
                                <Plus className="mr-2 h-4 w-4" />
                                <span className="hidden sm:inline">{t("users.add")}</span>
                                <span className="sm:hidden">{t("users.addShort")}</span>
                            </Button>
                        </DialogTrigger>
                        <DialogContent>
                            <DialogHeader>
                                <DialogTitle>{t("users.createTitle")}</DialogTitle>
                                <DialogDescription>
                                    {t("users.createDesc")}
                                </DialogDescription>
                            </DialogHeader>
                            <form onSubmit={handleCreateUser} className="space-y-4">
                                <div className="space-y-2">
                                    <Label htmlFor="displayName">{t("users.form.displayName")}</Label>
                                    <Input
                                        id="displayName"
                                        value={newUserDisplayName}
                                        onChange={(e) => setNewUserDisplayName(e.target.value)}
                                        placeholder="John Doe"
                                        required
                                    />
                                </div>
                                <div className="space-y-2">
                                    <Label htmlFor="email">{t("users.form.email")}</Label>
                                    <Input
                                        id="email"
                                        type="email"
                                        value={newUserEmail}
                                        onChange={(e) => setNewUserEmail(e.target.value)}
                                        placeholder="john@example.com"
                                        required
                                    />
                                </div>
                                <div className="space-y-2">
                                    <Label htmlFor="password">{t("users.form.password")}</Label>
                                    <Input
                                        id="password"
                                        type="password"
                                        value={newUserPassword}
                                        onChange={(e) => setNewUserPassword(e.target.value)}
                                        placeholder="Min. 6 characters"
                                        required
                                        minLength={6}
                                    />
                                </div>
                                <div className="space-y-2">
                                    <Label htmlFor="role">{t("users.form.role")}</Label>
                                    <Select value={newUserRole} onValueChange={setNewUserRole}>
                                        <SelectTrigger>
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent>
                                            {ROLE_IDS.map((roleId) => (
                                                <SelectItem key={roleId} value={roleId}>
                                                    {t(`users.role.${roleId}`)}
                                                </SelectItem>
                                            ))}
                                        </SelectContent>
                                    </Select>
                                </div>
                                {newUserRole === "partner" && (
                                    <div className="space-y-2">
                                        <Label htmlFor="partnerScope">{t("users.partnerScope")}</Label>
                                        <Input
                                            id="partnerScope"
                                            value={newUserPartnerScopeId}
                                            onChange={(e) => setNewUserPartnerScopeId(e.target.value)}
                                            placeholder={t("users.partnerScopePlaceholder")}
                                        />
                                    </div>
                                )}
                                {newUserRole === "customer" && (
                                    <div className="space-y-2">
                                        <Label htmlFor="customerScope">{t("users.customerScope")}</Label>
                                        <Input
                                            id="customerScope"
                                            value={newUserCustomerScopeId}
                                            onChange={(e) => setNewUserCustomerScopeId(e.target.value)}
                                            placeholder={t("users.customerScopePlaceholder")}
                                        />
                                    </div>
                                )}
                                <DialogFooter>
                                    <Button type="button" variant="outline" onClick={() => setIsCreateOpen(false)}>
                                        {t("users.form.cancel")}
                                    </Button>
                                    <Button type="submit" disabled={isCreating}>
                                        {isCreating && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                                        {t("users.form.create")}
                                    </Button>
                                </DialogFooter>
                            </form>
                        </DialogContent>
                    </Dialog>

                    <Dialog open={!!revokeTarget} onOpenChange={(open) => !open && setRevokeTarget(null)}>
                        <DialogContent>
                            <DialogHeader>
                                <DialogTitle>{t("users.revokeSessionsTitle")}</DialogTitle>
                                <DialogDescription className="space-y-2">
                                    <span>{t("users.revokeSessionsDesc")}</span>
                                    {revokeTarget ? (
                                        <span className="block text-foreground font-medium">
                                            {revokeTarget.displayName} ({revokeTarget.email})
                                        </span>
                                    ) : null}
                                </DialogDescription>
                            </DialogHeader>
                            <DialogFooter>
                                <Button type="button" variant="outline" onClick={() => setRevokeTarget(null)} disabled={isRevoking}>
                                    {t("users.revokeSessionsCancel")}
                                </Button>
                                <Button type="button" variant="destructive" onClick={() => void confirmRevokeSessions()} disabled={isRevoking}>
                                    {isRevoking ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                                    {t("users.revokeSessionsConfirm")}
                                </Button>
                            </DialogFooter>
                        </DialogContent>
                    </Dialog>

                    <EditUserDialog
                        user={editUser}
                        open={!!editUser}
                        onOpenChange={(open) => !open && setEditUser(null)}
                        functions={functions}
                        customers={customers}
                        onSaveSuccess={() => fetchUsers()}
                    />
                </div>
            </div>

            {/* Filters & Search */}
            <div className="flex flex-col gap-4 border-b border-border/40 pb-px">
                <div className="flex items-center gap-2">
                    <h2 className="text-sm font-semibold text-foreground">{t("users.tabs.allUsers")}</h2>
                </div>
                <div className="flex flex-wrap items-center gap-3">
                    <Select value={roleFilter} onValueChange={setRoleFilter}>
                        <SelectTrigger className="w-[160px] h-9">
                            <SelectValue placeholder={t("users.filter.role")} />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">{t("users.filter.roleAll")}</SelectItem>
                            {ROLE_IDS.map((roleId) => (
                                <SelectItem key={roleId} value={roleId}>
                                    {t(`users.role.${roleId}`)}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                    <Select value={statusFilter} onValueChange={setStatusFilter}>
                        <SelectTrigger className="w-[140px] h-9">
                            <SelectValue placeholder={t("users.filter.status")} />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">{t("users.filter.statusAll")}</SelectItem>
                            <SelectItem value="active">{t("users.status.active")}</SelectItem>
                            <SelectItem value="inactive">{t("users.status.inactive")}</SelectItem>
                        </SelectContent>
                    </Select>
                    <div className="relative flex-1 min-w-[200px] max-w-[280px]">
                        <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                        <Input
                            placeholder={t("users.searchPlaceholder")}
                            value={searchQuery}
                            onChange={(e) => setSearchQuery(e.target.value)}
                            className="pl-9 h-9"
                        />
                    </div>
                </div>
            </div>

            <div className="w-full space-y-6 mt-6">
                <Card className="bg-card border-border shadow-sm">
                        <div className="rounded-md overflow-hidden">
                            <Table>
                                <TableHeader className="bg-muted/40">
                                    <TableRow className="hover:bg-transparent border-none">
                                        <TableHead className="h-11 cursor-pointer pl-6" onClick={() => handleSort('displayName')}>
                                            <div className="flex items-center text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                                                {t("users.table.user")} {getSortIcon('displayName')}
                                            </div>
                                        </TableHead>
                                        <TableHead className="h-11 cursor-pointer" onClick={() => handleSort('role')}>
                                            <div className="flex items-center text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                                                {t("users.table.role")} {getSortIcon('role')}
                                            </div>
                                        </TableHead>
                                        <TableHead className="h-11 min-w-[180px]">
                                            <div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                                                {t("users.scope")}
                                            </div>
                                        </TableHead>
                                        <TableHead className="h-11">
                                            <div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{t("users.table.status")}</div>
                                        </TableHead>
                                        <TableHead className="h-11 cursor-pointer" onClick={() => handleSort('lastSignInTime')}>
                                            <div className="flex items-center text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                                                {t("users.table.lastSignIn")} {getSortIcon('lastSignInTime')}
                                            </div>
                                        </TableHead>
                                        {canManageUsers && !manageUsersPermLoading ? (
                                            <TableHead className="h-11 w-[52px] text-right">
                                                <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                                                    {t("users.table.actions")}
                                                </span>
                                            </TableHead>
                                        ) : null}
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {sortedUsers.map((user) => {
                                        const isCurrentUser = currentUser?.uid === user.uid;
                                        const role = user.customClaims?.role || (user.customClaims?.admin ? "admin" : "user");
                                        const status = calculateStatus(user.metadata.lastSignInTime);

                                        return (
                                            <TableRow key={user.uid} className="hover:bg-muted/30 border-b border-border/50 transition-colors">
                                                <TableCell className="pl-6 py-4">
                                                    <div className="flex items-center gap-3">
                                                        <Avatar className="h-9 w-9 border border-border shadow-sm">
                                                            <AvatarImage src={user.photoURL} alt={user.displayName} />
                                                            <AvatarFallback className={cn("text-xs font-bold text-white shadow-inner", status === "active" ? "bg-blue-600" : "bg-gray-500")}>
                                                                {user.displayName?.substring(0, 2).toUpperCase()}
                                                            </AvatarFallback>
                                                        </Avatar>
                                                        <div className="flex flex-col">
                                                            <span className="font-semibold text-sm text-foreground">{user.displayName || "No Name"}</span>
                                                            <span className="text-xs text-muted-foreground">{user.email}</span>
                                                        </div>
                                                    </div>
                                                </TableCell>
                                                <TableCell>
                                                    {getRoleBadge(user)}
                                                </TableCell>
                                                <TableCell className="align-top py-3">
                                                    {role === "partner" && user.customClaims?.partnerScopeId ? (
                                                        <span className="text-xs font-medium text-foreground">{user.customClaims.partnerScopeId}</span>
                                                    ) : role === "customer" && user.customClaims?.customerScopeId ? (
                                                        <span className="text-xs font-medium text-foreground">
                                                            {customers.find((c) => c.id === user.customClaims?.customerScopeId)?.name || user.customClaims.customerScopeId}
                                                        </span>
                                                    ) : (
                                                        <span className="text-xs text-muted-foreground">—</span>
                                                    )}
                                                </TableCell>
                                                <TableCell>
                                                    <div className={cn("inline-flex items-center px-2.5 py-1 rounded-full text-xs font-medium border",
                                                        status === "active" ? "bg-green-500/10 text-green-600 border-green-200/50" : "bg-gray-100 text-gray-500 border-gray-200"
                                                    )}>
                                                        <span className={cn("w-1.5 h-1.5 rounded-full mr-1.5", status === "active" ? "bg-green-500" : "bg-gray-400")}></span>
                                                        {t(`users.status.${status}`)}
                                                    </div>
                                                </TableCell>
                                                <TableCell>
                                                    <div className="text-sm font-medium text-foreground/80">
                                                        {user.metadata.lastSignInTime ? new Date(user.metadata.lastSignInTime).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) : "Never"}
                                                    </div>
                                                    <div className="text-[10px] text-muted-foreground uppercase tracking-wide">
                                                        {user.metadata.lastSignInTime ? new Date(user.metadata.lastSignInTime).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' }) : "-"}
                                                    </div>
                                                </TableCell>
                                                {canManageUsers && !manageUsersPermLoading ? (
                                                    <TableCell className="text-right py-2">
                                                        <DropdownMenu>
                                                            <DropdownMenuTrigger asChild>
                                                                <Button variant="ghost" size="icon" className="h-8 w-8" aria-label={t("users.table.actions")}>
                                                                    <MoreHorizontal className="h-4 w-4" />
                                                                </Button>
                                                            </DropdownMenuTrigger>
                                                            <DropdownMenuContent align="end">
                                                                <DropdownMenuItem
                                                                    onSelect={() => setEditUser(user)}
                                                                >
                                                                    <Edit className="mr-2 h-4 w-4" />
                                                                    {t("users.editRole")}
                                                                </DropdownMenuItem>
                                                                <DropdownMenuItem
                                                                    disabled={isCurrentUser}
                                                                    title={isCurrentUser ? t("users.revokeSessionsSelf") : undefined}
                                                                    onSelect={() => {
                                                                        if (!isCurrentUser) setRevokeTarget(user);
                                                                    }}
                                                                >
                                                                    <LogOut className="mr-2 h-4 w-4" />
                                                                    {t("users.revokeSessionsShort")}
                                                                </DropdownMenuItem>
                                                            </DropdownMenuContent>
                                                        </DropdownMenu>
                                                    </TableCell>
                                                ) : null}
                                            </TableRow>
                                        );
                                    })}
                                </TableBody>
                            </Table>
                        </div>
                    </Card>
            </div>

        </div>
    );
}

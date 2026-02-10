"use client";

import { useEffect, useState } from "react";
import { getFunctions, httpsCallable } from "firebase/functions";
import { collection, query, orderBy, limit, onSnapshot } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { useAuth } from "@/context/auth";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Loader2, Shield, User, Plus, Edit, ArrowUpDown, ArrowUp, ArrowDown, Search, MoreHorizontal, X, Truck, Box, Settings } from "lucide-react";
import { toast } from "sonner";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useLanguage } from "@/context/language";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Switch } from "@/components/ui/switch";
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

    // New State for Permissions Panel
    const [selectedUser, setSelectedUser] = useState<UserData | null>(null);

    // Form state
    const [newUserEmail, setNewUserEmail] = useState("");
    const [newUserPassword, setNewUserPassword] = useState("");
    const [newUserDisplayName, setNewUserDisplayName] = useState("");
    const [newUserRole, setNewUserRole] = useState("user");

    // Sorting State
    const [sortConfig, setSortConfig] = useState<{ key: string; direction: 'asc' | 'desc' } | null>(null);

    // Sync State
    const [isSyncing, setIsSyncing] = useState(false);

    const auth = useAuth();
    const currentUser = auth?.currentUser;
    const functions = getFunctions(undefined, "asia-southeast1");

    const [limitCount, setLimitCount] = useState(50);
    const [searchQuery, setSearchQuery] = useState("");

    // Status Logic
    const calculateStatus = (lastSignInTime: string | null) => {
        if (!lastSignInTime) return "Inactive";
        const lastLogin = new Date(lastSignInTime);
        const thirtyDaysAgo = new Date();
        thirtyDaysAgo.setDate(thirtyDaysAgo.getDate() - 30);
        return lastLogin > thirtyDaysAgo ? "Active" : "Inactive";
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
                        admin: data.role === 'admin'
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
            toast.error("Failed to load users from live database.");
        });

        return () => unsubscribe();
    }, [currentUser, limitCount]);

    // Legacy Cloud Function fetch (kept for reference or full sync)
    const fetchUsers = async () => {
        // Only used for manual refresh if needed, but onSnapshot handles it.
        // We can keep the Cloud Function for "Sync" but not for display.
    };

    const handleCreateUser = async (e: React.FormEvent) => {
        e.preventDefault();

        if (!newUserEmail || !newUserPassword || !newUserDisplayName) {
            toast.error("Please fill in all required fields");
            return;
        }

        try {
            setIsCreating(true);
            const createUser = httpsCallable(functions, 'createUser');
            await createUser({
                email: newUserEmail,
                password: newUserPassword,
                displayName: newUserDisplayName,
                role: newUserRole
            });

            toast.success("User created successfully");
            setIsCreateOpen(false);

            // Reset form
            setNewUserEmail("");
            setNewUserPassword("");
            setNewUserDisplayName("");
            setNewUserRole("user");

            // Refresh list
            fetchUsers();
        } catch (error: any) {
            console.error("Error creating user:", error);
            toast.error(`Failed to create user: ${error.message || "Unknown error"}`);
        } finally {
            setIsCreating(false);
        }
    };

    const handleEditRole = async () => {
        if (!editRoleUser) return;

        try {
            setIsEditRoleLoading(true);
            const updateUserRole = httpsCallable(functions, 'updateUserRole');
            await updateUserRole({
                targetUid: editRoleUser.uid,
                role: editRoleValue
            });

            toast.success("User role updated successfully.");
            setEditRoleUser(null);

            // Refresh list
            fetchUsers();

        } catch (error) {
            console.error("Error updating user role:", error);
            toast.error("Failed to update user role.");
        } finally {
            setIsEditRoleLoading(false);
        }
    };

    const handleEditRoleSpecific = async (user: UserData, newRole: string) => {
        try {
            setIsEditRoleLoading(true);
            const updateUserRole = httpsCallable(functions, 'updateUserRole');
            await updateUserRole({
                targetUid: user.uid,
                role: newRole
            });
            toast.success(`Role updated to ${newRole}`);
        } catch (error) {
            console.error("Error updating user role:", error);
            toast.error("Failed to update user role.");
        } finally {
            setIsEditRoleLoading(false);
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
            case 'partner':
                return <Badge className="bg-purple-500 hover:bg-purple-600"><User className="w-3 h-3 mr-1" /> {t("users.role.partner")}</Badge>;
            case 'subcontractor':
                return <Badge className="bg-orange-500 hover:bg-orange-600"><User className="w-3 h-3 mr-1" /> {t("users.role.subcontractor")}</Badge>;
            case 'customer':
                return <Badge className="bg-blue-500 hover:bg-blue-600"><User className="w-3 h-3 mr-1" /> {t("users.role.customer")}</Badge>;
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
            toast.error(`Failed to sync users: ${error.message}`);
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
        const status = calculateStatus(user.metadata.lastSignInTime).toLowerCase();

        return (
            (user.displayName?.toLowerCase() || "").includes(query) ||
            (user.email?.toLowerCase() || "").includes(query) ||
            role.toLowerCase().includes(query) ||
            status.includes(query)
        );
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
                        Refresh
                    </Button>
                    <Dialog open={isCreateOpen} onOpenChange={setIsCreateOpen}>
                        <DialogTrigger asChild>
                            <Button>
                                <Plus className="mr-2 h-4 w-4" />
                                <span className="hidden sm:inline">{t("users.add")}</span>
                                <span className="sm:hidden">Add</span>
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
                                    <Label htmlFor="displayName">Display Name</Label>
                                    <Input
                                        id="displayName"
                                        value={newUserDisplayName}
                                        onChange={(e) => setNewUserDisplayName(e.target.value)}
                                        placeholder="John Doe"
                                        required
                                    />
                                </div>
                                <div className="space-y-2">
                                    <Label htmlFor="email">Email</Label>
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
                                    <Label htmlFor="password">Password</Label>
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
                                    <Label htmlFor="role">Role</Label>
                                    <Select value={newUserRole} onValueChange={setNewUserRole}>
                                        <SelectTrigger>
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent>
                                            <SelectItem value="user">User</SelectItem>
                                            <SelectItem value="admin">Admin</SelectItem>
                                            <SelectItem value="partner">Partner</SelectItem>
                                            <SelectItem value="subcontractor">Subcontractor</SelectItem>
                                            <SelectItem value="customer">Customer</SelectItem>
                                        </SelectContent>
                                    </Select>
                                </div>
                                <DialogFooter>
                                    <Button type="button" variant="outline" onClick={() => setIsCreateOpen(false)}>
                                        Cancel
                                    </Button>
                                    <Button type="submit" disabled={isCreating}>
                                        {isCreating && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                                        Create User
                                    </Button>
                                </DialogFooter>
                            </form>
                        </DialogContent>
                    </Dialog>
                </div>
            </div>

            {/* Tabs & Search */}
            <Tabs defaultValue="all_users" className="w-full space-y-6">
                <div className="flex flex-col md:flex-row justify-between items-center gap-4 border-b border-border/40 pb-px">
                    <TabsList className="bg-transparent p-0 gap-6">
                        <TabsTrigger
                            value="all_users"
                            className="relative h-10 rounded-none border-b-2 border-transparent bg-transparent px-4 pb-3 pt-2 font-semibold text-muted-foreground shadow-none transition-all hover:text-foreground data-[state=active]:border-primary data-[state=active]:text-primary data-[state=active]:shadow-none"
                        >
                            All Users
                        </TabsTrigger>
                        <TabsTrigger
                            value="roles"
                            className="relative h-10 rounded-none border-b-2 border-transparent bg-transparent px-4 pb-3 pt-2 font-semibold text-muted-foreground shadow-none transition-all hover:text-foreground data-[state=active]:border-primary data-[state=active]:text-primary data-[state=active]:shadow-none"
                        >
                            <div className="flex items-center gap-2">
                                Roles
                                <Badge variant="secondary" className="h-5 px-1.5 text-[10px] font-medium text-muted-foreground">Soon</Badge>
                            </div>
                        </TabsTrigger>
                        <TabsTrigger
                            value="audit"
                            className="relative h-10 rounded-none border-b-2 border-transparent bg-transparent px-4 pb-3 pt-2 font-semibold text-muted-foreground shadow-none transition-all hover:text-foreground data-[state=active]:border-primary data-[state=active]:text-primary data-[state=active]:shadow-none"
                        >
                            <div className="flex items-center gap-2">
                                Audit Log
                                <Badge variant="secondary" className="h-5 px-1.5 text-[10px] font-medium text-muted-foreground">Soon</Badge>
                            </div>
                        </TabsTrigger>
                    </TabsList>

                    <div className="relative w-full md:w-64">
                        <Search className="absolute left-2 top-2.5 h-4 w-4 text-muted-foreground" />
                        <Input
                            placeholder="Search users..."
                            value={searchQuery}
                            onChange={(e) => setSearchQuery(e.target.value)}
                            className="pl-8 h-9"
                        />
                    </div>
                </div>

                <TabsContent value="all_users" className="mt-0">
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
                                        <TableHead className="h-11">
                                            <div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Status</div>
                                        </TableHead>
                                        <TableHead className="h-11 cursor-pointer" onClick={() => handleSort('lastSignInTime')}>
                                            <div className="flex items-center text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                                                {t("users.table.lastSignIn")} {getSortIcon('lastSignInTime')}
                                            </div>
                                        </TableHead>
                                        <TableHead className="h-11 text-right pr-6">
                                            <div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Actions</div>
                                        </TableHead>
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
                                                            <AvatarFallback className={cn("text-xs font-bold text-white shadow-inner", status === "Active" ? "bg-blue-600" : "bg-gray-500")}>
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
                                                    <Select
                                                        defaultValue={role}
                                                        onValueChange={(val) => {
                                                            setEditRoleUser(user);
                                                            setEditRoleValue(val);
                                                            handleEditRoleSpecific(user, val);
                                                        }}
                                                    >
                                                        <SelectTrigger className="h-8 w-[130px] bg-background border-input text-xs font-medium shadow-sm focus:ring-1 focus:ring-primary/20">
                                                            <SelectValue />
                                                        </SelectTrigger>
                                                        <SelectContent>
                                                            <SelectItem value="user">User</SelectItem>
                                                            <SelectItem value="admin">Admin</SelectItem>
                                                            <SelectItem value="partner">Partner</SelectItem>
                                                            <SelectItem value="subcontractor">Subcontractor</SelectItem>
                                                            <SelectItem value="customer">Customer</SelectItem>
                                                        </SelectContent>
                                                    </Select>
                                                </TableCell>
                                                <TableCell>
                                                    <div className={cn("inline-flex items-center px-2.5 py-1 rounded-full text-xs font-medium border",
                                                        status === "Active" ? "bg-green-500/10 text-green-600 border-green-200/50" : "bg-gray-100 text-gray-500 border-gray-200"
                                                    )}>
                                                        <span className={cn("w-1.5 h-1.5 rounded-full mr-1.5", status === "Active" ? "bg-green-500" : "bg-gray-400")}></span>
                                                        {status}
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
                                                <TableCell className="text-right pr-6">
                                                    <Button
                                                        variant="ghost"
                                                        size="sm"
                                                        className="h-8 px-2 text-primary hover:text-primary hover:bg-primary/10"
                                                        onClick={() => setSelectedUser(user)}
                                                    >
                                                        Manage Access
                                                    </Button>
                                                </TableCell>
                                            </TableRow>
                                        );
                                    })}
                                </TableBody>
                            </Table>
                        </div>
                    </Card>
                </TabsContent>
                <TabsContent value="roles"><div className="p-12 text-center text-muted-foreground bg-muted/20 rounded-lg border border-dashed border-border">Roles Management Coming Soon</div></TabsContent>
                <TabsContent value="audit"><div className="p-12 text-center text-muted-foreground bg-muted/20 rounded-lg border border-dashed border-border">Audit Logs Coming Soon</div></TabsContent>
            </Tabs>

            {/* Permission Details Sheet */}
            <Sheet open={!!selectedUser} onOpenChange={(open) => !open && setSelectedUser(null)}>
                <SheetContent className="w-[400px] sm:w-[540px] border-l border-border bg-card p-0 flex flex-col gap-0">
                    <SheetHeader className="p-6 border-b border-border/40 bg-muted/10">
                        <SheetTitle className="text-xl">Permission Details</SheetTitle>
                        <SheetDescription asChild className="flex items-center gap-2 mt-2">
                            <div>
                                <span className="text-muted-foreground">Configuring access for:</span>
                                <Badge variant="outline" className="font-medium text-foreground bg-background">{selectedUser?.email}</Badge>
                            </div>
                        </SheetDescription>
                        <div className="mt-4 flex items-center gap-2 text-sm">
                            <span className="text-muted-foreground">Current Role:</span>
                            <Badge className={cn("capitalize shadow-none",
                                selectedUser?.customClaims?.role === 'admin' ? "bg-green-600" : "bg-secondary text-secondary-foreground"
                            )}>
                                {selectedUser?.customClaims?.role || "User"}
                            </Badge>
                        </div>
                        <div className="mt-4 flex items-center justify-between">
                            <div className="flex items-center gap-2 text-sm">
                                <span className="text-muted-foreground">Account Status:</span>
                                <Badge variant="outline" className={cn("gap-1", selectedUser?.disabled ? "text-destructive border-destructive/50 bg-destructive/10" : "text-green-600 border-green-200 bg-green-50")}>
                                    <span className={cn("h-1.5 w-1.5 rounded-full", selectedUser?.disabled ? "bg-destructive" : "bg-green-600")} />
                                    {selectedUser?.disabled ? "Disabled" : "Active"}
                                </Badge>
                            </div>
                            <div className="flex items-center gap-2">
                                <Label htmlFor="user-status" className="text-xs text-muted-foreground">
                                    {selectedUser?.disabled ? "Enable User" : "Disable User"}
                                </Label>
                                <Switch
                                    id="user-status"
                                    checked={!selectedUser?.disabled}
                                    onCheckedChange={(checked) => {
                                        // TODO: Implement backend logic
                                        if (selectedUser) {
                                            setSelectedUser({ ...selectedUser, disabled: !checked });
                                        }
                                    }}
                                />
                            </div>
                        </div>
                    </SheetHeader>

                    <div className="flex-1 overflow-y-auto p-6">
                        <div className="space-y-8">
                            <section>
                                <div className="flex items-center gap-2 mb-4">
                                    <div className="h-8 w-8 rounded-lg bg-blue-100 dark:bg-blue-900/30 flex items-center justify-center text-blue-600">
                                        <Truck className="h-4 w-4" />
                                    </div>
                                    <h4 className="font-semibold text-foreground">Fleet Management</h4>
                                </div>
                                <div className="space-y-3">
                                    <div className="flex items-center justify-between p-4 rounded-xl border border-border/60 bg-card hover:bg-muted/20 transition-colors shadow-sm">
                                        <div className="space-y-1">
                                            <Label className="text-base font-medium cursor-pointer">View Trucks</Label>
                                            <p className="text-xs text-muted-foreground">Allow viewing fleet status and vehicle details</p>
                                        </div>
                                        <Switch defaultChecked={true} className="data-[state=checked]:bg-blue-600" />
                                    </div>
                                    <div className="flex items-center justify-between p-4 rounded-xl border border-border/60 bg-card hover:bg-muted/20 transition-colors shadow-sm">
                                        <div className="space-y-1">
                                            <Label className="text-base font-medium cursor-pointer">Edit Trucks</Label>
                                            <p className="text-xs text-muted-foreground">Allow modifying truck properties and assignments</p>
                                        </div>
                                        <Switch defaultChecked={selectedUser?.customClaims?.role === 'admin'} className="data-[state=checked]:bg-blue-600" />
                                    </div>
                                </div>
                            </section>

                            <section>
                                <div className="flex items-center gap-2 mb-4">
                                    <div className="h-8 w-8 rounded-lg bg-orange-100 dark:bg-orange-900/30 flex items-center justify-center text-orange-600">
                                        <Box className="h-4 w-4" />
                                    </div>
                                    <h4 className="font-semibold text-foreground">Order Tracking</h4>
                                </div>
                                <div className="space-y-3">
                                    <div className="flex items-center justify-between p-4 rounded-xl border border-border/60 bg-card hover:bg-muted/20 transition-colors shadow-sm">
                                        <div className="space-y-1">
                                            <Label className="text-base font-medium cursor-pointer">Create Shipments</Label>
                                            <p className="text-xs text-muted-foreground">Ability to create and schedule new orders</p>
                                        </div>
                                        <Switch defaultChecked={true} className="data-[state=checked]:bg-blue-600" />
                                    </div>
                                    <div className="flex items-center justify-between p-4 rounded-xl border border-border/60 bg-card hover:bg-muted/20 transition-colors shadow-sm">
                                        <div className="space-y-1">
                                            <Label className="text-base font-medium cursor-pointer">Delete Shipments</Label>
                                            <p className="text-xs text-muted-foreground">Permit deletion of existing orders</p>
                                        </div>
                                        <Switch defaultChecked={selectedUser?.customClaims?.role === 'admin'} className="data-[state=checked]:bg-blue-600" />
                                    </div>
                                </div>
                            </section>

                            <section>
                                <div className="flex items-center gap-2 mb-4">
                                    <div className="h-8 w-8 rounded-lg bg-slate-100 dark:bg-slate-800 flex items-center justify-center text-slate-600">
                                        <Settings className="h-4 w-4" />
                                    </div>
                                    <h4 className="font-semibold text-foreground">System Settings</h4>
                                </div>
                                <div className="space-y-3">
                                    <div className="flex items-center justify-between p-4 rounded-xl border border-border/60 bg-card hover:bg-muted/20 transition-colors shadow-sm">
                                        <div className="space-y-1">
                                            <Label className="text-base font-medium cursor-pointer">Manage Users</Label>
                                            <p className="text-xs text-muted-foreground">Full access to user management controls</p>
                                        </div>
                                        <Switch defaultChecked={selectedUser?.customClaims?.role === 'admin'} className="data-[state=checked]:bg-blue-600" />
                                    </div>
                                </div>
                            </section>
                        </div>
                    </div>
                    <div className="p-6 border-t border-border/40 bg-muted/10 mt-auto">
                        <div className="flex gap-3 justify-end">
                            <Button variant="outline" onClick={() => setSelectedUser(null)}>Cancel</Button>
                            <Button className="bg-blue-600 hover:bg-blue-700">Save Changes</Button>
                        </div>
                    </div>
                </SheetContent>
            </Sheet>

        </div>
    );
}

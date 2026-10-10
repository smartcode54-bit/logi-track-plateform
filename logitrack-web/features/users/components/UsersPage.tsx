"use client";

/**
 * Security Center users page, fully on Go from P0 (T18; developer-spec.md §10.13; R49, R81). The list is
 * `GET /v1/users` (keyset, 50 per page, "load more" past any count, server-side search and filters,
 * polled every 60 s while visible: Appendix E §E.5 row 14), and every action is a Go route through the
 * BFF; no Cloud Function and no Firestore read remain. Controls follow the Go capabilities of `['me']`
 * (`users:view|manage|assign_role|revoke_sessions`, `drivers:edit`, `platform:manage_platform_roles`);
 * Go authorises each call again and refuses actions on the caller.
 */
import { useEffect, useMemo, useState } from "react";
import { Edit, KeyRound, Loader2, LogOut, Mail, MoreHorizontal, Plus, RefreshCw, Search } from "lucide-react";
import { toast } from "sonner";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { TENANT_ROLES } from "@/features/auth/api/me";
import { apiErrorText } from "@/lib/apiError";
import {
    inviteUser,
    issueTemporaryPassword,
    revokeUserSessions,
    setUserDisabled,
    useInvalidateUsers,
    useUsers,
    type CreateUserResult,
    type UserDTO,
} from "../api/users";
import { isSelf, userActions } from "../utils/roles";
import { CreateUserDialog } from "./CreateUserDialog";
import { ConfirmDialog, TemporaryPasswordDialog } from "./dialogs";
import { EditUserDialog } from "./EditUserDialog";
import { UserRoleBadges, UserScopeCell, UserStatusBadge } from "./UserBadges";

/** Search is sent to Go after the user stops typing for this long. */
export const SEARCH_DEBOUNCE_MS = 300;

type Pending =
    | { kind: "disable"; user: UserDTO }
    | { kind: "revoke"; user: UserDTO }
    | { kind: "tempPassword"; user: UserDTO }
    | null;

function initials(user: UserDTO): string {
    const base = user.displayName?.trim() || user.email || "?";
    return base.substring(0, 2).toUpperCase();
}

export function UsersPage() {
    const { t, language } = useLanguage();
    const auth = useAuth();
    const me = auth?.me ?? null;
    const actions = useMemo(() => userActions(me), [me]);
    const invalidate = useInvalidateUsers();

    const [search, setSearch] = useState("");
    const [q, setQ] = useState("");
    const [role, setRole] = useState("");
    const [status, setStatus] = useState("");
    useEffect(() => {
        const id = setTimeout(() => setQ(search.trim()), SEARCH_DEBOUNCE_MS);
        return () => clearTimeout(id);
    }, [search]);

    const users = useUsers({ q, role, status }, Boolean(me));
    const rows = useMemo(() => users.data?.pages.flatMap((p) => p.data ?? []) ?? [], [users.data]);

    const [createOpen, setCreateOpen] = useState(false);
    const [editing, setEditing] = useState<UserDTO | null>(null);
    const [pending, setPending] = useState<Pending>(null);
    const [temp, setTemp] = useState<{ password: string; account: string } | null>(null);
    const [toggling, setToggling] = useState<string | null>(null);

    const created = (result: CreateUserResult) => {
        void invalidate();
        if (result.temporaryPassword) {
            setTemp({ password: result.temporaryPassword, account: result.user.email ?? result.user.displayName ?? "" });
        }
    };

    const enable = async (user: UserDTO) => {
        setToggling(user.id);
        try {
            await setUserDisabled(user.id, false);
            toast.success(t("users.disable.enabledSuccess"));
            void invalidate();
        } catch (err) {
            toast.error(apiErrorText(err, t));
        } finally {
            setToggling(null);
        }
    };

    const confirmPending = async (reason: string) => {
        if (!pending) return;
        const { user } = pending;
        try {
            if (pending.kind === "disable") {
                await setUserDisabled(user.id, true, reason);
                toast.success(t("users.disable.disabledSuccess"));
            } else if (pending.kind === "revoke") {
                await revokeUserSessions(user.id);
                toast.success(t("users.revokeSessionsSuccess"));
            } else {
                const result = await issueTemporaryPassword(user.id);
                setTemp({ password: result.temporaryPassword, account: user.email ?? user.displayName ?? "" });
            }
            setPending(null);
            void invalidate();
        } catch (err) {
            toast.error(apiErrorText(err, t));
        }
    };

    const invite = async (user: UserDTO) => {
        try {
            await inviteUser(user.id);
            toast.success(t("users.toast.invited"));
        } catch (err) {
            toast.error(apiErrorText(err, t));
        }
    };

    const showActions = actions.manage || actions.assignRole || actions.revokeSessions || actions.platformRoles;
    const lastSignIn = (iso: string | null) => {
        if (!iso) return { date: t("users.never"), time: "-" };
        const d = new Date(iso);
        const locale = language === "th" ? "th-TH" : "en-GB";
        return {
            date: d.toLocaleDateString(locale, { month: "short", day: "numeric", year: "numeric" }),
            time: d.toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit" }),
        };
    };

    return (
        <div className="p-6 space-y-6">
            <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("users.title")}</h1>
                    <p className="text-muted-foreground mt-2">{t("users.subtitle")}</p>
                </div>
                <div className="flex flex-wrap gap-2">
                    <Button variant="outline" className="gap-2" onClick={() => void users.refetch()} disabled={users.isFetching}>
                        {users.isFetching ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
                        {t("users.refresh")}
                    </Button>
                    {actions.manage ? (
                        <Button onClick={() => setCreateOpen(true)}>
                            <Plus className="mr-2 h-4 w-4" />
                            <span className="hidden sm:inline">{t("users.add")}</span>
                            <span className="sm:hidden">{t("users.addShort")}</span>
                        </Button>
                    ) : null}
                </div>
            </div>

            <div className="flex flex-wrap items-center gap-3 border-b border-border/40 pb-3">
                <Select value={role || "all"} onValueChange={(v) => setRole(v === "all" ? "" : v)}>
                    <SelectTrigger className="w-[180px] h-9" aria-label={t("users.filter.role")}>
                        <SelectValue placeholder={t("users.filter.role")} />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="all">{t("users.filter.roleAll")}</SelectItem>
                        {TENANT_ROLES.map((r) => (
                            <SelectItem key={r} value={r}>
                                {t(`users.tenantRole.${r}`)}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
                <Select value={status || "all"} onValueChange={(v) => setStatus(v === "all" ? "" : v)}>
                    <SelectTrigger className="w-[160px] h-9" aria-label={t("users.filter.status")}>
                        <SelectValue placeholder={t("users.filter.status")} />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="all">{t("users.filter.statusAll")}</SelectItem>
                        <SelectItem value="active">{t("users.accountStatus.active")}</SelectItem>
                        <SelectItem value="disabled">{t("users.accountStatus.disabled")}</SelectItem>
                        <SelectItem value="reset_required">{t("users.accountStatus.reset_required")}</SelectItem>
                    </SelectContent>
                </Select>
                <div className="relative flex-1 min-w-[200px] max-w-[320px]">
                    <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                    <Input placeholder={t("users.searchPlaceholder")} value={search} onChange={(e) => setSearch(e.target.value)} className="pl-9 h-9" />
                </div>
            </div>

            <Card className="bg-card border-border shadow-sm">
                <div className="rounded-md overflow-hidden">
                    <Table>
                        <TableHeader className="bg-muted/40">
                            <TableRow className="hover:bg-transparent border-none">
                                <TableHead className="h-11 pl-6 text-xs font-semibold uppercase tracking-wider">{t("users.table.user")}</TableHead>
                                <TableHead className="h-11 text-xs font-semibold uppercase tracking-wider">{t("users.table.role")}</TableHead>
                                <TableHead className="h-11 min-w-[180px] text-xs font-semibold uppercase tracking-wider">{t("users.scope")}</TableHead>
                                <TableHead className="h-11 text-xs font-semibold uppercase tracking-wider">{t("users.table.status")}</TableHead>
                                <TableHead className="h-11 text-xs font-semibold uppercase tracking-wider">{t("users.table.lastSignIn")}</TableHead>
                                {actions.manage ? <TableHead className="h-11 w-[100px] text-center text-xs font-semibold uppercase tracking-wider">{t("users.table.enabled")}</TableHead> : null}
                                {showActions ? <TableHead className="h-11 w-[52px] text-right text-xs font-semibold uppercase tracking-wider">{t("users.table.actions")}</TableHead> : null}
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {users.isPending ? (
                                <TableRow>
                                    <TableCell colSpan={7} className="py-10 text-center">
                                        <Loader2 className="mx-auto h-6 w-6 animate-spin text-muted-foreground" />
                                    </TableCell>
                                </TableRow>
                            ) : users.isError ? (
                                <TableRow>
                                    <TableCell colSpan={7} className="py-10 text-center text-sm text-destructive" role="alert">
                                        {apiErrorText(users.error, t)}
                                    </TableCell>
                                </TableRow>
                            ) : rows.length === 0 ? (
                                <TableRow>
                                    <TableCell colSpan={7} className="py-10 text-center text-sm text-muted-foreground">
                                        {t("users.empty")}
                                    </TableCell>
                                </TableRow>
                            ) : (
                                rows.map((user) => {
                                    const self = isSelf(me, user);
                                    const when = lastSignIn(user.lastLoginAt);
                                    return (
                                        <TableRow key={user.id} className="hover:bg-muted/30 border-b border-border/50" data-testid="user-row">
                                            <TableCell className="pl-6 py-4">
                                                <div className="flex items-center gap-3">
                                                    <Avatar className="h-9 w-9 border border-border shadow-sm">
                                                        <AvatarImage src={user.photoUrl ?? undefined} alt={user.displayName ?? ""} />
                                                        <AvatarFallback className="text-xs font-bold text-white bg-blue-600">{initials(user)}</AvatarFallback>
                                                    </Avatar>
                                                    <div className="flex flex-col">
                                                        <span className="font-semibold text-sm text-foreground">{user.displayName || t("users.noName")}</span>
                                                        <span className="text-xs text-muted-foreground">{user.email}</span>
                                                    </div>
                                                </div>
                                            </TableCell>
                                            <TableCell>
                                                <UserRoleBadges user={user} activeTenantId={me?.tenant?.id} />
                                            </TableCell>
                                            <TableCell className="align-top py-3">
                                                <UserScopeCell user={user} />
                                            </TableCell>
                                            <TableCell>
                                                <UserStatusBadge user={user} />
                                            </TableCell>
                                            <TableCell>
                                                <div className="text-sm font-medium text-foreground/80">{when.date}</div>
                                                <div className="text-[10px] text-muted-foreground uppercase tracking-wide">{when.time}</div>
                                            </TableCell>
                                            {actions.manage ? (
                                                <TableCell className="text-center py-2">
                                                    <div className="flex items-center justify-center">
                                                        <Switch
                                                            checked={user.status === "active"}
                                                            onCheckedChange={(checked) => {
                                                                if (checked) void enable(user);
                                                                else setPending({ kind: "disable", user });
                                                            }}
                                                            disabled={self || toggling === user.id || user.status === "deleted"}
                                                            aria-label={t("users.disable.toggle")}
                                                            title={self ? t("users.disable.cannotDisableSelf") : undefined}
                                                        />
                                                    </div>
                                                </TableCell>
                                            ) : null}
                                            {showActions ? (
                                                <TableCell className="text-right py-2">
                                                    <DropdownMenu>
                                                        <DropdownMenuTrigger asChild>
                                                            <Button variant="ghost" size="icon" className="h-8 w-8" aria-label={t("users.table.actions")}>
                                                                <MoreHorizontal className="h-4 w-4" />
                                                            </Button>
                                                        </DropdownMenuTrigger>
                                                        <DropdownMenuContent align="end">
                                                            {actions.assignRole || actions.linkDriver || actions.platformRoles ? (
                                                                <DropdownMenuItem onSelect={() => setEditing(user)}>
                                                                    <Edit className="mr-2 h-4 w-4" />
                                                                    {t("users.editRole")}
                                                                </DropdownMenuItem>
                                                            ) : null}
                                                            {actions.manage ? (
                                                                <DropdownMenuItem disabled={self} onSelect={() => setPending({ kind: "tempPassword", user })}>
                                                                    <KeyRound className="mr-2 h-4 w-4" />
                                                                    {t("users.tempPassword.action")}
                                                                </DropdownMenuItem>
                                                            ) : null}
                                                            {actions.manage && user.email ? (
                                                                <DropdownMenuItem disabled={self} onSelect={() => void invite(user)}>
                                                                    <Mail className="mr-2 h-4 w-4" />
                                                                    {t("users.invite.action")}
                                                                </DropdownMenuItem>
                                                            ) : null}
                                                            {actions.revokeSessions ? (
                                                                <DropdownMenuItem
                                                                    disabled={self}
                                                                    title={self ? t("users.revokeSessionsSelf") : undefined}
                                                                    onSelect={() => setPending({ kind: "revoke", user })}
                                                                >
                                                                    <LogOut className="mr-2 h-4 w-4" />
                                                                    {t("users.revokeSessionsShort")}
                                                                </DropdownMenuItem>
                                                            ) : null}
                                                        </DropdownMenuContent>
                                                    </DropdownMenu>
                                                </TableCell>
                                            ) : null}
                                        </TableRow>
                                    );
                                })
                            )}
                        </TableBody>
                    </Table>
                </div>
            </Card>

            <div className="flex items-center justify-between text-sm text-muted-foreground">
                <span data-testid="users-count">{t("users.shown", { count: rows.length })}</span>
                {users.hasNextPage ? (
                    <Button variant="outline" onClick={() => void users.fetchNextPage()} disabled={users.isFetchingNextPage}>
                        {users.isFetchingNextPage ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {t("users.loadMore")}
                    </Button>
                ) : null}
            </div>

            {actions.manage ? <CreateUserDialog open={createOpen} onOpenChange={setCreateOpen} me={me} actions={actions} onCreated={created} /> : null}
            {editing ? (
                <EditUserDialog
                    user={rows.find((u) => u.id === editing.id) ?? editing}
                    me={me}
                    actions={actions}
                    onOpenChange={(open) => !open && setEditing(null)}
                    onSaved={() => void invalidate()}
                />
            ) : null}
            <ConfirmDialog
                open={pending !== null}
                title={
                    pending?.kind === "disable"
                        ? t("users.disable.confirmTitle")
                        : pending?.kind === "revoke"
                          ? t("users.revokeSessionsTitle")
                          : t("users.tempPassword.confirmTitle")
                }
                description={
                    <>
                        <span>
                            {pending?.kind === "disable"
                                ? t("users.disable.confirmDesc")
                                : pending?.kind === "revoke"
                                  ? t("users.revokeSessionsDesc")
                                  : t("users.tempPassword.confirmDesc")}
                        </span>
                        {pending ? (
                            <span className="block font-medium text-foreground">
                                {pending.user.displayName} ({pending.user.email})
                            </span>
                        ) : null}
                    </>
                }
                confirmLabel={
                    pending?.kind === "disable"
                        ? t("users.disable.confirm")
                        : pending?.kind === "revoke"
                          ? t("users.revokeSessionsConfirm")
                          : t("users.tempPassword.confirm")
                }
                destructive={pending?.kind !== "tempPassword"}
                withReason={pending?.kind === "disable"}
                onConfirm={confirmPending}
                onCancel={() => setPending(null)}
            />
            <TemporaryPasswordDialog password={temp?.password ?? null} account={temp?.account} onClose={() => setTemp(null)} />
        </div>
    );
}

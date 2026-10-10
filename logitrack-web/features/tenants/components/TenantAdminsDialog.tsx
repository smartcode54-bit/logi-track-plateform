"use client";

/**
 * The `tenant_admin`s of one tenant (T18 owner addition; Appendix B §B.2.4): listed with
 * `GET /v1/tenants/{id}/members?role=tenant_admin`, removed with `DELETE /v1/tenants/{id}/members/{userId}`,
 * assigned from an existing user (`GET /v1/users?q=` then `PUT /v1/tenants/{id}/members/{userId}
 * {role: "tenant_admin"}`) or created (`POST /v1/users {role: "tenant_admin", tenantId}`, temporary
 * password shown once). The new admin signs in and sees only that tenant (RLS; its only membership).
 * The two reads cross tenants, so a platform principal sends `X-Act-On-Tenant` (`<id>` for the
 * members, `*` for the user search; Appendix C §C.3.9); the writes need no header.
 */
import { useEffect, useState } from "react";
import { Loader2, Plus, Search, Trash2, UserPlus } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { isPlatformPrincipal, tenantName } from "@/features/auth/api/me";
import { CreateUserDialog } from "@/features/users/components/CreateUserDialog";
import { TemporaryPasswordDialog } from "@/features/users/components/dialogs";
import { removeMember, setMemberRole, useInvalidateUsers, useUsers, usersReach, type CreateUserResult } from "@/features/users/api/users";
import { userActions } from "@/features/users/utils/roles";
import { apiErrorText } from "@/lib/apiError";
import { useInvalidateTenants, useTenantMembers, type TenantDTO } from "../api/tenants";

export const ADMIN_ROLE = "tenant_admin";

export function TenantAdminsDialog({ tenant, onOpenChange }: { tenant: TenantDTO | null; onOpenChange: (open: boolean) => void }) {
    const { t, language } = useLanguage();
    const auth = useAuth();
    const me = auth?.me ?? null;
    const invalidateTenants = useInvalidateTenants();
    const invalidateUsers = useInvalidateUsers();
    const admins = useTenantMembers(tenant?.id ?? null, ADMIN_ROLE, isPlatformPrincipal(me));
    const [search, setSearch] = useState("");
    const [q, setQ] = useState("");
    const [busy, setBusy] = useState<string | null>(null);
    const [createOpen, setCreateOpen] = useState(false);
    const [temp, setTemp] = useState<{ password: string; account: string } | null>(null);

    useEffect(() => {
        const id = setTimeout(() => setQ(search.trim()), 300);
        return () => clearTimeout(id);
    }, [search]);
    const candidates = useUsers({ q, role: "", status: "active" }, Boolean(tenant) && q.length >= 2, usersReach(me));

    if (!tenant) return null;
    const adminIds = new Set((admins.data ?? []).map((m) => m.user.id));
    const found = (candidates.data?.pages.flatMap((p) => p.data ?? []) ?? []).filter((u) => !adminIds.has(u.id)).slice(0, 10);

    const changed = () => {
        void invalidateTenants();
        void invalidateUsers();
    };

    const assign = async (userId: string) => {
        setBusy(userId);
        try {
            await setMemberRole(tenant.id, userId, ADMIN_ROLE);
            toast.success(t("tenants.admins.assigned"));
            setSearch("");
            changed();
        } catch (err) {
            toast.error(apiErrorText(err, t));
        } finally {
            setBusy(null);
        }
    };

    const remove = async (userId: string) => {
        setBusy(userId);
        try {
            await removeMember(tenant.id, userId);
            toast.success(t("tenants.admins.removed"));
            changed();
        } catch (err) {
            toast.error(apiErrorText(err, t));
        } finally {
            setBusy(null);
        }
    };

    const created = (result: CreateUserResult) => {
        changed();
        if (result.temporaryPassword) setTemp({ password: result.temporaryPassword, account: result.user.email ?? "" });
    };

    return (
        <>
            <Dialog open onOpenChange={onOpenChange}>
                <DialogContent className="sm:max-w-[560px] max-h-[90vh] overflow-y-auto">
                    <DialogHeader>
                        <DialogTitle>{t("tenants.admins.title")}</DialogTitle>
                        <DialogDescription>{tenantName(tenant, language)}</DialogDescription>
                    </DialogHeader>

                    <section className="space-y-2">
                        {admins.isPending ? (
                            <Loader2 className="mx-auto h-5 w-5 animate-spin text-muted-foreground" />
                        ) : admins.isError ? (
                            <p className="text-sm text-destructive" role="alert">
                                {apiErrorText(admins.error, t)}
                            </p>
                        ) : (admins.data ?? []).length === 0 ? (
                            <p className="text-sm text-muted-foreground">{t("tenants.admins.none")}</p>
                        ) : (
                            (admins.data ?? []).map((m) => (
                                <div key={m.user.id} className="flex items-center justify-between gap-2 rounded-md border px-3 py-2" data-testid="tenant-admin">
                                    <div className="flex flex-col">
                                        <span className="text-sm font-medium">{m.user.displayName || m.user.email}</span>
                                        <span className="text-xs text-muted-foreground">{m.user.email}</span>
                                    </div>
                                    <Button
                                        type="button"
                                        size="icon"
                                        variant="ghost"
                                        aria-label={t("tenants.admins.remove")}
                                        disabled={busy !== null || m.user.id === me?.id}
                                        onClick={() => void remove(m.user.id)}
                                    >
                                        {busy === m.user.id ? <Loader2 className="h-4 w-4 animate-spin" /> : <Trash2 className="h-4 w-4 text-destructive" />}
                                    </Button>
                                </div>
                            ))
                        )}
                    </section>

                    <Separator />

                    <section className="space-y-2">
                        <h3 className="text-sm font-semibold">{t("tenants.admins.assignExisting")}</h3>
                        <div className="relative">
                            <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
                            <Input value={search} onChange={(e) => setSearch(e.target.value)} placeholder={t("tenants.admins.searchUsers")} className="h-9 pl-7" />
                        </div>
                        {q.length >= 2 ? (
                            candidates.isFetching && found.length === 0 ? (
                                <Loader2 className="mx-auto h-4 w-4 animate-spin text-muted-foreground" />
                            ) : candidates.isError ? (
                                <p className="text-xs text-destructive">{apiErrorText(candidates.error, t)}</p>
                            ) : found.length === 0 ? (
                                <p className="text-xs text-muted-foreground">{t("tenants.admins.noUsers")}</p>
                            ) : (
                                found.map((u) => (
                                    <div key={u.id} className="flex items-center justify-between gap-2 px-1">
                                        <span className="text-sm">
                                            {u.displayName || u.email}
                                            <span className="ml-1 text-xs text-muted-foreground">{u.email}</span>
                                        </span>
                                        <Button type="button" size="sm" variant="outline" disabled={busy !== null} onClick={() => void assign(u.id)}>
                                            {busy === u.id ? <Loader2 className="h-3 w-3 animate-spin" /> : <Plus className="mr-1 h-3 w-3" />}
                                            {t("tenants.admins.assign")}
                                        </Button>
                                    </div>
                                ))
                            )
                        ) : null}
                    </section>

                    <Separator />

                    <Button type="button" variant="secondary" onClick={() => setCreateOpen(true)} className="w-full">
                        <UserPlus className="mr-2 h-4 w-4" />
                        {t("tenants.admins.createNew")}
                    </Button>

                    <DialogFooter>
                        <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
                            {t("users.close")}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
            <CreateUserDialog
                open={createOpen}
                onOpenChange={setCreateOpen}
                me={me}
                actions={userActions(me)}
                onCreated={created}
                fixedTenantId={tenant.id}
                fixedRole="tenant_admin"
            />
            <TemporaryPasswordDialog password={temp?.password ?? null} account={temp?.account} onClose={() => setTemp(null)} />
        </>
    );
}

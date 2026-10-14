"use client";

/**
 * The waitlist over Go (T54R, issue #74; Appendix E §E.2.4, §E.5 row 18; the only source since TW7):
 * `GET /v1/waitlist` in keyset pages of 50 (`['waitlist']`, 60 s poll while
 * one page is loaded, `meta.total` for the count) instead of a listener on the whole collection, and
 * `DELETE /v1/waitlist/{id}` behind a confirmation, which makes the list and the sidebar's count stale.
 * The route needs `waitlist:view` (a steward key, R60); `proxy.ts` refuses the page to anyone else.
 */
import { useState } from "react";
import { format } from "date-fns";
import { Loader2, Mail, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { apiErrorText } from "@/lib/apiError";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useDeleteWaitlistEntry, useWaitlist, waitlistEntries, waitlistTotal, type WaitlistEntryDTO } from "../api/waitlist";

function joinedAt(createdAt: string): string {
    const d = new Date(createdAt);
    return Number.isNaN(d.getTime()) ? "—" : format(d, "PPP p");
}

export function WaitlistPage() {
    const { t } = useLanguage();
    const list = useWaitlist();
    const remove = useDeleteWaitlistEntry();
    const [removing, setRemoving] = useState<WaitlistEntryDTO | null>(null);

    const entries = waitlistEntries(list.data?.pages);
    const total = waitlistTotal(list.data?.pages) ?? entries.length;

    const confirmRemove = async () => {
        if (!removing) return;
        try {
            await remove.mutateAsync({ id: removing.id });
            toast.success(t("waitlist.entryRemoved"));
            setRemoving(null);
        } catch (error) {
            toast.error(`${t("waitlist.deleteFailed")}: ${apiErrorText(error, (k, f) => t(k, f))}`);
        }
    };

    if (list.isPending) {
        return (
            <div className="flex h-[50vh] items-center justify-center" role="status" aria-busy="true">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
            </div>
        );
    }

    return (
        <div className="p-6 space-y-6">
            <div className="flex items-start justify-between gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("nav.waitlist")}</h1>
                    <p className="text-muted-foreground mt-2">{t("waitlist.description")}</p>
                </div>
                <Button variant="outline" size="sm" onClick={() => void list.refetch()} disabled={list.isFetching}>
                    <RefreshCw className={`mr-2 h-4 w-4 ${list.isFetching ? "animate-spin" : ""}`} />
                    {t("waitlist.refresh")}
                </Button>
            </div>

            {list.isError && (
                <p className="rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm text-destructive" role="alert">
                    {t("waitlist.fetchFailed")}: {apiErrorText(list.error, (k, f) => t(k, f))}
                </p>
            )}

            <Card>
                <CardHeader>
                    <CardTitle className="flex items-center gap-2">
                        <Mail className="h-5 w-5" />
                        {t("waitlist.requests")} (<span data-testid="waitlist-total">{total.toLocaleString()}</span>)
                    </CardTitle>
                    <CardDescription>{t("waitlist.requestsDesc")}</CardDescription>
                </CardHeader>
                <CardContent>
                    <div className="rounded-md border">
                        <Table>
                            <TableHeader>
                                <TableRow>
                                    <TableHead>{t("waitlist.email")}</TableHead>
                                    <TableHead>{t("waitlist.name")}</TableHead>
                                    <TableHead>{t("waitlist.phone")}</TableHead>
                                    <TableHead>{t("waitlist.joinedAt")}</TableHead>
                                    <TableHead className="text-right">{t("waitlist.actions")}</TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {entries.length === 0 ? (
                                    <TableRow>
                                        <TableCell colSpan={5} className="text-center h-24 text-muted-foreground">
                                            {t("waitlist.noRequests")}
                                        </TableCell>
                                    </TableRow>
                                ) : (
                                    entries.map((entry) => (
                                        <TableRow key={entry.id}>
                                            <TableCell className="font-medium">{entry.email}</TableCell>
                                            <TableCell>{entry.name}</TableCell>
                                            <TableCell className="tabular-nums">{entry.phone ? `${entry.countryCode} ${entry.phone}` : "—"}</TableCell>
                                            <TableCell>{joinedAt(entry.createdAt)}</TableCell>
                                            <TableCell className="text-right">
                                                <Button
                                                    variant="ghost"
                                                    size="icon"
                                                    aria-label={`${t("waitlist.remove")} ${entry.email}`}
                                                    onClick={() => setRemoving(entry)}
                                                    className="text-destructive hover:text-destructive/90 hover:bg-destructive/10"
                                                >
                                                    <Trash2 className="h-4 w-4" />
                                                </Button>
                                            </TableCell>
                                        </TableRow>
                                    ))
                                )}
                            </TableBody>
                        </Table>
                    </div>
                    {entries.length > 0 && (
                        <div className="mt-4 flex items-center justify-between gap-4 text-sm text-muted-foreground">
                            <span>{t("waitlist.showing", { shown: entries.length.toLocaleString(), total: total.toLocaleString() })}</span>
                            {list.hasNextPage && (
                                <Button variant="outline" size="sm" onClick={() => void list.fetchNextPage()} disabled={list.isFetchingNextPage}>
                                    {list.isFetchingNextPage && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                                    {t("waitlist.loadMore")}
                                </Button>
                            )}
                        </div>
                    )}
                </CardContent>
            </Card>

            <Dialog open={removing !== null} onOpenChange={(open) => !open && !remove.isPending && setRemoving(null)}>
                <DialogContent>
                    <DialogHeader>
                        <DialogTitle>{t("waitlist.removeTitle")}</DialogTitle>
                        <DialogDescription>{t("waitlist.removeDesc", { email: removing?.email ?? "" })}</DialogDescription>
                    </DialogHeader>
                    <DialogFooter>
                        <Button variant="outline" onClick={() => setRemoving(null)} disabled={remove.isPending}>
                            {t("waitlist.cancel")}
                        </Button>
                        <Button variant="destructive" onClick={() => void confirmRemove()} disabled={remove.isPending}>
                            {remove.isPending && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                            {t("waitlist.remove")}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
}

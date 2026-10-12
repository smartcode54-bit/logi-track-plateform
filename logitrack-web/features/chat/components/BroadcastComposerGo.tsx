"use client";

/**
 * The broadcast composer and history over Go (T50; Appendix E §E.2.1 `?view=broadcast`, §E.10 row 15). The
 * audience of a group is resolved by Go under the caller's reach when the broadcast is sent (no list of every
 * driver read in the browser, `BroadcastComposer.tsx:58`); single drivers are picked through the server
 * search `GET /v1/drivers?fields=minimal&q` and sent as their logins (`recipientUserIds`). The history is
 * `['broadcasts']` in keyset pages (no cap of 50), with Go's read counts; "withdraw" voids a broadcast.
 */
import { useState } from "react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, Check, Loader2, Megaphone, Search, Send, X } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { SEARCH_DEBOUNCE_MS, useDebouncedValue } from "@/hooks/useDebouncedValue";
import { useCan, useMe } from "@/features/auth/api/useMe";
import { driverPicksQuery } from "@/features/accounting/api/pickers.go";
import { fieldViolations } from "@/features/auth/utils/fieldErrors";
import { apiErrorText } from "@/lib/apiError";
import { cn } from "@/lib/utils";
import { displayBroadcastTitle } from "@/lib/broadcastDisplay";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";

import {
    MAX_BROADCAST_MESSAGE,
    MAX_BROADCAST_TITLE,
    broadcastRows,
    broadcastsQuery,
    useSendBroadcast,
    useVoidBroadcast,
    type BroadcastGroup,
    type BroadcastRecipients,
} from "../api/broadcasts.go";
import { chatDriverQuery, driverUserId } from "../api/chats.go";
import { BroadcastVoidConfirmDialog } from "./BroadcastVoidConfirmDialog";
import { dateTimeLabel } from "./chatFormat";


type Mode = BroadcastGroup | "selected";

interface Picked {
    driverId: string;
    userId: string;
    name: string;
}

const isSteward = (me: { steward?: boolean } | null) => me?.steward === true;

/** The text of a refused send: Go's reason for the recipients when it named one, else the error's catalogue text. */
function sendErrorText(error: unknown, t: (key: string, fallback?: string) => string): string {
    for (const v of fieldViolations(error)) {
        if (v.reason === "unsupported_until_p7b") return t("chat.broadcast.selectedUnsupported");
        if (v.reason === "no_recipients") return t("chat.broadcast.noRecipients");
        if (v.field.startsWith("recipientUserIds") && v.reason === "not_found") return t("chat.broadcast.recipientNotFound");
    }
    return apiErrorText(error, t);
}


function DriverPicker({ picked, onAdd }: { picked: readonly Picked[]; onAdd: (p: Picked) => void }) {
    const { t } = useLanguage();
    const client = useQueryClient();
    const [q, setQ] = useState("");
    const [resolving, setResolving] = useState<string | null>(null);
    const term = useDebouncedValue(q.trim(), SEARCH_DEBOUNCE_MS);
    const drivers = useQuery({ ...driverPicksQuery(term), enabled: term !== "" });
    const add = async (id: string, name: string) => {
        setResolving(id);
        try {
            // The login a broadcast addresses: the driver's detail (one request per pick, cached).
            const d = await client.fetchQuery(chatDriverQuery(id));
            const userId = driverUserId(d);
            if (!userId) {
                toast.error(t("chat.broadcast.noLogin", { name }));
                return;
            }
            onAdd({ driverId: id, userId, name });
            setQ("");
        } catch (e) {
            toast.error(apiErrorText(e, t));
        } finally {
            setResolving(null);
        }
    };
    const chosen = new Set(picked.map((p) => p.driverId));
    return (
        <div className="relative">
            <Search className="absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <Input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("chat.broadcast.searchDriver")} className="pl-8" aria-label={t("chat.broadcast.searchDriver")} />
            {q.trim() !== "" && (
                <ul className="absolute left-0 right-0 top-full z-50 mt-1 max-h-[240px] overflow-y-auto rounded-md border bg-popover shadow-md" data-testid="broadcast-driver-results">
                    {term === "" || drivers.isPending ? (
                        <li className="flex items-center gap-2 p-3 text-sm text-muted-foreground">
                            <Loader2 className="h-4 w-4 animate-spin" />
                            {t("chat.panel.searching")}
                        </li>
                    ) : drivers.isError ? (
                        <li className="p-3 text-sm text-destructive">{apiErrorText(drivers.error, t)}</li>
                    ) : (drivers.data ?? []).length === 0 ? (
                        <li className="p-3 text-sm text-muted-foreground">{t("chat.panel.noDriver")}</li>
                    ) : (
                        (drivers.data ?? []).map((d) => {
                            const name = d.displayName || d.id.slice(0, 8);
                            return (
                                <li key={d.id}>
                                    <button
                                        type="button"
                                        className="flex w-full items-center justify-between gap-2 p-3 text-left text-sm hover:bg-muted/80 disabled:opacity-60"
                                        disabled={chosen.has(d.id) || resolving !== null}
                                        onClick={() => void add(d.id, name)}
                                    >
                                        <span className="truncate font-medium">{name}</span>
                                        {resolving === d.id ? <Loader2 className="h-4 w-4 animate-spin" /> : chosen.has(d.id) ? <Check className="h-4 w-4 text-primary" /> : null}
                                    </button>
                                </li>
                            );
                        })
                    )}
                </ul>
            )}
        </div>
    );
}

function History() {
    const { t, language } = useLanguage();
    const canVoid = useCan("broadcasts:send").allowed;
    const history = useInfiniteQuery(broadcastsQuery);
    const rows = broadcastRows(history.data?.pages);
    const voiding = useVoidBroadcast();
    const [confirmId, setConfirmId] = useState<string | null>(null);
    const groupLabel = (g: string) => t(`chat.broadcast.group.${g}`, g);

    const confirmVoid = () => {
        const id = confirmId;
        if (!id) return;
        voiding.mutate(id, {
            onSuccess: () => {
                setConfirmId(null);
                toast.success(t("chat.broadcast.voided"));
            },
            onError: (e) => toast.error(apiErrorText(e, t)),
        });
    };

    return (
        <div className="mt-10 border-t pt-8">
            <h3 className="mb-4 text-base font-semibold">{t("chat.broadcast.history")}</h3>
            {history.isPending ? (
                <div className="flex justify-center py-12" role="status" aria-busy="true">
                    <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                </div>
            ) : history.isError && rows.length === 0 ? (
                <div className="space-y-2 px-4 py-8 text-center">
                    <p className="text-sm font-medium text-destructive">{t("chat.broadcast.historyFailed")}</p>
                    <p className="text-xs text-muted-foreground">{apiErrorText(history.error, t)}</p>
                </div>
            ) : rows.length === 0 ? (
                <p className="py-8 text-center text-sm text-muted-foreground">{t("chat.broadcast.historyEmpty")}</p>
            ) : (
                <div className="max-h-[50vh] overflow-auto rounded-md border">
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead className="min-w-[160px] max-w-[220px]">{t("chat.broadcast.colTitle")}</TableHead>
                                <TableHead className="min-w-[200px]">{t("chat.broadcast.colMessage")}</TableHead>
                                <TableHead className="w-[140px]">{t("chat.broadcast.colSender")}</TableHead>
                                <TableHead className="w-[160px]">{t("chat.broadcast.colSentAt")}</TableHead>
                                <TableHead className="w-[120px] text-right">{t("chat.broadcast.colRecipients")}</TableHead>
                                <TableHead className="w-[88px] text-right">{t("chat.broadcast.colRead")}</TableHead>
                                {canVoid && <TableHead className="w-[130px] text-right"> </TableHead>}
                            </TableRow>
                        </TableHeader>
                        <TableBody data-testid="broadcast-history">
                            {rows.map((b) => (
                                <TableRow key={b.id}>
                                    <TableCell className="py-3 align-top">
                                        <div className="max-w-[220px] whitespace-pre-wrap break-words text-sm font-medium">{displayBroadcastTitle(b.title ?? undefined)}</div>
                                    </TableCell>
                                    <TableCell className="py-3 align-top">
                                        <div className="max-w-md whitespace-pre-wrap break-words text-sm">{b.messageText || "—"}</div>
                                    </TableCell>
                                    <TableCell className="py-3 align-top text-sm text-muted-foreground">{b.createdByName || "—"}</TableCell>
                                    <TableCell className="py-3 align-top text-sm text-muted-foreground">{dateTimeLabel(b.sentAt, language)}</TableCell>
                                    <TableCell className="py-3 text-right align-top text-sm">
                                        {b.recipientCount} <span className="text-muted-foreground">({groupLabel(b.recipientGroup)})</span>
                                    </TableCell>
                                    <TableCell className="py-3 text-right align-top text-sm tabular-nums">{b.readCount}</TableCell>
                                    {canVoid && (
                                        <TableCell className="py-2 text-right align-top">
                                            <Button
                                                type="button"
                                                variant="outline"
                                                size="sm"
                                                className="border-destructive/40 text-destructive hover:bg-destructive/10"
                                                disabled={voiding.isPending && voiding.variables === b.id}
                                                onClick={() => setConfirmId(b.id)}
                                            >
                                                <Ban className="mr-1 h-4 w-4" />
                                                {t("chat.broadcast.void")}
                                            </Button>
                                        </TableCell>
                                    )}
                                </TableRow>
                            ))}
                        </TableBody>
                    </Table>
                    {history.hasNextPage && (
                        <div className="flex justify-center border-t p-3">
                            <Button variant="outline" size="sm" onClick={() => void history.fetchNextPage()} disabled={history.isFetchingNextPage}>
                                {history.isFetchingNextPage ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                                {t("chat.panel.loadMore")}
                            </Button>
                        </div>
                    )}
                </div>
            )}
            <BroadcastVoidConfirmDialog
                open={confirmId !== null}
                onOpenChange={(open) => {
                    if (!open && !voiding.isPending) setConfirmId(null);
                }}
                onConfirm={confirmVoid}
                loading={voiding.isPending}
            />
        </div>
    );
}

export function BroadcastComposerGo() {
    const { t } = useLanguage();
    const canSend = useCan("broadcasts:send").allowed;
    const { data: steward = false } = useMe(isSteward);
    const send = useSendBroadcast();
    const [mode, setMode] = useState<Mode>("all_driver");
    const [picked, setPicked] = useState<Picked[]>([]);
    const [title, setTitle] = useState("");
    const [message, setMessage] = useState("");
    const [showValidation, setShowValidation] = useState(false);
    const [sentCount, setSentCount] = useState<number | null>(null);

    const modes: Mode[] = ["all_driver", "own_fleet", "subcontractor", "selected", ...(steward ? (["all_tenants"] as const) : [])];
    const titleOk = title.trim() !== "";
    const messageOk = message.trim() !== "";
    const recipientsOk = mode !== "selected" || picked.length > 0;

    const submit = () => {
        if (send.isPending) return;
        if (!titleOk || !messageOk) {
            setShowValidation(true);
            return;
        }
        if (!recipientsOk) return;
        setShowValidation(false);
        setSentCount(null);
        const recipients: BroadcastRecipients = mode === "selected" ? { userIds: picked.map((p) => p.userId) } : { group: mode };
        send.mutate(
            { recipients, title, messageText: message },
            {
                onSuccess: (b) => {
                    setSentCount(b.recipientCount);
                    setTitle("");
                    setMessage("");
                    setPicked([]);
                    toast.success(t("chat.broadcast.sent", { count: b.recipientCount }));
                },
            }
        );
    };

    return (
        <div className="mx-auto flex w-full max-w-4xl flex-1 flex-col overflow-y-auto p-6">
            <div className="mb-6 flex items-center gap-2">
                <Megaphone className="h-6 w-6 text-primary" />
                <div>
                    <h2 className="text-lg font-semibold">{t("chat.broadcast.title")}</h2>
                    <p className="text-sm text-muted-foreground">{t("chat.broadcast.subtitle")}</p>
                </div>
            </div>

            {canSend && (
                <form
                    className="space-y-4"
                    noValidate
                    onSubmit={(e) => {
                        e.preventDefault();
                        submit();
                    }}
                >
                    <div>
                        <p className="mb-2 text-sm font-medium text-muted-foreground">{t("chat.broadcast.recipients")}</p>
                        <div className="grid grid-cols-1 gap-2 sm:grid-cols-3" role="radiogroup" aria-label={t("chat.broadcast.recipients")}>
                            {modes.map((m) => (
                                <button
                                    key={m}
                                    type="button"
                                    role="radio"
                                    aria-checked={mode === m}
                                    onClick={() => setMode(m)}
                                    className={cn(
                                        "flex flex-col items-start rounded-lg border-2 p-3 text-left transition-colors",
                                        mode === m ? "border-primary bg-primary/5" : "border-border hover:bg-muted/50"
                                    )}
                                >
                                    <span className="text-sm font-medium">{t(`chat.broadcast.group.${m}`)}</span>
                                    <span className="mt-0.5 text-xs text-muted-foreground">{t(`chat.broadcast.groupHint.${m}`)}</span>
                                    {mode === m && <Check className="mt-1 h-4 w-4 text-primary" />}
                                </button>
                            ))}
                        </div>
                        {mode === "selected" && (
                            <div className="mt-3 space-y-2">
                                <DriverPicker picked={picked} onAdd={(p) => setPicked((prev) => (prev.some((x) => x.driverId === p.driverId) ? prev : [...prev, p]))} />
                                <div className="flex flex-wrap gap-2" data-testid="broadcast-picked">
                                    {picked.map((p) => (
                                        <Badge key={p.driverId} variant="secondary" className="gap-1">
                                            {p.name}
                                            <button
                                                type="button"
                                                aria-label={t("chat.broadcast.removeRecipient", { name: p.name })}
                                                onClick={() => setPicked((prev) => prev.filter((x) => x.driverId !== p.driverId))}
                                            >
                                                <X className="h-3 w-3" />
                                            </button>
                                        </Badge>
                                    ))}
                                </div>
                                {picked.length === 0 && <p className="text-sm text-amber-600 dark:text-amber-500">{t("chat.broadcast.pickDrivers")}</p>}
                            </div>
                        )}
                    </div>

                    <div>
                        <label htmlFor="broadcast-title" className="mb-2 block text-sm font-medium">
                            {t("chat.broadcast.subject")} <span className="text-destructive">*</span>
                        </label>
                        <Input
                            id="broadcast-title"
                            placeholder={t("chat.broadcast.subjectPlaceholder")}
                            value={title}
                            onChange={(e) => setTitle(e.target.value)}
                            disabled={send.isPending}
                            maxLength={MAX_BROADCAST_TITLE}
                            aria-invalid={showValidation && !titleOk}
                            className={cn(showValidation && !titleOk && "border-destructive focus-visible:ring-destructive/40")}
                        />
                        {showValidation && !titleOk && <p className="mt-1.5 text-sm text-destructive">{t("chat.broadcast.subjectRequired")}</p>}
                    </div>

                    <div>
                        <label htmlFor="broadcast-message" className="mb-2 block text-sm font-medium">
                            {t("chat.broadcast.body")} <span className="text-destructive">*</span>
                        </label>
                        <Textarea
                            id="broadcast-message"
                            placeholder={t("chat.broadcast.bodyPlaceholder")}
                            value={message}
                            onChange={(e) => setMessage(e.target.value)}
                            disabled={send.isPending}
                            rows={5}
                            maxLength={MAX_BROADCAST_MESSAGE}
                            aria-invalid={showValidation && !messageOk}
                            className={cn("resize-none", showValidation && !messageOk && "border-destructive focus-visible:ring-destructive/40")}
                        />
                        {showValidation && !messageOk && <p className="mt-1.5 text-sm text-destructive">{t("chat.broadcast.bodyRequired")}</p>}
                    </div>

                    {send.isError && (
                        <p className="text-sm text-destructive" role="alert">
                            {sendErrorText(send.error, t)}
                        </p>
                    )}
                    {sentCount !== null && <p className="text-sm text-green-600 dark:text-green-400">{t("chat.broadcast.sent", { count: sentCount })}</p>}

                    <div className="flex items-center justify-between pt-2">
                        <span className="text-sm text-muted-foreground">
                            {mode === "selected" ? t("chat.broadcast.selectedCount", { count: picked.length }) : t("chat.broadcast.resolvedOnSend")}
                        </span>
                        <Button type="submit" disabled={send.isPending || !recipientsOk} className="gap-2">
                            {send.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Send className="h-4 w-4" />}
                            {t("chat.broadcast.send")}
                        </Button>
                    </div>
                </form>
            )}

            <History />
        </div>
    );
}

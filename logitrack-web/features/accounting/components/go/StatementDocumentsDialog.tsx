"use client";

/**
 * The stored documents of one statement (T42; `['statements','detail',id]`, `statement_documents`, R69):
 * each kind with its render state and a download of the stored file (`lib/goDownload.ts`: the Go route
 * answers 302 to a signed GET with the real file name), plus "render again" for
 * `accounting:manage_statements`. While a document renders the detail polls every 5 s if the session
 * stream is closed; `documents.rendered` refreshes it otherwise.
 */
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Download, Loader2, RefreshCw } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { useCan } from "@/features/auth/api/useMe";
import { apiErrorText } from "@/lib/apiError";
import { downloadFromGo } from "@/lib/goDownload";
import { useRealtimeStatus } from "@/lib/realtime/status";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";

import { documentPath, documentsPending, regenerateDocuments, statementQuery, type StatementDTO } from "../../api/billing.go";
import { ApiErrorAlert, CenteredSpinner, formatDateTime } from "./shared";

const DOCUMENT_ORDER = ["bundle_zip", "invoice_summary_pdf", "invoice_detail_xlsx", "receipt_pdf"];

export function StatementDocumentsDialog({ statement, onClose }: { statement: Pick<StatementDTO, "id" | "invoiceNumber">; onClose: () => void }) {
    const { t } = useLanguage();
    const streamOpen = useRealtimeStatus() === "open";
    const canManage = useCan("accounting:manage_statements").allowed;
    const detail = useQuery(statementQuery(statement.id, streamOpen));
    const [busy, setBusy] = useState<string | null>(null);
    const docs = [...(detail.data?.documents ?? [])].sort((a, b) => DOCUMENT_ORDER.indexOf(a.kind) - DOCUMENT_ORDER.indexOf(b.kind));

    const download = async (kind: string) => {
        setBusy(kind);
        try {
            await downloadFromGo(documentPath(statement.id, kind));
        } catch (e) {
            toast.error(`${t("accounting.billingResult.action.redownloadError")} ${apiErrorText(e, t)}`);
        } finally {
            setBusy(null);
        }
    };
    const regenerate = async () => {
        setBusy("regenerate");
        try {
            await regenerateDocuments(statement.id);
            toast.success(t("accounting.billingResult.documents.regenerating"));
        } catch (e) {
            toast.error(apiErrorText(e, t));
        } finally {
            setBusy(null);
        }
    };

    return (
        <Dialog open onOpenChange={(o) => !o && onClose()}>
            <DialogContent className="sm:max-w-lg">
                <DialogHeader>
                    <DialogTitle>{t("accounting.billingResult.documents.title", { invoiceNumber: statement.invoiceNumber })}</DialogTitle>
                    <DialogDescription>{t("accounting.billingResult.documents.hint")}</DialogDescription>
                </DialogHeader>
                {detail.isPending ? (
                    <CenteredSpinner />
                ) : detail.error ? (
                    <ApiErrorAlert error={detail.error} />
                ) : docs.length === 0 ? (
                    <p className="text-sm text-muted-foreground">{t("accounting.billingResult.documents.none")}</p>
                ) : (
                    <ul className="divide-y rounded-md border" data-testid="statement-documents">
                        {docs.map((d) => (
                            <li key={d.kind} className="flex items-center justify-between gap-3 px-3 py-2">
                                <div className="min-w-0">
                                    <p className="text-sm font-medium">{t(`accounting.billingResult.documents.kind.${d.kind}`, d.kind)}</p>
                                    <p className="text-xs text-muted-foreground">
                                        {t(`accounting.billingResult.documents.status.${d.status}`, d.status)}
                                        {d.renderedAt ? ` · ${formatDateTime(d.renderedAt)}` : ""}
                                    </p>
                                    {d.status === "failed" && d.error ? <p className="text-xs text-destructive">{d.error}</p> : null}
                                </div>
                                <Button size="sm" variant="outline" disabled={d.status !== "ready" || busy !== null} onClick={() => void download(d.kind)}>
                                    {busy === d.kind ? <Loader2 className="h-4 w-4 animate-spin" /> : <Download className="h-4 w-4" />}
                                    <span className="ml-1">{t("accounting.billingResult.documents.download")}</span>
                                </Button>
                            </li>
                        ))}
                    </ul>
                )}
                {documentsPending(detail.data) ? (
                    <p className="flex items-center gap-2 text-xs text-muted-foreground">
                        <Loader2 className="h-3 w-3 animate-spin" />
                        {t("accounting.billingResult.documents.rendering")}
                    </p>
                ) : null}
                <DialogFooter>
                    {canManage ? (
                        <Button variant="outline" onClick={() => void regenerate()} disabled={busy !== null}>
                            {busy === "regenerate" ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <RefreshCw className="mr-2 h-4 w-4" />}
                            {t("accounting.billingResult.documents.regenerate")}
                        </Button>
                    ) : null}
                    <Button onClick={onClose}>{t("common.close")}</Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

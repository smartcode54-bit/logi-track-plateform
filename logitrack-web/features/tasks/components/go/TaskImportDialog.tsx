"use client";

/**
 * The boards' spreadsheet import over Go (T34; `POST /v1/tasks/import`, Appendix B §B.2.11). The browser reads the
 * sheet (xlsx loaded on the file pick, never with the page) and lets the user pick each row's customer (each
 * distinct customer cell is one server search, UI2C; the picker's value is the row's billing party); Go
 * validates every row as the legacy dialog did (the job-category cell, the plate against the fleet, the required
 * places and customer), numbers the tasks server-side (no `countTasksForDay` + 1, R10) and creates the valid ones.
 * "Check" is a dry run; "Import" writes. Job-assign reads an FM/LH column per row and sends one request per type:
 * each type's outcome applies as soon as its answer arrives (its created rows are marked and leave the list), so a
 * later type's failure never hides tasks already created and Import again never sends them twice; each type group
 * keeps its Idempotency-Key while its rows are unchanged, so a retry after a lost answer replays Go's stored one.
 */
import { useRef, useState } from "react";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { format } from "date-fns";
import { Check, Download, FileSpreadsheet, Loader2, Upload } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { customerSearchQuery, type CustomerPick } from "@/features/accounting/api/pickers.go";
import { CustomerPartySelect } from "@/components/entity-pickers";
import { fieldReasonText } from "@/lib/formServerErrors";
import { apiErrorText } from "@/lib/apiError";
import { newIdempotencyKey } from "@/lib/goFetch";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import { IMPORT_MAX_ROWS, importTasks, type ImportResult, type ImportRowInput, type TaskBoardType, type TaskType } from "../../api/tasks.go";

interface SheetRow {
    /** The sheet's row number (1 = the first data row). */
    line: number;
    taskType: TaskType;
    date: string;
    time: string;
    sourceHub: string;
    destination: string;
    customerCell: string;
    /** The customer's billing party (resolved from the cell, or picked); "" = none. */
    billingPartyId: string;
    jobCategory: string;
    truckType: string;
    plate: string;
    taskNo: string;
    note: string;
}

type RowResult = { ok: true; taskNo?: string } | { ok: false; text: string };

const pad = (n: number) => String(n).padStart(2, "0");

/** A date cell (an Excel serial, a `dd/MM/yyyy` text or an ISO day) as `yyyy-MM-dd`, else "". */
export function cellDay(raw: unknown, parseSerial?: (n: number) => { y: number; m: number; d: number } | null): string {
    if (raw == null || raw === "") return "";
    if (typeof raw === "number") {
        const dc = parseSerial?.(raw);
        return dc ? `${dc.y}-${pad(dc.m)}-${pad(dc.d)}` : "";
    }
    const s = String(raw).trim();
    const dmY = /^(\d{1,2})[/.-](\d{1,2})[/.-](\d{4})/.exec(s);
    if (dmY) return `${dmY[3]}-${pad(Number(dmY[2]))}-${pad(Number(dmY[1]))}`;
    const iso = /^(\d{4})-(\d{2})-(\d{2})/.exec(s);
    if (iso) return `${iso[1]}-${iso[2]}-${iso[3]}`;
    const d = new Date(s);
    return Number.isNaN(d.getTime()) ? "" : format(d, "yyyy-MM-dd");
}

/** A time cell (`HH:mm`, or an Excel day fraction) as `HH:mm`, else "". */
export function cellTime(raw: unknown): string {
    if (raw == null || raw === "") return "";
    if (typeof raw === "number") {
        const minutes = Math.round((raw % 1) * 24 * 60);
        return `${pad(Math.floor(minutes / 60) % 24)}:${pad(minutes % 60)}`;
    }
    const m = /^(\d{1,2})[:.](\d{2})/.exec(String(raw).trim());
    return m ? `${pad(Number(m[1]))}:${m[2]}` : "";
}

/** The FM/LH cell of job-assign: "LH" or "line" is line-haul, anything else first-mile (the legacy rule). */
export function cellTaskType(raw: unknown, fallback: TaskType): TaskType {
    const s = String(raw ?? "").trim().toUpperCase();
    if (!s) return fallback;
    return s.includes("LH") || s.includes("LINE") ? "line_haul" : "first_mile";
}

/** A customer cell (code or name, any case; a partial match last) to a customer. */
export function resolveCustomer(cell: string, list: CustomerPick[]): CustomerPick | undefined {
    const s = cell.trim().toUpperCase();
    if (!s) return undefined;
    return (
        list.find((c) => c.code.toUpperCase() === s) ||
        list.find((c) => c.name.toUpperCase() === s) ||
        list.find((c) => c.code.toUpperCase().includes(s) || c.name.toUpperCase().includes(s))
    );
}

/**
 * The billing party of each distinct customer cell: one picker search per cell (`q` = the cell; Go matches it
 * in the code or the name, the candidates `resolveCustomer` ranks), cached and shared with the pickers. A cell
 * that finds no customer, or one without a billing party, resolves to "" (the row's picker asks for one).
 */
export async function resolveCustomerCells(client: QueryClient, cells: Iterable<string>): Promise<Map<string, string>> {
    const distinct = [...new Set([...cells].map((c) => c.trim()).filter(Boolean))];
    const found = await Promise.all(
        distinct.map(async (cell) => {
            const page = await client.fetchQuery(customerSearchQuery(cell)).catch(() => [] as CustomerPick[]);
            return [cell, resolveCustomer(cell, page)?.billingPartyId ?? ""] as const;
        })
    );
    return new Map(found);
}

const isExampleNote = (note: string) => /ตัวอย่าง|example|ลบ|delete/i.test(note);

export function TaskImportDialog({ kind, onClose }: { kind: TaskBoardType; onClose: () => void }) {
    const { t } = useLanguage();
    const client = useQueryClient();
    const fileRef = useRef<HTMLInputElement>(null);
    const [fileName, setFileName] = useState("");
    const [rows, setRows] = useState<SheetRow[]>([]);
    const [results, setResults] = useState<Record<number, RowResult>>({});
    const [checked, setChecked] = useState(false);
    const [busy, setBusy] = useState<"parse" | "check" | "import" | null>(null);
    const [error, setError] = useState<string | null>(null);
    const fallbackType: TaskType = kind === "line_haul" ? "line_haul" : "first_mile";
    // One Idempotency-Key per type group and body (a changed group, a new file, a new customer: a new key).
    const groupKeys = useRef(new Map<TaskType, { body: string; key: string }>());
    const groupKey = (type: TaskType, body: ImportRowInput[]): string => {
        const sig = JSON.stringify(body);
        const cur = groupKeys.current.get(type);
        if (cur && cur.body === sig) return cur.key;
        const key = newIdempotencyKey();
        groupKeys.current.set(type, { body: sig, key });
        return key;
    };

    const parse = async (file: File) => {
        setBusy("parse");
        setError(null);
        setResults({});
        setChecked(false);
        try {
            const XLSX = await import("xlsx");
            const wb = XLSX.read(await file.arrayBuffer());
            const sheet = wb.Sheets[wb.SheetNames[0]];
            const data = XLSX.utils.sheet_to_json(sheet, { header: 1 }) as unknown[][];
            const headers = ((data[0] ?? []) as unknown[]).map((h) => String(h ?? "").toLowerCase().trim());
            const col = (terms: string[]) => headers.findIndex((h) => terms.some((x) => h.includes(x)));
            const idx = {
                type: col(["fm/lh", "fmlh"]),
                date: col(["date", "วัน"]),
                source: col(["source", "hub", "ต้นทาง", "pickup", "จุดรับงาน"]),
                dest: col(["destination", "soc", "ปลายทาง"]),
                time: col(["time", "เวลา"]),
                customer: col(["customer", "ลูกค้า", "บริษัท"]),
                category: col(["job category", "ประเภทงาน", "jobcategory"]),
                truckType: col(["truck type", "ประเภทรถ", "platetype"]),
                taskNo: col(["shipment", "เลขงาน"]),
                plate: col(["license", "ทะเบียน"]),
                note: col(["note", "หมายเหตุ"]),
            };
            const cell = (r: unknown[], i: number) => (i >= 0 ? r[i] : undefined);
            const text = (r: unknown[], i: number) => String(cell(r, i) ?? "").trim();
            const serial = (n: number) => {
                const dc = XLSX.SSF.parse_date_code(n);
                return dc ? { y: dc.y, m: dc.m, d: dc.d } : null;
            };
            const out: SheetRow[] = [];
            data.slice(1).forEach((r, i) => {
                if (!Array.isArray(r) || r.every((x) => x == null || String(x).trim() === "")) return;
                const note = text(r, idx.note);
                if (isExampleNote(note)) return;
                const customerCell = text(r, idx.customer);
                out.push({
                    line: i + 1,
                    taskType: kind === "all" ? cellTaskType(cell(r, idx.type), "first_mile") : fallbackType,
                    date: cellDay(cell(r, idx.date), serial),
                    time: cellTime(cell(r, idx.time)),
                    sourceHub: text(r, idx.source),
                    destination: text(r, idx.dest),
                    customerCell,
                    billingPartyId: "",
                    jobCategory: text(r, idx.category),
                    truckType: text(r, idx.truckType).toUpperCase(),
                    plate: text(r, idx.plate),
                    taskNo: text(r, idx.taskNo),
                    note,
                });
            });
            const kept = out.slice(0, IMPORT_MAX_ROWS);
            const parties = await resolveCustomerCells(client, kept.map((r) => r.customerCell));
            for (const r of kept) r.billingPartyId = parties.get(r.customerCell.trim()) ?? "";
            if (out.length > IMPORT_MAX_ROWS) setError(t("taskBoard.import.tooMany", { max: IMPORT_MAX_ROWS }));
            setRows(kept);
            setFileName(file.name);
        } catch {
            setError(t("firstMile.import.parseError"));
        } finally {
            setBusy(null);
        }
    };

    const downloadTemplate = async () => {
        const XLSX = await import("xlsx");
        const headers = [
            ...(kind === "all" ? ["FM/LH (ชนิดงาน)"] : []),
            "Date (วัน)",
            "Pickup Location (จุดรับงาน)",
            "Destination (ปลายทาง)",
            "Time (เวลา)",
            "Customer (ลูกค้า: รหัส/ชื่อ)",
            "Job Category (ประเภทงาน: หลัก/เสริม)",
            "Truck Type (ประเภทรถ)",
            "Shipment ID (เลขงาน)",
            "License Plate (ทะเบียน)",
            "Note (หมายเหตุ)",
        ];
        const example = [
            ...(kind === "all" ? ["FM"] : []),
            format(new Date(), "dd/MM/yyyy"),
            "ALANG-A",
            "SOCE",
            "15:00",
            "CJSF",
            "หลัก",
            "6WH",
            "",
            "70-1234",
            "*ตัวอย่าง — ลบแถวนี้ก่อน import",
        ];
        const wb = XLSX.utils.book_new();
        XLSX.utils.book_append_sheet(wb, XLSX.utils.aoa_to_sheet([headers, example]), "Template");
        XLSX.writeFile(wb, kind === "line_haul" ? "LineHaulTask_Template.xlsx" : kind === "all" ? "JobAssign_Template.xlsx" : "FirstMileTask_Template.xlsx");
    };

    const run = async (dryRun: boolean) => {
        setBusy(dryRun ? "check" : "import");
        setError(null);
        const next: Record<number, RowResult> = {};
        const createdLines = new Set<number>();
        let created = 0;
        let failed = false;
        try {
            for (const type of ["first_mile", "line_haul"] as const) {
                const group = rows.filter((r) => r.taskType === type);
                if (group.length === 0) continue;
                const body: ImportRowInput[] = group.map((r) => ({
                    date: r.date,
                    time: r.time || undefined,
                    sourceHub: r.sourceHub,
                    destination: r.destination,
                    billingPartyId: r.billingPartyId,
                    jobCategory: r.jobCategory || undefined,
                    truckType: r.truckType || undefined,
                    plate: r.plate || undefined,
                    taskNo: r.taskNo || undefined,
                    note: r.note || undefined,
                }));
                const out: ImportResult = await importTasks(type, body, dryRun, undefined, dryRun ? undefined : groupKey(type, body));
                for (const c of out.created) {
                    const line = group[c.row - 1].line;
                    next[line] = { ok: true, taskNo: c.taskNo };
                    created++;
                    if (!dryRun) createdLines.add(line);
                }
                for (const e of out.errors) {
                    const text = (e.fields ?? []).map((f) => `${f.field}: ${fieldReasonText(f.reason, t, f.params ?? {})}`).join("; ") || e.code;
                    next[group[e.row - 1].line] = { ok: false, text };
                }
            }
            setChecked(true);
        } catch (e) {
            failed = true;
            const text = apiErrorText(e, t);
            setError(createdLines.size > 0 ? `${t("taskBoard.import.partial", { count: createdLines.size })} ${text}` : text);
        } finally {
            // What the answered groups did stays shown, whichever group failed after them.
            setResults(next);
            if (createdLines.size > 0) {
                if (!failed) toast.success(t("taskBoard.import.done", { count: created }));
                // The created rows leave the list, so a second "Import" never creates them twice.
                const left = rows.filter((r) => !createdLines.has(r.line));
                if (left.length === 0 && !failed) onClose();
                else setRows(left);
            }
            setBusy(null);
        }
    };

    const invalid = Object.values(results).filter((r) => !r.ok).length;
    const setCustomer = (line: number, partyId: string) => {
        setRows((prev) => prev.map((r) => (r.line === line ? { ...r, billingPartyId: partyId } : r)));
        setChecked(false);
    };

    return (
        <Dialog open onOpenChange={(o) => !o && busy === null && onClose()}>
            <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-5xl">
                <DialogHeader>
                    <DialogTitle>{kind === "all" ? t("jobAssign.import.title") : t("firstMile.import.title")}</DialogTitle>
                    <DialogDescription>{kind === "all" ? t("jobAssign.import.description") : t("firstMile.import.description")}</DialogDescription>
                </DialogHeader>
                <div className="flex flex-wrap items-center gap-3">
                    <Button variant="outline" size="sm" onClick={() => void downloadTemplate()}>
                        <Download className="mr-2 h-4 w-4" />
                        {t("firstMile.import.downloadTemplate")}
                    </Button>
                    <input ref={fileRef} type="file" accept=".xlsx,.xls,.csv" className="hidden"
                        onChange={(e) => {
                            const f = e.target.files?.[0];
                            if (f) void parse(f);
                            e.target.value = "";
                        }} />
                    <Button variant="outline" size="sm" onClick={() => fileRef.current?.click()} disabled={busy !== null}>
                        {busy === "parse" ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Upload className="mr-2 h-4 w-4" />}
                        {t("firstMile.import.clickUpload")}
                    </Button>
                    {fileName ? (
                        <span className="flex items-center gap-1 text-sm text-muted-foreground">
                            <FileSpreadsheet className="h-4 w-4" />
                            {fileName} · {rows.length} {t("firstMile.import.recordsFound")}
                        </span>
                    ) : null}
                </div>
                {error ? (
                    <Alert variant="destructive">
                        <AlertDescription>{error}</AlertDescription>
                    </Alert>
                ) : null}
                {rows.length > 0 ? (
                    <div className="max-h-[50vh] overflow-auto rounded border">
                        <Table>
                            <TableHeader>
                                <TableRow>
                                    <TableHead>{t("firstMile.import.table.row")}</TableHead>
                                    {kind === "all" ? <TableHead>{t("jobAssign.table.jobType")}</TableHead> : null}
                                    <TableHead>{t("firstMile.import.table.date")}</TableHead>
                                    <TableHead>{t("firstMile.import.table.source")}</TableHead>
                                    <TableHead>{t("firstMile.import.table.dest")}</TableHead>
                                    <TableHead>{t("firstMile.task.customer")}</TableHead>
                                    <TableHead>{t("firstMile.import.table.jobCategory")}</TableHead>
                                    <TableHead>{t("firstMile.import.table.truckType")}</TableHead>
                                    <TableHead>{t("firstMile.task.licensePlate")}</TableHead>
                                    <TableHead>{t("firstMile.import.table.taskId")}</TableHead>
                                    <TableHead>{t("firstMile.import.table.status")}</TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {rows.map((r) => {
                                    const res = results[r.line];
                                    return (
                                        <TableRow key={r.line} className={res && !res.ok ? "bg-destructive/5" : undefined}>
                                            <TableCell>{r.line}</TableCell>
                                            {kind === "all" ? <TableCell>{r.taskType === "line_haul" ? "LH" : "FM"}</TableCell> : null}
                                            <TableCell>{r.date || "—"}{r.time ? ` ${r.time}` : ""}</TableCell>
                                            <TableCell>{r.sourceHub || "—"}</TableCell>
                                            <TableCell>{r.destination || "—"}</TableCell>
                                            <TableCell className="min-w-[200px]">
                                                <CustomerPartySelect aria-label={`${t("firstMile.task.customer")} ${r.line}`} value={r.billingPartyId}
                                                    onValueChange={(p) => setCustomer(r.line, p)} placeholder={r.customerCell || t("firstMile.import.customerMissing")}
                                                    size="sm" className="min-w-[200px]" />
                                            </TableCell>
                                            <TableCell>{r.jobCategory || "—"}</TableCell>
                                            <TableCell>{r.truckType || "—"}</TableCell>
                                            <TableCell className="font-mono">{r.plate || "—"}</TableCell>
                                            <TableCell className="font-mono text-xs">{(res?.ok && res.taskNo) || r.taskNo || "—"}</TableCell>
                                            <TableCell className="max-w-[260px] text-xs">
                                                {res ? (
                                                    res.ok ? (
                                                        <span className="flex items-center gap-1 text-success"><Check className="h-3 w-3" />{t("taskBoard.import.valid")}</span>
                                                    ) : (
                                                        <span className="text-destructive">{res.text}</span>
                                                    )
                                                ) : (
                                                    <span className="text-muted-foreground">—</span>
                                                )}
                                            </TableCell>
                                        </TableRow>
                                    );
                                })}
                            </TableBody>
                        </Table>
                    </div>
                ) : null}
                {checked && invalid > 0 ? <p className="text-sm text-destructive">{t("taskBoard.import.invalidRows", { count: invalid })}</p> : null}
                <DialogFooter>
                    <Button variant="outline" onClick={onClose} disabled={busy !== null}>
                        {t("firstMile.import.cancel")}
                    </Button>
                    <Button variant="secondary" onClick={() => void run(true)} disabled={busy !== null || rows.length === 0}>
                        {busy === "check" ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {t("taskBoard.import.check")}
                    </Button>
                    <Button onClick={() => void run(false)} disabled={busy !== null || rows.length === 0}>
                        {busy === "import" ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {t("firstMile.import.upload")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

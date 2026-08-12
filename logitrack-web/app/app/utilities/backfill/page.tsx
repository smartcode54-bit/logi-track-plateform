"use client";

import { useState } from "react";
import { httpsCallable } from "firebase/functions";
import { AlertCircle, CheckCircle2, Loader2, PauseCircle, Truck, Zap } from "lucide-react";
import { functions } from "@/firebase/client";
import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";

interface BackfillStats {
    success: boolean;
    totalProcessed: number;
    updated: number;
    alreadyComplete: number;
    errors: number;
    errorDetails: string[];
}

interface StandbyBackfillResult {
    scanned: number;
    eligible: number;
    written: number;
    skipped: number;
    failed: number;
    /** Rows left untouched because their period already carries a sent/paid invoice (ADR 0008 §5). */
    blocked?: number;
    blockedInvoices?: string[];
    failures: Array<{ standbyId: string; error?: string }>;
    capped: boolean;
}

export default function BackfillPage() {
    const auth = useAuth();
    const { t } = useLanguage();
    const [loading, setLoading] = useState(false);
    const [result, setResult] = useState<BackfillStats | null>(null);
    const [error, setError] = useState<string | null>(null);
    // Standby billing backfill state
    const [standbyFrom, setStandbyFrom] = useState(() => {
        const d = new Date(); d.setDate(d.getDate() - 89);
        return d.toISOString().slice(0, 10);
    });
    const [standbyTo, setStandbyTo] = useState(() => new Date().toISOString().slice(0, 10));
    const [standbyLoading, setStandbyLoading] = useState(false);
    const [standbyResult, setStandbyResult] = useState<StandbyBackfillResult | null>(null);
    const [standbyError, setStandbyError] = useState<string | null>(null);
    // ADR 0008 §5 — without this, an existing standby price could never be corrected, which is why
    // recomputing after a rate change appeared to do nothing.
    const [standbyForce, setStandbyForce] = useState(false);

    const runStandbyBillingBackfill = async () => {
        if (!auth?.currentUser) return;
        setStandbyLoading(true);
        setStandbyError(null);
        setStandbyResult(null);
        try {
            const fn = httpsCallable(functions, "backfillStandbyBillingSnapshots");
            const res = await fn({
                fromDateStr: standbyFrom,
                toDateStr: standbyTo,
                forceRecompute: standbyForce,
            });
            setStandbyResult(res.data as StandbyBackfillResult);
        } catch (err) {
            setStandbyError(err instanceof Error ? err.message : String(err));
        } finally {
            setStandbyLoading(false);
        }
    };

    interface TruckCollectionStats { scanned: number; updated: number; skipped: number; errors: number }
    interface TruckBackfillResult { tripRecords: TruckCollectionStats; vehicleExpenses: TruckCollectionStats; tasks: TruckCollectionStats }

    const [truckLoading, setTruckLoading] = useState(false);
    const [truckResult, setTruckResult] = useState<TruckBackfillResult | null>(null);
    const [truckError, setTruckError] = useState<string | null>(null);

    const runBackfillTripTruckData = async () => {
        if (!auth?.currentUser) return;
        setTruckLoading(true);
        setTruckError(null);
        setTruckResult(null);
        try {
            const fn = httpsCallable(functions, "backfillTripTruckData");
            const res = await fn({});
            setTruckResult(res.data as TruckBackfillResult);
        } catch (err) {
            setTruckError(err instanceof Error ? err.message : String(err));
        } finally {
            setTruckLoading(false);
        }
    };

    interface TruckTypeStat { matched: number; updated: number }
    interface TruckTypeMigrationResult {
        fromType: string;
        toType: string;
        tasks: TruckTypeStat;
        trip_records: TruckTypeStat;
        capped: boolean;
    }

    const [ttFrom, setTtFrom] = useState("PICKUP");
    const [ttTo, setTtTo] = useState("4W");
    const [ttLoading, setTtLoading] = useState(false);
    const [ttResult, setTtResult] = useState<TruckTypeMigrationResult | null>(null);
    const [ttError, setTtError] = useState<string | null>(null);

    const runBackfillTruckType = async () => {
        if (!auth?.currentUser) return;
        setTtLoading(true);
        setTtError(null);
        setTtResult(null);
        try {
            const fn = httpsCallable(functions, "backfillTruckType");
            const res = await fn({ fromType: ttFrom.trim(), toType: ttTo.trim() });
            setTtResult(res.data as TruckTypeMigrationResult);
        } catch (err) {
            setTtError(err instanceof Error ? err.message : String(err));
        } finally {
            setTtLoading(false);
        }
    };

    interface TripJobCategoryResult {
        success: boolean;
        totalProcessed: number;
        alreadySet: number;
        updated: number;
        copiedFromTask: number;
        defaultedPrimary: number;
        taskMissing: number;
        errors: number;
        errorDetails: string[];
    }

    const [jcLoading, setJcLoading] = useState(false);
    const [jcResult, setJcResult] = useState<TripJobCategoryResult | null>(null);
    const [jcError, setJcError] = useState<string | null>(null);

    const runBackfillTripJobCategory = async () => {
        if (!auth?.currentUser) return;
        setJcLoading(true);
        setJcError(null);
        setJcResult(null);
        try {
            const fn = httpsCallable(functions, "backfillTripJobCategoryFromTask");
            const res = await fn({});
            setJcResult(res.data as TripJobCategoryResult);
        } catch (err) {
            setJcError(err instanceof Error ? err.message : String(err));
        } finally {
            setJcLoading(false);
        }
    };

    const runBackfillTaskCustomerLinks = async () => {
        if (!auth?.currentUser) return;

        setLoading(true);
        setError(null);
        setResult(null);

        try {
            const backfill = httpsCallable(functions, "backfillTaskCustomerLinks");
            const response = await backfill({});
            const data = response.data as BackfillStats;
            setResult(data);
        } catch (err) {
            const message = err instanceof Error ? err.message : String(err);
            setError(message);
            console.error("Backfill error:", err);
        } finally {
            setLoading(false);
        }
    };

    return (
        <div className="container py-8">
            <div className="mb-8">
                <h1 className="text-3xl font-bold">Data Backfill Utilities</h1>
                <p className="text-muted-foreground mt-2">
                    Tools to backfill and migrate data structures in the system
                </p>
            </div>

            <div className="grid gap-6">
                {/* Standby Billing Backfill */}
                <Card className="border-l-4 border-l-orange-500">
                    <CardHeader>
                        <div className="flex items-start justify-between">
                            <div>
                                <CardTitle className="flex items-center gap-2">
                                    <PauseCircle className="h-5 w-5 text-orange-500" />
                                    Backfill Standby Billing Snapshots
                                </CardTitle>
                                <CardDescription>
                                    Compute and persist billingEstimateThb for completed standby records missing billing
                                </CardDescription>
                            </div>
                            <Badge variant="outline">Admin Only</Badge>
                        </div>
                    </CardHeader>
                    <CardContent className="space-y-4">
                        <div className="rounded-lg bg-orange-50 p-4 text-sm text-orange-900">
                            <p className="font-medium mb-2">What this does:</p>
                            <ul className="list-disc list-inside space-y-1 text-xs">
                                <li>Scans standby_records with status=completed by <strong>endedAt</strong> in the date range (same axis the invoice groups by — ADR 0008)</li>
                                <li>Skips records that already have billingEstimateThb, unless &quot;Force recompute&quot; is ticked</li>
                                <li>Looks up standby rate from standby_rate_entries by customerId + date</li>
                                <li>Never rewrites a price in a period that already has a sent/paid invoice</li>
                            </ul>
                        </div>

                        <div className="grid grid-cols-2 gap-4">
                            <div className="space-y-1.5">
                                <Label>From Date (yyyy-MM-dd)</Label>
                                <Input type="date" value={standbyFrom} onChange={(e) => setStandbyFrom(e.target.value)} />
                            </div>
                            <div className="space-y-1.5">
                                <Label>To Date (yyyy-MM-dd)</Label>
                                <Input type="date" value={standbyTo} onChange={(e) => setStandbyTo(e.target.value)} />
                            </div>
                        </div>

                        <label className="flex items-center gap-2 text-sm cursor-pointer">
                            <input
                                type="checkbox"
                                className="h-4 w-4"
                                checked={standbyForce}
                                onChange={(e) => setStandbyForce(e.target.checked)}
                            />
                            <span>
                                Force recompute — overwrite existing prices (draft periods only)
                            </span>
                        </label>

                        {standbyError && (
                            <Alert variant="destructive">
                                <AlertCircle className="h-4 w-4" />
                                <AlertTitle>Error</AlertTitle>
                                <AlertDescription>{standbyError}</AlertDescription>
                            </Alert>
                        )}

                        {standbyResult && (
                            <Alert className="border-green-200 bg-green-50">
                                <CheckCircle2 className="h-4 w-4 text-green-600" />
                                <AlertTitle className="text-green-900">Backfill Complete</AlertTitle>
                                <AlertDescription className="text-green-800 mt-2">
                                    <div className="grid grid-cols-3 gap-3 text-sm font-mono">
                                        {[
                                            ["Scanned", standbyResult.scanned],
                                            ["Eligible", standbyResult.eligible],
                                            ["Written", standbyResult.written],
                                            ["Skipped", standbyResult.skipped],
                                            ["Failed", standbyResult.failed],
                                            ["Blocked (invoiced)", standbyResult.blocked ?? 0],
                                        ].map(([label, val]) => (
                                            <div key={String(label)}>
                                                <div className="text-xs opacity-75">{label}</div>
                                                <div className="text-lg font-bold">{val}</div>
                                            </div>
                                        ))}
                                    </div>
                                    {(standbyResult.blocked ?? 0) > 0 && (
                                        <p className="text-xs mt-2 text-orange-700">
                                            ⚠ {standbyResult.blocked} record(s) left unchanged — their period already has a
                                            sent/paid invoice
                                            {standbyResult.blockedInvoices?.length
                                                ? ` (${standbyResult.blockedInvoices.join(", ")})`
                                                : ""}
                                            . Cancel or credit-note it first.
                                        </p>
                                    )}
                                    {standbyResult.capped && (
                                        <p className="text-xs mt-2 text-orange-700">⚠ More eligible records remain — run again or widen date range.</p>
                                    )}
                                    {standbyResult.failures.length > 0 && (
                                        <div className="mt-3 text-xs">
                                            <p className="font-semibold mb-1">Failures:</p>
                                            <div className="bg-white bg-opacity-50 rounded p-2 max-h-32 overflow-y-auto">
                                                {standbyResult.failures.map((f) => (
                                                    <div key={f.standbyId}>• {f.standbyId}: {f.error ?? "unknown"}</div>
                                                ))}
                                            </div>
                                        </div>
                                    )}
                                </AlertDescription>
                            </Alert>
                        )}

                        <div className="flex gap-2">
                            <Button onClick={runStandbyBillingBackfill} disabled={standbyLoading} variant="default" className="gap-2 bg-orange-600 hover:bg-orange-700">
                                {standbyLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : <PauseCircle className="h-4 w-4" />}
                                {standbyLoading ? "Running..." : "Run Standby Backfill"}
                            </Button>
                            {standbyResult && <Button variant="outline" onClick={() => setStandbyResult(null)}>Clear</Button>}
                        </div>
                    </CardContent>
                </Card>

                {/* Backfill Task Customer Links */}
                <Card className="border-l-4 border-l-blue-500">
                    <CardHeader>
                        <div className="flex items-start justify-between">
                            <div>
                                <CardTitle className="flex items-center gap-2">
                                    <Zap className="h-5 w-5" />
                                    Backfill Task Customer Links
                                </CardTitle>
                                <CardDescription>
                                    Add sourceHubLinkedCustomerId and destinationLinkedCustomerId to old tasks
                                </CardDescription>
                            </div>
                            <Badge variant="outline">Admin Only</Badge>
                        </div>
                    </CardHeader>
                    <CardContent className="space-y-4">
                        <div className="rounded-lg bg-blue-50 p-4 text-sm text-blue-900">
                            <p className="font-medium mb-2">What this does:</p>
                            <ul className="list-disc list-inside space-y-1 text-xs">
                                <li>Scans all tasks in Firestore</li>
                                <li>Finds tasks missing customer link fields</li>
                                <li>Sets both fields to customer: TTP (7gbnX0Tv9xNQgTKrgp0F)</li>
                                <li>Processes in batches of 100 for efficiency</li>
                            </ul>
                        </div>

                        {error && (
                            <Alert variant="destructive">
                                <AlertCircle className="h-4 w-4" />
                                <AlertTitle>Error</AlertTitle>
                                <AlertDescription>{error}</AlertDescription>
                            </Alert>
                        )}

                        {result && (
                            <Alert className="border-green-200 bg-green-50">
                                <CheckCircle2 className="h-4 w-4 text-green-600" />
                                <AlertTitle className="text-green-900">Backfill Completed</AlertTitle>
                                <AlertDescription className="text-green-800 space-y-2 mt-2">
                                    <div className="grid grid-cols-2 gap-4 text-sm font-mono">
                                        <div>
                                            <div className="text-xs opacity-75">Total Processed</div>
                                            <div className="text-lg font-bold">{result.totalProcessed}</div>
                                        </div>
                                        <div>
                                            <div className="text-xs opacity-75">Updated</div>
                                            <div className="text-lg font-bold">{result.updated}</div>
                                        </div>
                                        <div>
                                            <div className="text-xs opacity-75">Already Complete</div>
                                            <div className="text-lg font-bold">{result.alreadyComplete}</div>
                                        </div>
                                        <div>
                                            <div className="text-xs opacity-75">Errors</div>
                                            <div className="text-lg font-bold">{result.errors}</div>
                                        </div>
                                    </div>

                                    {result.errors > 0 && result.errorDetails?.length > 0 && (
                                        <div className="mt-4 space-y-1">
                                            <div className="text-xs font-semibold">Error Details:</div>
                                            <div className="bg-white bg-opacity-50 rounded p-2 text-xs max-h-32 overflow-y-auto">
                                                {result.errorDetails.map((err, i) => (
                                                    <div key={i} className="break-words">
                                                        • {err}
                                                    </div>
                                                ))}
                                            </div>
                                        </div>
                                    )}
                                </AlertDescription>
                            </Alert>
                        )}

                        <div className="flex gap-2">
                            <Button
                                onClick={runBackfillTaskCustomerLinks}
                                disabled={loading}
                                className="gap-2"
                            >
                                {loading ? (
                                    <>
                                        <Loader2 className="h-4 w-4 animate-spin" />
                                        Running...
                                    </>
                                ) : (
                                    <>
                                        <Zap className="h-4 w-4" />
                                        Run Backfill
                                    </>
                                )}
                            </Button>
                            {result && (
                                <Button variant="outline" onClick={() => setResult(null)}>
                                    Clear Results
                                </Button>
                            )}
                        </div>

                        <div className="text-xs text-muted-foreground">
                            <p className="font-medium mb-1">Next step:</p>
                            <p>
                                After backfill succeeds, run <code className="bg-muted px-1 rounded">backfillTripBillingSnapshots</code> to compute
                                billing for all delivered trips.
                            </p>
                        </div>
                    </CardContent>
                </Card>

                {/* Trip Truck Data Backfill */}
                <Card className="border-l-4 border-l-blue-500">
                    <CardHeader>
                        <CardTitle className="flex items-center gap-2">
                            <Zap className="w-5 h-5 text-blue-500" />
                            Backfill Trip Truck Data
                        </CardTitle>
                        <CardDescription>
                            Fills <code className="bg-muted px-1 rounded">truckLicensePlate</code> and <code className="bg-muted px-1 rounded">truckType</code> into trip_records that are missing them.
                            Resolves from the linked task&apos;s snapshot (written at check-in time) — so historical trips show the correct plate even after truck reassignment.
                        </CardDescription>
                    </CardHeader>
                    <CardContent className="space-y-4">
                        <Button onClick={runBackfillTripTruckData} disabled={truckLoading} className="bg-blue-600 hover:bg-blue-700">
                            {truckLoading ? <><Loader2 className="w-4 h-4 mr-2 animate-spin" />Running...</> : "Run Backfill"}
                        </Button>
                        {truckError && (
                            <Alert variant="destructive">
                                <AlertCircle className="h-4 w-4" />
                                <AlertTitle>Error</AlertTitle>
                                <AlertDescription>{truckError}</AlertDescription>
                            </Alert>
                        )}
                        {truckResult && (
                            <Alert className="border-green-200 bg-green-50">
                                <CheckCircle2 className="h-4 w-4 text-green-600" />
                                <AlertTitle className="text-green-900">Done</AlertTitle>
                                <AlertDescription className="text-green-800 mt-2 space-y-3">
                                    {(["tripRecords", "vehicleExpenses", "tasks"] as const).map((key) => {
                                        const labels = { tripRecords: "Trip Records", vehicleExpenses: "Vehicle Expenses", tasks: "Tasks" };
                                        const s = truckResult[key];
                                        return (
                                            <div key={key}>
                                                <p className="text-xs font-semibold mb-1">{labels[key]}</p>
                                                <div className="flex gap-2 flex-wrap">
                                                    <Badge variant="outline">Scanned: {s.scanned}</Badge>
                                                    <Badge className="bg-green-600">Updated: {s.updated}</Badge>
                                                    <Badge variant="secondary">Skipped: {s.skipped}</Badge>
                                                    {s.errors > 0 && <Badge variant="destructive">Errors: {s.errors}</Badge>}
                                                </div>
                                            </div>
                                        );
                                    })}
                                </AlertDescription>
                            </Alert>
                        )}
                    </CardContent>
                </Card>

                {/* Truck Type Migration (PICKUP → 4W) */}
                <Card className="border-l-4 border-l-green-500">
                    <CardHeader>
                        <div className="flex items-start justify-between">
                            <div>
                                <CardTitle className="flex items-center gap-2">
                                    <Truck className="h-5 w-5 text-green-600" />
                                    Migrate Truck Type
                                </CardTitle>
                                <CardDescription>
                                    Rewrite a legacy <code className="bg-muted px-1 rounded">truckType</code> value across <code className="bg-muted px-1 rounded">tasks</code> and <code className="bg-muted px-1 rounded">trip_records</code>. Default: PICKUP → 4W. Reuse for 4WH by changing the From value.
                                </CardDescription>
                            </div>
                            <Badge variant="outline">Admin Only</Badge>
                        </div>
                    </CardHeader>
                    <CardContent className="space-y-4">
                        <div className="rounded-lg bg-green-50 p-4 text-sm text-green-900">
                            <p className="font-medium mb-2">What this does:</p>
                            <ul className="list-disc list-inside space-y-1 text-xs">
                                <li>Finds tasks + trip_records where truckType == From</li>
                                <li>Rewrites them to the To value (batched, idempotent)</li>
                                <li>Does NOT touch the trucks master (keeps full-name types)</li>
                            </ul>
                        </div>

                        <div className="grid grid-cols-2 gap-4">
                            <div className="space-y-1.5">
                                <Label>From (old value)</Label>
                                <Input value={ttFrom} onChange={(e) => setTtFrom(e.target.value)} />
                            </div>
                            <div className="space-y-1.5">
                                <Label>To (new value)</Label>
                                <Input value={ttTo} onChange={(e) => setTtTo(e.target.value)} />
                            </div>
                        </div>

                        {ttError && (
                            <Alert variant="destructive">
                                <AlertCircle className="h-4 w-4" />
                                <AlertTitle>Error</AlertTitle>
                                <AlertDescription>{ttError}</AlertDescription>
                            </Alert>
                        )}

                        {ttResult && (
                            <Alert className="border-green-200 bg-green-50">
                                <CheckCircle2 className="h-4 w-4 text-green-600" />
                                <AlertTitle className="text-green-900">Migrated {ttResult.fromType} → {ttResult.toType}</AlertTitle>
                                <AlertDescription className="text-green-800 mt-2 space-y-3">
                                    {(["tasks", "trip_records"] as const).map((key) => {
                                        const s = ttResult[key];
                                        return (
                                            <div key={key}>
                                                <p className="text-xs font-semibold mb-1">{key}</p>
                                                <div className="flex gap-2 flex-wrap">
                                                    <Badge variant="outline">Matched: {s.matched}</Badge>
                                                    <Badge className="bg-green-600">Updated: {s.updated}</Badge>
                                                </div>
                                            </div>
                                        );
                                    })}
                                    {ttResult.capped && (
                                        <p className="text-xs mt-2 text-orange-700">⚠ More matched than updated — run again.</p>
                                    )}
                                </AlertDescription>
                            </Alert>
                        )}

                        <div className="flex gap-2">
                            <Button onClick={runBackfillTruckType} disabled={ttLoading} className="gap-2 bg-green-600 hover:bg-green-700">
                                {ttLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Truck className="h-4 w-4" />}
                                {ttLoading ? "Running..." : "Run Migration"}
                            </Button>
                            {ttResult && <Button variant="outline" onClick={() => setTtResult(null)}>Clear</Button>}
                        </div>
                    </CardContent>
                </Card>

                {/* Backfill Trip Job Category (หลัก/เสริม) */}
                <Card className="border-l-4 border-l-amber-500">
                    <CardHeader>
                        <div className="flex items-start justify-between">
                            <div>
                                <CardTitle className="flex items-center gap-2">
                                    <Zap className="h-5 w-5 text-amber-500" />
                                    Backfill Trip Job Category (หลัก/เสริม)
                                </CardTitle>
                                <CardDescription>
                                    Copies each trip&apos;s jobCategory from its linked task (ADR 0010). Fixes trips that
                                    never got a value because billing was skipped/failed, so the invoice stops guessing หลัก.
                                </CardDescription>
                            </div>
                            <Badge variant="outline">Admin Only</Badge>
                        </div>
                    </CardHeader>
                    <CardContent className="space-y-4">
                        <div className="rounded-lg bg-amber-50 p-4 text-sm text-amber-900">
                            <p className="font-medium mb-2">What this does:</p>
                            <ul className="list-disc list-inside space-y-1 text-xs">
                                <li>Scans trip_records missing jobCategory (idempotent — skips ones already set)</li>
                                <li>Copies PRIMARY/SUPPLEMENTARY from the linked task</li>
                                <li>Defaults PRIMARY only when the task is missing or has no category</li>
                            </ul>
                        </div>

                        {jcError && (
                            <Alert variant="destructive">
                                <AlertCircle className="h-4 w-4" />
                                <AlertTitle>Error</AlertTitle>
                                <AlertDescription>{jcError}</AlertDescription>
                            </Alert>
                        )}

                        {jcResult && (
                            <Alert className="border-green-200 bg-green-50">
                                <CheckCircle2 className="h-4 w-4 text-green-600" />
                                <AlertTitle className="text-green-900">Backfill Complete</AlertTitle>
                                <AlertDescription className="text-green-800 mt-2">
                                    <div className="grid grid-cols-3 gap-3 text-sm font-mono">
                                        {[
                                            ["Processed", jcResult.totalProcessed],
                                            ["Already set", jcResult.alreadySet],
                                            ["Updated", jcResult.updated],
                                            ["Copied from task", jcResult.copiedFromTask],
                                            ["Defaulted PRIMARY", jcResult.defaultedPrimary],
                                            ["Task missing", jcResult.taskMissing],
                                            ["Errors", jcResult.errors],
                                        ].map(([label, val]) => (
                                            <div key={String(label)}>
                                                <div className="text-xs opacity-75">{label}</div>
                                                <div className="text-lg font-bold">{val}</div>
                                            </div>
                                        ))}
                                    </div>
                                    {jcResult.errors > 0 && jcResult.errorDetails?.length > 0 && (
                                        <div className="mt-3 text-xs">
                                            <p className="font-semibold mb-1">Error Details:</p>
                                            <div className="bg-white bg-opacity-50 rounded p-2 max-h-32 overflow-y-auto">
                                                {jcResult.errorDetails.map((err, i) => (
                                                    <div key={i} className="break-words">• {err}</div>
                                                ))}
                                            </div>
                                        </div>
                                    )}
                                </AlertDescription>
                            </Alert>
                        )}

                        <div className="flex gap-2">
                            <Button onClick={runBackfillTripJobCategory} disabled={jcLoading} className="gap-2 bg-amber-600 hover:bg-amber-700">
                                {jcLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Zap className="h-4 w-4" />}
                                {jcLoading ? "Running..." : "Run Backfill"}
                            </Button>
                            {jcResult && <Button variant="outline" onClick={() => setJcResult(null)}>Clear</Button>}
                        </div>
                    </CardContent>
                </Card>
            </div>
        </div>
    );
}

"use client";

import { useState } from "react";
import { httpsCallable } from "firebase/functions";
import { AlertCircle, CheckCircle2, Loader2, PauseCircle, Zap } from "lucide-react";
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

    const runStandbyBillingBackfill = async () => {
        if (!auth?.currentUser) return;
        setStandbyLoading(true);
        setStandbyError(null);
        setStandbyResult(null);
        try {
            const fn = httpsCallable(functions, "backfillStandbyBillingSnapshots");
            const res = await fn({ fromDateStr: standbyFrom, toDateStr: standbyTo });
            setStandbyResult(res.data as StandbyBackfillResult);
        } catch (err) {
            setStandbyError(err instanceof Error ? err.message : String(err));
        } finally {
            setStandbyLoading(false);
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
                                <li>Scans standby_records with status=completed in the date range</li>
                                <li>Skips records that already have billingEstimateThb</li>
                                <li>Looks up standby rate from standby_rate_entries by customerId + date</li>
                                <li>Writes billingEstimateThb to each record (idempotent)</li>
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
                                        ].map(([label, val]) => (
                                            <div key={String(label)}>
                                                <div className="text-xs opacity-75">{label}</div>
                                                <div className="text-lg font-bold">{val}</div>
                                            </div>
                                        ))}
                                    </div>
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
            </div>
        </div>
    );
}

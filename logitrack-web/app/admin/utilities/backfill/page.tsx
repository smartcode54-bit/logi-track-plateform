"use client";

import { useState } from "react";
import { httpsCallable } from "firebase/functions";
import { AlertCircle, CheckCircle2, Loader2, Zap } from "lucide-react";
import { functions } from "@/firebase/client";
import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
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

export default function BackfillPage() {
    const auth = useAuth();
    const { t } = useLanguage();
    const [loading, setLoading] = useState(false);
    const [result, setResult] = useState<BackfillStats | null>(null);
    const [error, setError] = useState<string | null>(null);

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

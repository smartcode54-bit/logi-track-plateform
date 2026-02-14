"use client";

import { useEffect, useState } from "react";
import { collection, query, orderBy, onSnapshot, deleteDoc, doc } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Loader2, Trash2, Mail } from "lucide-react";
import { toast } from "sonner";
import { format } from "date-fns";

type WaitlistEntry = {
    id: string;
    email: string;
    createdAt: any;
};

export default function AdminWaitlistPage() {
    const [entries, setEntries] = useState<WaitlistEntry[]>([]);
    const [loading, setLoading] = useState(true);

    useEffect(() => {
        const q = query(collection(db, COLLECTIONS.WAITLIST), orderBy("createdAt", "desc"));
        const unsubscribe = onSnapshot(q, (snapshot) => {
            const data = snapshot.docs.map(doc => ({
                id: doc.id,
                ...doc.data()
            })) as WaitlistEntry[];
            setEntries(data);
            setLoading(false);
        }, (error) => {
            console.error("Error fetching waitlist:", error);
            toast.error("Failed to fetch waitlist");
            setLoading(false);
        });

        return () => unsubscribe();
    }, []);

    const handleDelete = async (id: string, email: string) => {
        if (!confirm(`Are you sure you want to remove ${email} from the waitlist?`)) return;

        try {
            await deleteDoc(doc(db, COLLECTIONS.WAITLIST, id));
            toast.success("Entry removed");
        } catch (error) {
            console.error("Error deleting entry:", error);
            toast.error("Failed to delete entry");
        }
    };

    if (loading) {
        return (
            <div className="flex h-[50vh] items-center justify-center">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
            </div>
        );
    }

    return (
        <div className="p-6 space-y-6">
            <div>
                <h1 className="text-3xl font-bold tracking-tight">Waitlist</h1>
                <p className="text-muted-foreground mt-2">
                    Manage users who requested access.
                </p>
            </div>

            <Card>
                <CardHeader>
                    <CardTitle className="flex items-center gap-2">
                        <Mail className="h-5 w-5" />
                        Waitlist Requests ({entries.length})
                    </CardTitle>
                    <CardDescription>
                        List of emails waiting for an invitation.
                    </CardDescription>
                </CardHeader>
                <CardContent>
                    <div className="rounded-md border">
                        <Table>
                            <TableHeader>
                                <TableRow>
                                    <TableHead>Email</TableHead>
                                    <TableHead>Joined At</TableHead>
                                    <TableHead className="text-right">Actions</TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {entries.length === 0 ? (
                                    <TableRow>
                                        <TableCell colSpan={3} className="text-center h-24 text-muted-foreground">
                                            No pending requests.
                                        </TableCell>
                                    </TableRow>
                                ) : (
                                    entries.map((entry) => (
                                        <TableRow key={entry.id}>
                                            <TableCell className="font-medium">{entry.email}</TableCell>
                                            <TableCell>
                                                {entry.createdAt?.seconds
                                                    ? format(new Date(entry.createdAt.seconds * 1000), "PPP p")
                                                    : "Unknown"}
                                            </TableCell>
                                            <TableCell className="text-right">
                                                <Button
                                                    variant="ghost"
                                                    size="icon"
                                                    onClick={() => handleDelete(entry.id, entry.email)}
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
                </CardContent>
            </Card>
        </div>
    );
}

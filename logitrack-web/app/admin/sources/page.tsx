"use client";

import { useEffect, useState } from "react";
import { Plus, MapPin, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { HubDialog } from "../first-mile/hub-dialog";

interface Hub {
    id?: string;
    "Hub Code": string;
    "Hub Name": string;
    "Hub Name TH"?: string;
    source: string;
    lat?: number;
    lng?: number;
}

export default function SourcesPage() {
    const [hubs, setHubs] = useState<Hub[]>([]);
    const [search, setSearch] = useState("");
    const [loading, setLoading] = useState(true);

    const fetchHubs = async () => {
        setLoading(true);
        try {
            const res = await fetch('/api/hubs');
            const data = await res.json();
            if (data.hubs) {
                setHubs(data.hubs);
            }
        } catch (error) {
            console.error("Error fetching sources:", error);
        } finally {
            setLoading(false);
        }
    };

    useEffect(() => {
        fetchHubs();
    }, []);

    const filteredHubs = hubs.filter(hub =>
        (hub["Hub Name"] && hub["Hub Name"].toLowerCase().includes(search.toLowerCase())) ||
        (hub["Hub Code"] && hub["Hub Code"].toLowerCase().includes(search.toLowerCase())) ||
        (hub["Hub Name TH"] && hub["Hub Name TH"].toLowerCase().includes(search.toLowerCase()))
    );

    return (
        <div className="flex-1 space-y-4 p-8 pt-6">
            <div className="flex items-center justify-between space-y-2">
                <h2 className="text-3xl font-bold tracking-tight">Source Management</h2>
                <div className="flex items-center space-x-2">
                    <HubDialog
                        trigger={
                            <Button>
                                <Plus className="mr-2 h-4 w-4" />
                                New Source
                            </Button>
                        }
                        onSuccess={fetchHubs}
                    />
                </div>
            </div>

            <Card>
                <CardHeader>
                    <div className="flex items-center justify-between">
                        <CardTitle>Database Sources</CardTitle>
                        <div className="flex w-full max-w-sm items-center space-x-2">
                            <Search className="h-4 w-4 text-muted-foreground mr-2" />
                            <Input
                                placeholder="Search sources..."
                                value={search}
                                onChange={(e) => setSearch(e.target.value)}
                                className="h-8 w-[250px]"
                            />
                        </div>
                    </div>
                </CardHeader>
                <CardContent>
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>Source ID</TableHead>
                                <TableHead>Name (SPX)</TableHead>
                                <TableHead>Name (Thai)</TableHead>
                                <TableHead>Coordinates</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {loading ? (
                                <TableRow>
                                    <TableCell colSpan={4} className="text-center h-24">
                                        Loading...
                                    </TableCell>
                                </TableRow>
                            ) : filteredHubs.length === 0 ? (
                                <TableRow>
                                    <TableCell colSpan={4} className="text-center h-24">
                                        No database sources found. Create one to get started.
                                    </TableCell>
                                </TableRow>
                            ) : (
                                filteredHubs.map((hub, index) => (
                                    <TableRow key={hub.id || index}>
                                        <TableCell className="font-medium">{hub["Hub Code"]}</TableCell>
                                        <TableCell>{hub["Hub Name"]}</TableCell>
                                        <TableCell>{hub["Hub Name TH"] || "-"}</TableCell>
                                        <TableCell>
                                            {hub.lat && hub.lng ? (
                                                <a
                                                    href={`https://www.google.com/maps/search/?api=1&query=${hub.lat},${hub.lng}`}
                                                    target="_blank"
                                                    rel="noopener noreferrer"
                                                    className="flex items-center text-blue-600 hover:text-blue-800"
                                                >
                                                    <MapPin className="mr-1 h-3 w-3" />
                                                    {hub.lat.toFixed(4)}, {hub.lng.toFixed(4)}
                                                </a>
                                            ) : (
                                                <span className="text-muted-foreground text-xs">No coords</span>
                                            )}
                                        </TableCell>
                                    </TableRow>
                                ))
                            )}
                        </TableBody>
                    </Table>
                </CardContent>
            </Card>
        </div>
    );
}

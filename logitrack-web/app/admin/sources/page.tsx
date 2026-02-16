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
import { collection, getDocs } from "firebase/firestore";
import { db } from "@/firebase/client";
import { useLanguage } from "@/context/language";

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
    const { t } = useLanguage();
    const [hubs, setHubs] = useState<Hub[]>([]);
    const [search, setSearch] = useState("");
    const [loading, setLoading] = useState(true);

    const fetchHubs = async () => {
        setLoading(true);
        try {
            const querySnapshot = await getDocs(collection(db, "hubs"));
            const hubList: Hub[] = querySnapshot.docs.map(doc => {
                const data = doc.data();
                return {
                    'Hub Code': data.hubId || data.hubCode,
                    'Hub Name': data.hubName,
                    'Hub Name TH': data.hubTHName,
                    lat: data.lat,
                    lng: data.lng,
                    source: 'custom',
                    id: doc.id
                } as Hub;
            });
            setHubs(hubList);
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
                <h2 className="text-3xl font-bold tracking-tight">{t("firstMile.sources.title")}</h2>
                <div className="flex items-center space-x-2">
                    <HubDialog
                        trigger={
                            <Button>
                                <Plus className="mr-2 h-4 w-4" />
                                {t("firstMile.sources.newSource")}
                            </Button>
                        }
                        onSuccess={fetchHubs}
                    />
                </div>
            </div>

            <Card>
                <CardHeader>
                    <div className="flex items-center justify-between">
                        <CardTitle>{t("firstMile.sources.dbSources")}</CardTitle>
                        <div className="flex w-full max-w-sm items-center space-x-2">
                            <Search className="h-4 w-4 text-muted-foreground mr-2" />
                            <Input
                                placeholder={t("firstMile.sources.search")}
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
                                <TableHead>{t("firstMile.sources.table.sourceId")}</TableHead>
                                <TableHead>{t("firstMile.sources.table.nameSPX")}</TableHead>
                                <TableHead>{t("firstMile.sources.table.nameThai")}</TableHead>
                                <TableHead>{t("firstMile.sources.table.coordinates")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {loading ? (
                                <TableRow>
                                    <TableCell colSpan={4} className="text-center h-24">
                                        {t("firstMile.sources.loading")}
                                    </TableCell>
                                </TableRow>
                            ) : filteredHubs.length === 0 ? (
                                <TableRow>
                                    <TableCell colSpan={4} className="text-center h-24">
                                        {t("firstMile.sources.noData")}
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
                                                <span className="text-muted-foreground text-xs">{t("firstMile.sources.noCoords")}</span>
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

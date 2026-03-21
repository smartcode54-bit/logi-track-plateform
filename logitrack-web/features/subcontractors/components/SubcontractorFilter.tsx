"use client";

import { useLanguage } from "@/context/language";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Search, Filter } from "lucide-react";

interface SubcontractorFilterProps {
    searchQuery: string;
    setSearchQuery: (v: string) => void;
    statusFilter: string;
    setStatusFilter: (v: string) => void;
    regionFilter: string;
    setRegionFilter: (v: string) => void;
    fleetSizeFilter: string;
    setFleetSizeFilter: (v: string) => void;
    regions: string[];
}

export function SubcontractorFilter(props: SubcontractorFilterProps) {
    const { t } = useLanguage();

    return (
        <div className="flex flex-col lg:flex-row gap-4 bg-card/50 p-4 rounded-lg border border-border/50">
            <div className="relative flex-1">
                <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                <Input
                    placeholder={t("subcontractors.search")}
                    className="pl-10 bg-background/50 border-border/50 focus-visible:ring-1"
                    value={props.searchQuery}
                    onChange={(e) => props.setSearchQuery(e.target.value)}
                />
            </div>
            <div className="flex gap-3 overflow-x-auto pb-1 lg:pb-0">
                <Button variant="outline" size="icon" className="shrink-0 lg:hidden">
                    <Filter className="h-4 w-4" />
                </Button>
                <Select value={props.statusFilter} onValueChange={props.setStatusFilter}>
                    <SelectTrigger className="w-[140px] bg-background/50 border-border/50">
                        <SelectValue placeholder={t("subcontractors.filter.allStatuses")} />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="all">{t("subcontractors.filter.allStatuses")}</SelectItem>
                        <SelectItem value="active">{t("subcontractors.filter.active")}</SelectItem>
                        <SelectItem value="pending">{t("subcontractors.filter.onTrial")}</SelectItem>
                        <SelectItem value="suspended">{t("subcontractors.filter.terminated")}</SelectItem>
                    </SelectContent>
                </Select>

                <Select value={props.regionFilter} onValueChange={props.setRegionFilter}>
                    <SelectTrigger className="w-[160px] bg-background/50 border-border/50">
                        <SelectValue placeholder={t("subcontractors.filter.allRegions")} />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="all">{t("subcontractors.filter.allRegions")}</SelectItem>
                        {props.regions.map(r => (
                            <SelectItem key={r} value={r}>{r}</SelectItem>
                        ))}
                    </SelectContent>
                </Select>

                <Select value={props.fleetSizeFilter} onValueChange={props.setFleetSizeFilter}>
                    <SelectTrigger className="w-[140px] bg-background/50 border-border/50">
                        <SelectValue placeholder={t("subcontractors.filter.fleetSize")} />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="all">{t("subcontractors.filter.allSizes")}</SelectItem>
                        <SelectItem value="small">{t("subcontractors.filter.small")}</SelectItem>
                        <SelectItem value="medium">{t("subcontractors.filter.medium")}</SelectItem>
                        <SelectItem value="large">{t("subcontractors.filter.large")}</SelectItem>
                    </SelectContent>
                </Select>
            </div>
        </div>
    );
}

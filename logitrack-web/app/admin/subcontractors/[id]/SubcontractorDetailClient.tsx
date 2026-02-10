"use client";

import { useEffect, useState } from "react";
import { useParams, useRouter } from "next/navigation";
import { getSubcontractorById, SubcontractorData } from "../actions.client";
import { getTrucksClient, TruckData } from "../../trucks/actions.client";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
    ArrowLeft, Loader2, Building2, User, Phone, Mail, MapPin, Truck,
    Plus, Eye, Edit, LayoutDashboard, FileText, Activity, Calendar, MessageSquare,
    CheckCircle2, Clock, AlertTriangle, Download
} from "lucide-react";
import Link from "next/link";
import { Badge } from "@/components/ui/badge";
import { formatLicensePlate, cn } from "@/lib/utils"; // Added cn import
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";

export default function SubcontractorDetailClient() {
    const params = useParams();
    const router = useRouter();
    const id = params?.id as string;

    const [subcontractor, setSubcontractor] = useState<SubcontractorData | null>(null);
    const [loading, setLoading] = useState(true);
    const [trucks, setTrucks] = useState<TruckData[]>([]);

    useEffect(() => {
        const fetchData = async () => {
            if (!id) return;
            setLoading(true);
            try {
                // Fetch Subcontractor
                const subData = await getSubcontractorById(id);
                setSubcontractor(subData);

                // Fetch Trucks for this Subcontractor
                const allTrucks = await getTrucksClient();
                const subTrucks = allTrucks.filter(t => t.ownershipType === "subcontractor" && t.subcontractorId === id);
                setTrucks(subTrucks);

            } catch (error) {
                console.error("Error loading data:", error);
            } finally {
                setLoading(false);
            }
        };

        fetchData();
    }, [id]);

    if (loading) {
        return (
            <div className="flex items-center justify-center min-h-[600px]">
                <Loader2 className="h-8 w-8 animate-spin text-blue-600" />
            </div>
        );
    }

    if (!subcontractor) {
        return (
            <div className="container mx-auto py-12 text-center">
                <h2 className="text-xl font-semibold mb-4 text-slate-800">Subcontractor not found</h2>
                <Button asChild variant="outline">
                    <Link href="/admin/subcontractors">Back to List</Link>
                </Button>
            </div>
        );
    }

    const NavItem = ({ icon: Icon, label, active = false }: { icon: any, label: string, active?: boolean }) => (
        <button
            className={cn(
                "w-full flex items-center gap-3 px-4 py-3 text-sm font-medium rounded-lg transition-colors",
                active
                    ? "bg-blue-50 text-blue-700 dark:bg-blue-900/20 dark:text-blue-300"
                    : "text-slate-600 hover:bg-slate-50 dark:text-slate-400 dark:hover:bg-slate-800"
            )}
        >
            <Icon className={cn("h-4 w-4", active ? "text-blue-600 dark:text-blue-400" : "text-slate-400")} />
            {label}
        </button>
    );

    return (
        <div className="min-h-screen bg-slate-50/50 dark:bg-slate-950/50 p-6 md:p-8">
            <div className="max-w-7xl mx-auto space-y-6">
                {/* Breadcrumbs */}
                <div className="flex items-center gap-2 text-sm text-slate-500 mb-2">
                    <Link href="/admin/subcontractors" className="hover:text-slate-900 transition-colors">Partners</Link>
                    <span>/</span>
                    <Link href="/admin/subcontractors" className="hover:text-slate-900 transition-colors">Subcontractors</Link>
                    <span>/</span>
                    <span className="font-medium text-slate-900 dark:text-slate-100">{subcontractor.name}</span>
                </div>

                {/* Main Content Grid */}
                <div className="grid grid-cols-1 lg:grid-cols-12 gap-8 items-start">
                    {/* Left Sidebar - Partner Portal */}
                    <div className="lg:col-span-3 space-y-6">
                        <Card className="border-none shadow-sm bg-white dark:bg-card">
                            <div className="p-4 border-b border-slate-100 dark:border-slate-800">
                                <h3 className="font-semibold text-slate-900 dark:text-slate-100">Partner Portal</h3>
                                <p className="text-xs text-slate-500">Subcontractor Management</p>
                            </div>
                            <div className="p-2 space-y-1">
                                <NavItem icon={LayoutDashboard} label="Overview" active />
                                <NavItem icon={Truck} label="Assigned Trucks" />
                                <NavItem icon={FileText} label="Contracts & Documents" />
                                <NavItem icon={Activity} label="Performance" />
                                <NavItem icon={Clock} label="Activity Log" />
                            </div>
                        </Card>

                        {/* Quick Stats Card (Optional, based on design layout logic) */}
                        <div className="bg-blue-600 rounded-xl p-6 text-white shadow-lg shadow-blue-600/20">
                            <h4 className="text-xs font-semibold uppercase opacity-80 mb-1">Total Deliveries</h4>
                            <div className="flex items-baseline gap-2 mb-4">
                                <span className="text-3xl font-bold">1,284</span>
                                <span className="text-sm font-medium bg-white/20 px-1.5 py-0.5 rounded text-white">+12%</span>
                            </div>
                            <div className="h-1 bg-blue-500 rounded-full overflow-hidden">
                                <div className="h-full bg-white w-[85%]" />
                            </div>
                            <p className="text-xs mt-2 opacity-80">Monthly target: 1,500</p>
                        </div>
                    </div>

                    {/* Right Content Area */}
                    <div className="lg:col-span-9 space-y-6">
                        {/* Header Profile Card */}
                        <Card className="border-none shadow-sm bg-white dark:bg-card overflow-hidden">
                            <CardContent className="p-6 md:p-8">
                                <div className="flex flex-col md:flex-row gap-6 items-start justify-between">
                                    <div className="flex items-start gap-6">
                                        <div className="h-24 w-24 rounded-lg bg-slate-100 dark:bg-slate-800 flex items-center justify-center text-slate-400 text-2xl font-serif">
                                            {subcontractor.name.substring(0, 2).toUpperCase()}
                                        </div>
                                        <div>
                                            <div className="flex items-center gap-3 mb-2">
                                                <h1 className="text-2xl font-bold text-slate-900 dark:text-slate-100">{subcontractor.name}</h1>
                                                <Badge className="bg-green-100 text-green-700 hover:bg-green-100 border-none px-2 py-0.5 h-auto">ACTIVE PARTNER</Badge>
                                            </div>
                                            <div className="space-y-1 text-sm text-slate-500 dark:text-slate-400">
                                                <p className="flex items-center gap-2">
                                                    <span className="font-mono text-slate-400">ID: SUB-{subcontractor.id.substring(0, 6).toUpperCase()}</span>
                                                    <span>•</span>
                                                    <span className="text-blue-600 font-medium">Master Subcontractor</span>
                                                </p>
                                                <div className="flex items-center gap-4 mt-2">
                                                    <div className="flex items-center gap-1.5">
                                                        <Calendar className="h-4 w-4" />
                                                        <span>Joined {subcontractor.createdAt ? new Date(subcontractor.createdAt).toLocaleDateString() : 'Jan 2024'}</span>
                                                    </div>
                                                    <div className="flex items-center gap-1.5">
                                                        <MapPin className="h-4 w-4" />
                                                        <span>{subcontractor.address ? subcontractor.address.split(',')[0] : 'Chicago, IL'}</span>
                                                    </div>
                                                </div>
                                            </div>
                                        </div>
                                    </div>
                                    <div className="flex items-center gap-3 w-full md:w-auto mt-4 md:mt-0">
                                        <Button variant="outline" className="flex-1 md:flex-none gap-2">
                                            <MessageSquare className="h-4 w-4" /> Message
                                        </Button>
                                        <Button className="flex-1 md:flex-none gap-2 bg-blue-600 hover:bg-blue-700 text-white" asChild>
                                            <Link href={`/admin/subcontractors/${subcontractor.id}/edit`}>
                                                <Edit className="h-4 w-4" /> Edit Profile
                                            </Link>
                                        </Button>
                                    </div>
                                </div>
                            </CardContent>
                        </Card>

                        {/* Content Tabs Navigation */}
                        <div className="border-b border-slate-200 dark:border-slate-800">
                            <div className="flex items-center gap-6">
                                <button className="px-1 py-3 text-sm font-medium text-blue-600 border-b-2 border-blue-600 flex items-center gap-2">
                                    <LayoutDashboard className="h-4 w-4" /> Overview
                                </button>
                                <button className="px-1 py-3 text-sm font-medium text-slate-500 hover:text-slate-700 flex items-center gap-2">
                                    <Truck className="h-4 w-4" /> Assigned Trucks
                                    <span className="bg-slate-100 text-slate-600 px-1.5 py-0.5 rounded text-xs">{trucks.length}</span>
                                </button>
                                <button className="px-1 py-3 text-sm font-medium text-slate-500 hover:text-slate-700 flex items-center gap-2">
                                    <FileText className="h-4 w-4" /> Contracts & Documents
                                </button>
                            </div>
                        </div>

                        {/* Two Column Layout for Details */}
                        <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                            {/* Company Details */}
                            <div className="space-y-4">
                                <h3 className="font-semibold flex items-center gap-2 text-slate-900 dark:text-slate-100">
                                    <Building2 className="h-4 w-4 text-blue-600" />
                                    Company Details
                                </h3>
                                <div className="bg-white dark:bg-card rounded-xl border border-slate-200 dark:border-slate-800 p-6 space-y-4">
                                    <div className="flex justify-between items-center py-2 border-b border-slate-50 dark:border-slate-800/50">
                                        <span className="text-sm text-slate-500">Legal Name</span>
                                        <span className="text-sm font-medium text-slate-900 dark:text-slate-100">{subcontractor.name}</span>
                                    </div>
                                    <div className="flex justify-between items-center py-2 border-b border-slate-50 dark:border-slate-800/50">
                                        <span className="text-sm text-slate-500">Tax ID (EIN)</span>
                                        <span className="text-sm font-medium text-slate-900 dark:text-slate-100">{subcontractor.taxId || "XX-XXXX882"}</span>
                                    </div>
                                    <div className="flex justify-between items-center py-2 border-b border-slate-50 dark:border-slate-800/50">
                                        <span className="text-sm text-slate-500">Business Type</span>
                                        <span className="text-sm font-medium text-slate-900 dark:text-slate-100">{subcontractor.type === 'company' ? 'Corporation (C-Corp)' : 'Sole Proprietorship'}</span>
                                    </div>
                                    <div className="flex justify-between items-center py-2">
                                        <span className="text-sm text-slate-500">Headquarters</span>
                                        <span className="text-sm font-medium text-slate-900 dark:text-slate-100 text-right max-w-[60%] truncate">{subcontractor.address || "Chicago, Illinois, US"}</span>
                                    </div>
                                </div>
                            </div>

                            {/* Point of Contact */}
                            <div className="space-y-4">
                                <h3 className="font-semibold flex items-center gap-2 text-slate-900 dark:text-slate-100">
                                    <User className="h-4 w-4 text-blue-600" />
                                    Point of Contact
                                </h3>
                                <div className="bg-white dark:bg-card rounded-xl border border-slate-200 dark:border-slate-800 p-6">
                                    <div className="flex items-center gap-4 mb-6">
                                        <Avatar className="h-12 w-12 bg-blue-50 text-blue-600">
                                            <AvatarFallback>{subcontractor.contactPerson.substring(0, 1)}</AvatarFallback>
                                        </Avatar>
                                        <div>
                                            <p className="font-semibold text-slate-900 dark:text-slate-100">{subcontractor.contactPerson}</p>
                                            <p className="text-xs font-medium text-slate-500 uppercase tracking-wide">DISPATCH MANAGER</p>
                                        </div>
                                    </div>
                                    <div className="space-y-3">
                                        <a href={`mailto:${subcontractor.email}`} className="flex items-center gap-3 text-sm text-blue-600 hover:text-blue-700 transition-colors">
                                            <Mail className="h-4 w-4 shrink-0 opacity-70" />
                                            {subcontractor.email || "email@example.com"}
                                        </a>
                                        <a href={`tel:${subcontractor.phone}`} className="flex items-center gap-3 text-sm text-slate-600 dark:text-slate-400 hover:text-slate-900 transition-colors">
                                            <Phone className="h-4 w-4 shrink-0 opacity-70" />
                                            {subcontractor.phone}
                                        </a>
                                        <div className="flex items-center gap-3 text-sm text-slate-600 dark:text-slate-400">
                                            <Clock className="h-4 w-4 shrink-0 opacity-70" />
                                            Available: 08:00 - 18:00 CST
                                        </div>
                                    </div>
                                </div>
                            </div>
                        </div>

                        {/* Assigned Trucks Preview */}
                        <div className="space-y-4">
                            <div className="flex items-center justify-between">
                                <h3 className="font-semibold text-slate-900 dark:text-slate-100">Assigned Trucks</h3>
                                <Link href="#" className="text-sm font-medium text-blue-600 hover:text-blue-700 flex items-center gap-1">
                                    View All Fleet <ArrowLeft className="h-3 w-3 rotate-180" />
                                </Link>
                            </div>
                            <div className="bg-white dark:bg-card rounded-xl border border-slate-200 dark:border-slate-800 overflow-hidden">
                                <div className="overflow-x-auto">
                                    <table className="w-full text-sm text-left">
                                        <thead className="bg-slate-50 dark:bg-slate-900 text-slate-500 font-medium">
                                            <tr>
                                                <th className="px-6 py-3">Truck ID</th>
                                                <th className="px-6 py-3">Type</th>
                                                <th className="px-6 py-3">Plate</th>
                                                <th className="px-6 py-3">Status</th>
                                                <th className="px-6 py-3">Location</th>
                                            </tr>
                                        </thead>
                                        <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
                                            {trucks.slice(0, 3).map((truck) => (
                                                <tr key={truck.id} className="hover:bg-slate-50 dark:hover:bg-slate-900/50 transition-colors">
                                                    <td className="px-6 py-4 font-medium text-blue-600">TRK-{truck.id.substring(0, 4)}</td>
                                                    <td className="px-6 py-4 text-slate-600 dark:text-slate-400">{truck.type}</td>
                                                    <td className="px-6 py-4 font-mono text-slate-600 dark:text-slate-400">{formatLicensePlate(truck.licensePlate)}</td>
                                                    <td className="px-6 py-4">
                                                        <Badge variant="secondary" className={cn(
                                                            "font-normal uppercase text-[10px] tracking-wider",
                                                            truck.truckStatus === 'available' ? "bg-green-100 text-green-700" :
                                                                truck.truckStatus === 'maintenance' ? "bg-orange-100 text-orange-700" :
                                                                    "bg-slate-100 text-slate-600"
                                                        )}>
                                                            {truck.truckStatus}
                                                        </Badge>
                                                    </td>
                                                    <td className="px-6 py-4 text-slate-500">Bangkok, TH</td>
                                                </tr>
                                            ))}
                                            {trucks.length === 0 && (
                                                <tr>
                                                    <td colSpan={5} className="px-6 py-8 text-center text-slate-500">
                                                        No trucks assigned yet.
                                                    </td>
                                                </tr>
                                            )}
                                        </tbody>
                                    </table>
                                </div>
                            </div>
                        </div>

                        {/* Recent Documents */}
                        <div className="space-y-4">
                            <div className="flex items-center justify-between">
                                <h3 className="font-semibold text-slate-900 dark:text-slate-100">Recent Documents</h3>
                                <Button variant="outline" size="sm" className="bg-blue-50 text-blue-600 border-blue-100 hover:bg-blue-100 hover:text-blue-700 dark:bg-blue-900/20 dark:text-blue-400 dark:border-blue-900">
                                    <Plus className="mr-2 h-3 w-3" /> Upload New
                                </Button>
                            </div>
                            <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
                                {/* Document Card 1 */}
                                <div className="bg-white dark:bg-card border border-slate-200 dark:border-slate-800 rounded-xl p-4 flex gap-4 hover:shadow-md transition-shadow">
                                    <div className="h-10 w-10 bg-red-50 text-red-500 rounded-lg flex items-center justify-center shrink-0">
                                        <FileText className="h-5 w-5" />
                                    </div>
                                    <div className="flex-1 min-w-0">
                                        <h4 className="font-medium text-sm text-slate-900 dark:text-slate-100 truncate">Master_Service_Agreement.pdf</h4>
                                        <p className="text-xs text-slate-500 mt-0.5">Added: Dec 12, 2023</p>
                                        <div className="flex items-center gap-3 mt-2 text-xs font-medium">
                                            <button className="text-blue-600 hover:underline">Download</button>
                                            <button className="text-slate-600 hover:text-slate-900 dark:text-slate-400 dark:hover:text-slate-200">View</button>
                                        </div>
                                    </div>
                                </div>

                                {/* Document Card 2 */}
                                <div className="bg-yellow-50 dark:bg-yellow-900/10 border border-yellow-200 dark:border-yellow-900/50 rounded-xl p-4 flex gap-4">
                                    <div className="h-10 w-10 bg-yellow-100 text-yellow-600 dark:bg-yellow-900/50 dark:text-yellow-400 rounded-lg flex items-center justify-center shrink-0">
                                        <AlertTriangle className="h-5 w-5" />
                                    </div>
                                    <div className="flex-1 min-w-0">
                                        <h4 className="font-medium text-sm text-slate-900 dark:text-slate-100 truncate">COI_Insurance_GlobalLog.pdf</h4>
                                        <p className="text-xs text-orange-600 font-medium mt-0.5">Expires in 12 days</p>
                                        <div className="flex items-center gap-3 mt-2 text-xs font-medium">
                                            <button className="text-blue-600 hover:underline">Renew Now</button>
                                            <button className="text-slate-600 hover:text-slate-900 dark:text-slate-400 dark:hover:text-slate-200">View</button>
                                        </div>
                                    </div>
                                </div>

                                {/* Document Card 3 */}
                                <div className="bg-white dark:bg-card border border-slate-200 dark:border-slate-800 rounded-xl p-4 flex gap-4 hover:shadow-md transition-shadow">
                                    <div className="h-10 w-10 bg-blue-50 text-blue-500 rounded-lg flex items-center justify-center shrink-0">
                                        <CheckCircle2 className="h-5 w-5" />
                                    </div>
                                    <div className="flex-1 min-w-0">
                                        <h4 className="font-medium text-sm text-slate-900 dark:text-slate-100 truncate">W9_Tax_Form_Verified.pdf</h4>
                                        <p className="text-xs text-slate-500 mt-0.5">Verified: Jan 15, 2024</p>
                                        <div className="flex items-center gap-3 mt-2 text-xs font-medium">
                                            <button className="text-blue-600 hover:underline">Download</button>
                                            <button className="text-slate-600 hover:text-slate-900 dark:text-slate-400 dark:hover:text-slate-200">Audit</button>
                                        </div>
                                    </div>
                                </div>
                            </div>
                        </div>

                    </div>
                </div>
            </div>
        </div>
    );
}

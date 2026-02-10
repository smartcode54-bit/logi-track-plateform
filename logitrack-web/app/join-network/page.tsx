"use client";

import { useState } from "react";
import { toast } from "sonner";
import { submitPartnerInterest } from "./actions.client";

import Image from "next/image";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { CheckCircle2, Truck, ShieldCheck, FileText, ArrowRight, Wallet, Map, TrendingUp } from "lucide-react";

export default function JoinNetworkPage() {
    const [loading, setLoading] = useState(false);
    const [formData, setFormData] = useState({
        companyName: "",
        contractNumber: "",
        contactName: "",
        email: "",
        serviceArea: "",
        fleetSize: "",
        vehicleType: ""
    });

    const handleChange = (key: string, value: string) => {
        setFormData(prev => ({ ...prev, [key]: value }));
    };

    const handleSubmit = async () => {
        if (!formData.companyName || !formData.contactName || !formData.email) {
            toast.error("Please fill in all required fields");
            return;
        }

        setLoading(true);
        try {
            await submitPartnerInterest({
                companyName: formData.companyName,
                contractNumber: formData.contractNumber,
                contactName: formData.contactName,
                email: formData.email,
                serviceArea: formData.serviceArea,
                fleetSize: formData.fleetSize,
                vehicleType: formData.vehicleType
            });
            toast.success("Application submitted successfully!");
            setFormData({
                companyName: "",
                contractNumber: "",
                contactName: "",
                email: "",
                serviceArea: "",
                fleetSize: "",
                vehicleType: ""
            });
        } catch (error) {
            toast.error("Failed to submit application. Please try again.");
        } finally {
            setLoading(false);
        }
    };

    return (
        <div className="min-h-screen bg-white text-slate-900 font-sans">
            {/* Header */}
            <header className="sticky top-0 z-50 w-full border-b border-slate-200 bg-white/80 backdrop-blur-md">
                <div className="container mx-auto px-4 h-16 flex items-center justify-between">
                    <Link href="/" className="flex items-center gap-2 hover:opacity-80 transition-opacity">
                        <div className="h-8 w-8 bg-blue-600 rounded-lg flex items-center justify-center text-white">
                            <Truck className="h-5 w-5" />
                        </div>
                        <span className="font-bold text-xl text-slate-900">LogisticsPro</span>
                    </Link>
                    <nav className="hidden md:flex items-center gap-8 text-sm font-medium text-slate-600">
                        <Link href="#benefits" className="hover:text-blue-600 transition-colors">Benefits</Link>
                        <Link href="#requirements" className="hover:text-blue-600 transition-colors">Requirements</Link>
                        <Button asChild size="sm" className="bg-blue-600 hover:bg-blue-700 text-white">
                            <Link href="#apply">Join Network</Link>
                        </Button>
                    </nav>
                </div>
            </header>

            <main>
                {/* Hero Section */}
                <section className="py-8 md:py-12 bg-slate-50">
                    <div className="container mx-auto px-4">
                        <div className="flex flex-col lg:flex-row items-center gap-12">
                            <div className="flex-1 space-y-6">
                                <div className="inline-flex items-center rounded-full border border-blue-200 bg-blue-50 px-3 py-1 text-sm text-blue-800">
                                    <span className="flex h-2 w-2 rounded-full bg-blue-600 mr-2"></span>
                                    Now Recruiting Carriers
                                </div>
                                <h1 className="text-4xl md:text-5xl lg:text-6xl font-extrabold tracking-tight text-slate-900 leading-[1.1]">
                                    Partner with the <br />
                                    <span className="text-blue-600">Future of Logistics</span>
                                </h1>
                                <p className="text-lg text-slate-600 max-w-xl leading-relaxed">
                                    Join our network of elite subcontractors. Access consistent volume, AI-powered route optimization, and reliable weekly payments through our next-gen tracking platform.
                                </p>
                                <div className="flex flex-wrap gap-4 pt-4">
                                    <Button size="lg" className="bg-blue-600 hover:bg-blue-700 text-white gap-2 h-12 px-8">
                                        View Routes
                                    </Button>

                                </div>
                            </div>
                            <div className="flex-1 w-full max-w-xl lg:max-w-none">
                                <div className="relative rounded-2xl overflow-hidden shadow-2xl border border-slate-200 bg-white">
                                    {/* Placeholder for truck image */}
                                    <div className="aspect-[4/3] bg-slate-200 relative">
                                        <div className="absolute inset-0 flex items-center justify-center text-slate-400">
                                            <Truck className="h-24 w-24 opacity-20" />
                                        </div>
                                        {/* In a real app, use next/image here */}
                                        <img
                                            src="/trucks-driving-highway-dusk.jpg"
                                            alt="Logistics Truck on Highway"
                                            className="object-cover w-full h-full"
                                        />
                                    </div>
                                </div>
                            </div>
                        </div>
                    </div>
                </section>

                {/* Benefits Section */}
                <section id="benefits" className="py-20 bg-white">
                    <div className="container mx-auto px-4">
                        <div className="text-center max-w-2xl mx-auto mb-16">
                            <h2 className="text-3xl font-bold text-slate-900 mb-4">Why Partner With Us?</h2>
                            <p className="text-slate-600">
                                We provide the technology and volume you need to scale your logistics business efficiently.
                            </p>
                        </div>

                        <div className="grid md:grid-cols-3 gap-8">
                            {/* Benefit 1 */}
                            <div className="p-8 rounded-2xl border border-slate-100 bg-white shadow-sm hover:shadow-md transition-shadow">
                                <div className="h-12 w-12 rounded-xl bg-blue-100 flex items-center justify-center text-blue-600 mb-6">
                                    <Wallet className="h-6 w-6" />
                                </div>
                                <h3 className="text-xl font-bold text-slate-900 mb-3">Weekly Payments</h3>
                                <p className="text-slate-600 leading-relaxed">
                                    Get paid on time, every time. Our transparent automated billing system ensures you never have to chase invoices.
                                </p>
                            </div>

                            {/* Benefit 2 */}
                            <div className="p-8 rounded-2xl border border-slate-100 bg-white shadow-sm hover:shadow-md transition-shadow">
                                <div className="h-12 w-12 rounded-xl bg-indigo-100 flex items-center justify-center text-indigo-600 mb-6">
                                    <Map className="h-6 w-6" />
                                </div>
                                <h3 className="text-xl font-bold text-slate-900 mb-3">Smart Routing</h3>
                                <p className="text-slate-600 leading-relaxed">
                                    Our AI-driven app optimizes your routes in real-time to reduce fuel costs, minimize idle time, and maximize efficiency.
                                </p>
                            </div>

                            {/* Benefit 3 */}
                            <div className="p-8 rounded-2xl border border-slate-100 bg-white shadow-sm hover:shadow-md transition-shadow">
                                <div className="h-12 w-12 rounded-xl bg-emerald-100 flex items-center justify-center text-emerald-600 mb-6">
                                    <TrendingUp className="h-6 w-6" />
                                </div>
                                <h3 className="text-xl font-bold text-slate-900 mb-3">Scalable Volume</h3>
                                <p className="text-slate-600 leading-relaxed">
                                    Gain priority access to a steady stream of shipments from top-tier national shippers and growing local enterprises.
                                </p>
                            </div>
                        </div>
                    </div>
                </section>

                {/* Requirements Section */}
                <section id="requirements" className="py-20 bg-slate-50">
                    <div className="container mx-auto px-4">
                        <div className="flex items-center gap-4 mb-10">
                            <div className="h-8 w-1 bg-blue-600 rounded-full"></div>
                            <h2 className="text-3xl font-bold text-slate-900">Minimum Requirements</h2>
                        </div>

                        <div className="grid md:grid-cols-2 gap-6">
                            <div className="flex gap-4 p-6 bg-white rounded-xl border border-slate-200">
                                <div className="shrink-0 mt-1">
                                    <ShieldCheck className="h-6 w-6 text-blue-600" />
                                </div>
                                <div>
                                    <h4 className="font-bold text-slate-900 mb-1">Insurance</h4>
                                    <p className="text-sm text-slate-600">General Liability & Cargo Insurance ($1M+ Coverage Required)</p>
                                </div>
                            </div>

                            <div className="flex gap-4 p-6 bg-white rounded-xl border border-slate-200">
                                <div className="shrink-0 mt-1">
                                    <Truck className="h-6 w-6 text-blue-600" />
                                </div>
                                <div>
                                    <h4 className="font-bold text-slate-900 mb-1">Vehicle Types</h4>
                                    <p className="text-sm text-slate-600">Sprinter Vans, Box Trucks (16-26ft), and Reefers accepted</p>
                                </div>
                            </div>

                            <div className="flex gap-4 p-6 bg-white rounded-xl border border-slate-200">
                                <div className="shrink-0 mt-1">
                                    <FileText className="h-6 w-6 text-blue-600" />
                                </div>
                                <div>
                                    <h4 className="font-bold text-slate-900 mb-1">Operating Authority</h4>
                                    <p className="text-sm text-slate-600">Active DOT/MC numbers for at least 6 months of operation</p>
                                </div>
                            </div>

                            <div className="flex gap-4 p-6 bg-white rounded-xl border border-slate-200">
                                <div className="shrink-0 mt-1">
                                    <CheckCircle2 className="h-6 w-6 text-blue-600" />
                                </div>
                                <div>
                                    <h4 className="font-bold text-slate-900 mb-1">Safety Record</h4>
                                    <p className="text-sm text-slate-600">Satisfactory safety rating with no major violations in 3 years</p>
                                </div>
                            </div>
                        </div>
                    </div>
                </section>

                {/* Application Form Section */}
                <section id="apply" className="py-24 bg-slate-100 flex justify-center">
                    <div className="container mx-auto px-4 max-w-3xl">
                        <div className="bg-white rounded-2xl shadow-xl overflow-hidden border border-slate-200">
                            <div className="bg-blue-600 p-8 text-center text-white">
                                <h2 className="text-2xl font-bold mb-2">Partner Interest Form</h2>
                                <p className="text-blue-100">Submit your details and our team will review your application within 24 hours.</p>
                            </div>

                            <div className="p-8 md:p-10 space-y-6">
                                <div className="grid md:grid-cols-2 gap-6">
                                    <div className="space-y-2">
                                        <Label htmlFor="companyName">Company Name *</Label>
                                        <Input
                                            id="companyName"
                                            placeholder="Global Logistics LLC"
                                            className="bg-slate-50 border-slate-200"
                                            value={formData.companyName}
                                            onChange={(e) => handleChange("companyName", e.target.value)}
                                        />
                                    </div>
                                    <div className="space-y-2">
                                        <Label htmlFor="contactName">Contact Name *</Label>
                                        <Input
                                            id="contactName"
                                            placeholder="John Doe"
                                            className="bg-slate-50 border-slate-200"
                                            value={formData.contactName}
                                            onChange={(e) => handleChange("contactName", e.target.value)}
                                        />
                                    </div>
                                    <div className="space-y-2">
                                        <Label htmlFor="email">Email Address *</Label>
                                        <Input
                                            id="email"
                                            type="email"
                                            placeholder="john@company.com"
                                            className="bg-slate-50 border-slate-200"
                                            value={formData.email}
                                            onChange={(e) => handleChange("email", e.target.value)}
                                        />
                                    </div>
                                    <div className="space-y-2">
                                        <Label htmlFor="contractNumber">Contract Number</Label>
                                        <Input
                                            id="contractNumber"
                                            placeholder="0987954321"
                                            className="bg-slate-50 border-slate-200"
                                            value={formData.contractNumber}
                                            onChange={(e) => handleChange("contractNumber", e.target.value)}
                                        />
                                    </div>
                                </div>
                                <div className="space-y-2">
                                    <Label htmlFor="serviceArea">Primary Service Area</Label>
                                    <Input
                                        id="serviceArea"
                                        placeholder="Bangkok-GBKK, Upcountry, International"
                                        className="bg-slate-50 border-slate-200"
                                        value={formData.serviceArea}
                                        onChange={(e) => handleChange("serviceArea", e.target.value)}
                                    />
                                </div>

                                <div className="grid md:grid-cols-2 gap-6">
                                    <div className="space-y-2">
                                        <Label htmlFor="fleetSize">Fleet Size</Label>
                                        <Select value={formData.fleetSize} onValueChange={(val) => handleChange("fleetSize", val)}>
                                            <SelectTrigger className="bg-slate-50 border-slate-200">
                                                <SelectValue placeholder="Select size" />
                                            </SelectTrigger>
                                            <SelectContent>
                                                <SelectItem value="1-5">1-5 Vehicles</SelectItem>
                                                <SelectItem value="6-20">6-20 Vehicles</SelectItem>
                                                <SelectItem value="21-50">21-50 Vehicles</SelectItem>
                                                <SelectItem value="50+">50+ Vehicles</SelectItem>
                                            </SelectContent>
                                        </Select>
                                    </div>
                                    <div className="space-y-2">
                                        <Label htmlFor="vehicleType">Primary Vehicle Type</Label>
                                        <Select value={formData.vehicleType} onValueChange={(val) => handleChange("vehicleType", val)}>
                                            <SelectTrigger className="bg-slate-50 border-slate-200">
                                                <SelectValue placeholder="Select type" />
                                            </SelectTrigger>
                                            <SelectContent>
                                                <SelectItem value="box">Pickup</SelectItem>
                                                <SelectItem value="sprinter">4WJ</SelectItem>
                                                <SelectItem value="semi">6WJ</SelectItem>
                                                <SelectItem value="reefer">10W</SelectItem>
                                                <SelectItem value="reefer">All</SelectItem>
                                            </SelectContent>
                                        </Select>
                                    </div>
                                </div>

                                <Button
                                    size="lg"
                                    className="w-full bg-blue-600 hover:bg-blue-700 text-white font-semibold h-12 text-lg"
                                    onClick={handleSubmit}
                                    disabled={loading}
                                >
                                    {loading ? "Submitting..." : "Submit Application"}
                                </Button>
                                <p className="text-xs text-center text-slate-500 mt-4">
                                    By submitting, you agree to our Terms of Service and Privacy Policy.
                                </p>
                            </div>
                        </div>
                    </div>
                </section>
            </main>

            {/* Footer */}
            <footer className="bg-white border-t border-slate-200 py-12">
                <div className="container mx-auto px-4">
                    <div className="grid md:grid-cols-4 gap-8 mb-8">
                        <div className="col-span-1 md:col-span-2">
                            <div className="flex items-center gap-2 mb-4">
                                <div className="h-6 w-6 bg-blue-600 rounded flex items-center justify-center text-white">
                                    <Truck className="h-4 w-4" />
                                </div>
                                <span className="font-bold text-lg text-slate-900">LogisticsPro</span>
                            </div>
                            <p className="text-sm text-slate-500 max-w-xs">
                                The modern standard for logistics subcontracting and carrier management.
                            </p>
                        </div>

                        <div>
                            <h5 className="font-bold text-slate-900 mb-4 text-sm uppercase tracking-wider">Company</h5>
                            <ul className="space-y-2 text-sm text-slate-600">
                                <li><Link href="#" className="hover:text-blue-600">About Us</Link></li>
                                <li><Link href="#" className="hover:text-blue-600">Careers</Link></li>
                                <li><Link href="#" className="hover:text-blue-600">Contact</Link></li>
                            </ul>
                        </div>

                        <div>
                            <h5 className="font-bold text-slate-900 mb-4 text-sm uppercase tracking-wider">Legal</h5>
                            <ul className="space-y-2 text-sm text-slate-600">
                                <li><Link href="#" className="hover:text-blue-600">Privacy</Link></li>
                                <li><Link href="#" className="hover:text-blue-600">Terms</Link></li>
                            </ul>
                        </div>
                    </div>
                    <div className="pt-8 border-t border-slate-100 text-center text-xs text-slate-400">
                        © 2024 WANPEN RADCHADA Transport Co.,Ltd. All rights reserved.
                    </div>
                </div>
            </footer>
        </div>
    );
}

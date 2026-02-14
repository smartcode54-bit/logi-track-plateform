import { Users, MapPin, Package, Zap } from "lucide-react";

interface StatCardProps {
    title: string;
    value: string;
    trend: string;
    isPositive: boolean;
    icon: React.ElementType;
    iconBgColor: string;
    iconColor: string;
}

function StatCard({ title, value, trend, isPositive, icon: Icon, iconBgColor, iconColor }: StatCardProps) {
    return (
        <div className="bg-card border border-border rounded-xl p-6 shadow-sm hover:shadow-md transition-shadow">
            <div className="flex justify-between items-start mb-4">
                <div className={`p-3 rounded-lg ${iconBgColor} ${iconColor}`}>
                    <Icon className="w-6 h-6" />
                </div>
                <div className={`flex items-center gap-1 text-sm font-medium ${isPositive ? 'text-green-500' : 'text-red-500'}`}>
                    <span>{isPositive ? '+' : ''}{trend}</span>
                    <svg
                        xmlns="http://www.w3.org/2000/svg"
                        width="16"
                        height="16"
                        viewBox="0 0 24 24"
                        fill="none"
                        stroke="currentColor"
                        strokeWidth="2"
                        strokeLinecap="round"
                        strokeLinejoin="round"
                        className={isPositive ? "" : "rotate-180"}
                    >
                        <polyline points="23 6 13.5 15.5 8.5 10.5 1 18"></polyline>
                        <polyline points="17 6 23 6 23 12"></polyline>
                    </svg>
                </div>
            </div>
            <div>
                <p className="text-muted-foreground text-xs font-semibold uppercase tracking-wider mb-1">{title}</p>
                <h3 className="text-3xl font-bold text-foreground">{value}</h3>
            </div>
        </div>
    );
}

export function DashboardStats() {
    return (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6 mb-6">
            <StatCard
                title="Total Users"
                value="1,284"
                trend="12.5%"
                isPositive={true}
                icon={Users}
                iconBgColor="bg-blue-500/10"
                iconColor="text-blue-500"
            />
            <StatCard
                title="Active Drivers"
                value="452"
                trend="5.2%"
                isPositive={true}
                icon={MapPin}
                iconBgColor="bg-sky-500/10"
                iconColor="text-sky-500"
            />
            <StatCard
                title="Total Packages"
                value="12,890"
                trend="-2.1%"
                isPositive={false}
                icon={Package}
                iconBgColor="bg-indigo-500/10"
                iconColor="text-indigo-500"
            />
            <StatCard
                title="Fleet Efficiency"
                value="92.8%"
                trend="8.4%"
                isPositive={true}
                icon={Zap}
                iconBgColor="bg-yellow-500/10"
                iconColor="text-yellow-500"
            />
        </div>
    );
}

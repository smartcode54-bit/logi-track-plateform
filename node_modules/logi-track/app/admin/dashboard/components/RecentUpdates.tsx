import { Truck, Users, AlertTriangle, Wrench } from "lucide-react";

const updates = [
    {
        id: 1,
        title: "Truck #4421 Arrived",
        meta: "2 minutes ago • Chicago Hub",
        icon: Truck,
        iconBg: "bg-green-500/10",
        iconColor: "text-green-500",
    },
    {
        id: 2,
        title: "New Driver Onboarded",
        meta: "45 minutes ago • HR Dept",
        icon: Users,
        iconBg: "bg-blue-500/10",
        iconColor: "text-blue-500",
    },
    {
        id: 3,
        title: "Route Delay Reported",
        meta: "1 hour ago • Weather Event",
        icon: AlertTriangle,
        iconBg: "bg-yellow-500/10",
        iconColor: "text-yellow-500",
    },
    {
        id: 4,
        title: "Maintenance Completed",
        meta: "3 hours ago • Seattle Workshop",
        icon: Wrench,
        iconBg: "bg-gray-500/10",
        iconColor: "text-gray-500",
    },
];

export function RecentUpdates() {
    return (
        <div className="bg-card border border-border rounded-xl p-6 shadow-sm col-span-1 h-full">
            <h3 className="text-lg font-semibold text-foreground mb-1">Recent Updates</h3>
            <p className="text-sm text-muted-foreground mb-6">Latest fleet activities and alerts</p>

            <div className="space-y-6">
                {updates.map((item) => (
                    <div key={item.id} className="flex gap-4">
                        <div className={`p-3 rounded-full h-fit ${item.iconBg} ${item.iconColor}`}>
                            <item.icon className="w-5 h-5" />
                        </div>
                        <div>
                            <h4 className="font-medium text-sm text-foreground">{item.title}</h4>
                            <p className="text-xs text-muted-foreground mt-0.5">{item.meta}</p>
                        </div>
                    </div>
                ))}
            </div>

            <div className="mt-8 pt-4 border-t border-border">
                <button className="w-full text-center text-sm font-medium text-blue-500 hover:text-blue-600 transition-colors">
                    View All Notifications
                </button>
            </div>
        </div>
    );
}

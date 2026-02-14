"use client";

import { DashboardStats } from "./components/DashboardStats";
import { ActivityChart } from "./components/ActivityChart";
import { RecentUpdates } from "./components/RecentUpdates";

export default function AdminDashboardPage() {
  return (
    <div className="container mx-auto px-4 py-6 max-w-[1600px]">
      {/* Page Header */}
      <div className="flex flex-col md:flex-row md:items-center justify-between mb-8 gap-4">
        <div>
          <h1 className="text-2xl font-bold text-foreground tracking-tight">
            Dashboard Overview
          </h1>
          <div className="flex items-center gap-2 mt-1">
            <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">REAL-TIME OPERATIONS</span>
            <span className="bg-muted px-1.5 py-0.5 rounded text-[10px] font-bold text-muted-foreground">EN</span>
          </div>
        </div>

        <div className="flex items-center gap-3">
          {/* Right side header actions if any specific to dashboard */}
        </div>
      </div>

      {/* Stats Grid */}
      <DashboardStats />

      {/* Main Content Grid */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6 h-full">
        {/* Chart Section - Takes up 2 columns */}
        <div className="lg:col-span-2 h-full">
          <ActivityChart />
        </div>

        {/* Recent Updates Section - Takes up 1 column */}
        <div className="lg:col-span-1 h-full">
          <RecentUpdates />
        </div>
      </div>
    </div>
  );
}

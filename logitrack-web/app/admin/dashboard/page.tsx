"use client";

import { DashboardStats } from "./components/DashboardStats";
import { ActivityChart } from "./components/ActivityChart";
import { RecentUpdates } from "./components/RecentUpdates";
import { ChatStatusWidget } from "./components/ChatStatusWidget";
import { ExpenseAuditWidget } from "./components/ExpenseAuditWidget";
import { ComplianceSummary } from "./components/ComplianceSummary";
import { CurrentVehiclePosition } from "./components/CurrentVehiclePosition";
import { DashboardVehicleMap } from "./components/DashboardVehicleMap";
import { useLanguage } from "@/context/language";

export default function AdminDashboardPage() {
  const { t, language } = useLanguage();

  return (
    <div className="container mx-auto px-4 py-6 max-w-[1600px]">
      {/* Page Header */}
      <div className="flex flex-col md:flex-row md:items-center justify-between mb-8 gap-4">
        <div>
          <h1 className="text-2xl font-bold text-foreground tracking-tight">
            {t("dashboard.overview")}
          </h1>
          <div className="flex items-center gap-2 mt-1">
            <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t("dashboard.realTimeOperations")}</span>
            <span className="bg-muted px-1.5 py-0.5 rounded text-[10px] font-bold text-muted-foreground">{language.toUpperCase()}</span>
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
        {/* Chart + Map - Takes up 2 columns */}
        <div className="lg:col-span-2 h-full space-y-6">
          <ActivityChart />
          <DashboardVehicleMap />
        </div>

        {/* Recent Updates Section - Takes up 1 column */}
        <div className="lg:col-span-1 h-full space-y-6">
          <CurrentVehiclePosition />
          <ExpenseAuditWidget />
          <ChatStatusWidget />
          <RecentUpdates />
          <ComplianceSummary />
        </div>
      </div>
    </div>
  );
}

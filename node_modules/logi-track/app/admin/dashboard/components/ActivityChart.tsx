"use client";

import { useState } from "react";

export function ActivityChart() {
    const [period, setPeriod] = useState<"Week" | "Month">("Week");

    // Mock data points for the SVG path
    // Simplified smooth curve approximation
    const pathData = "M0 150 C 40 140, 80 100, 120 80 S 200 140, 240 140 S 320 60, 400 40 S 520 80, 560 60 S 640 40, 700 30 L 700 200 L 0 200 Z";
    const lineData = "M0 150 C 40 140, 80 100, 120 80 S 200 140, 240 140 S 320 60, 400 40 S 520 80, 560 60 S 640 40, 700 30";

    return (
        <div className="bg-card border border-border rounded-xl p-6 shadow-sm col-span-1 lg:col-span-2">
            <div className="flex justify-between items-start mb-8">
                <div>
                    <h3 className="text-lg font-semibold text-foreground">Logistics Activity</h3>
                    <p className="text-sm text-muted-foreground">Weekly delivery performance metrics</p>
                </div>
                <div className="flex bg-muted/50 rounded-lg p-1">
                    <button
                        onClick={() => setPeriod("Week")}
                        className={`px-3 py-1 text-xs font-medium rounded-md transition-all ${period === "Week" ? "bg-background shadow-sm text-foreground" : "text-muted-foreground hover:text-foreground"}`}
                    >
                        Week
                    </button>
                    <button
                        onClick={() => setPeriod("Month")}
                        className={`px-3 py-1 text-xs font-medium rounded-md transition-all ${period === "Month" ? "bg-background shadow-sm text-foreground" : "text-muted-foreground hover:text-foreground"}`}
                    >
                        Month
                    </button>
                </div>
            </div>

            <div className="mb-6">
                <h2 className="text-4xl font-bold text-foreground">84,200</h2>
                <div className="flex items-center gap-2 mt-1">
                    <span className="text-green-500 text-sm font-medium">↑ 14%</span>
                    <span className="text-muted-foreground text-sm">vs last week</span>
                </div>
            </div>

            <div className="h-[250px] w-full relative overflow-hidden">
                {/* Chart Area */}
                <svg
                    viewBox="0 0 700 200"
                    className="w-full h-full"
                    preserveAspectRatio="none"
                >
                    <defs>
                        <linearGradient id="chartGradient" x1="0" y1="0" x2="0" y2="1">
                            <stop offset="0%" stopColor="#3b82f6" stopOpacity="0.5" />
                            <stop offset="100%" stopColor="#3b82f6" stopOpacity="0" />
                        </linearGradient>
                    </defs>
                    <path
                        d={pathData}
                        fill="url(#chartGradient)"
                    />
                    <path
                        d={lineData}
                        fill="none"
                        stroke="#3b82f6"
                        strokeWidth="4"
                        strokeLinecap="round"
                    />
                </svg>

                {/* X Axis Labels */}
                <div className="flex justify-between text-xs text-muted-foreground mt-4 px-2">
                    <span>Mon</span>
                    <span>Tue</span>
                    <span>Wed</span>
                    <span>Thu</span>
                    <span>Fri</span>
                    <span>Sat</span>
                    <span>Sun</span>
                </div>
            </div>
        </div>
    );
}

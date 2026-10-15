"use client";

import dynamic from "next/dynamic";
import { PageLoading } from "@/components/page-states";

const DriverPreview = dynamic(() => import("@/features/drivers/components/DriverPreview"), {
    ssr: false,
    loading: () => <PageLoading />,
});

export default function DriverViewPage() {
    return <DriverPreview />;
}

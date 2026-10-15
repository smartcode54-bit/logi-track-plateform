"use client";

import dynamic from "next/dynamic";
import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/trucks/components/RenewalsPage").then((m) => m.RenewalsPage), {
    ssr: false,
    loading: () => <PageLoading />,
});

export default function RenewalsRoute() {
    return <GoPage />;
}

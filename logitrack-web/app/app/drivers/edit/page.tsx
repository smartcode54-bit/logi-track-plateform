"use client";

import dynamic from "next/dynamic";
import { PageLoading } from "@/components/page-states";

const EditDriverForm = dynamic(() => import("@/features/drivers/components/EditDriverForm"), {
    ssr: false,
    loading: () => <PageLoading />,
});

export default function EditDriverPage() {
    return <EditDriverForm />;
}

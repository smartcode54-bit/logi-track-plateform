"use client";

// Mobile release: `GET /v1/app-releases` and `PUT /v1/app-releases/floor` over Go (ADR 0007 guards kept)
// (T54S, pulled forward at the owner's request; developer-spec.md §10.13, §12.2). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/mobile-release/components/MobileReleasePage").then((m) => m.MobileReleasePage), { loading: () => <PageLoading /> });

export default function MobileReleaseRoute() {
    return <GoPage />;
}

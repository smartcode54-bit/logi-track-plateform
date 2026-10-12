"use client";

// API keys: `GET`/`POST`/`DELETE /v1/api-keys` over Go (the secret is shown once) (T54S, pulled forward at
// the owner's request; developer-spec.md §10.13, §12.2). The page is its own chunk.
import dynamic from "next/dynamic";

import { PageLoading } from "@/features/platform/components/PageLoading";

const GoPage = dynamic(() => import("@/features/security-center/components/ApiKeysPage").then((m) => m.ApiKeysPage), { loading: () => <PageLoading /> });

export default function ApiKeysRoute() {
    return <GoPage />;
}

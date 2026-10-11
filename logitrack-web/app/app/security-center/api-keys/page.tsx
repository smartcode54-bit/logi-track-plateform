"use client";

// API keys: `GET`/`POST`/`DELETE /v1/api-keys` over Go (the secret is shown once) while the `security` web
// flag is on; the Firestore page otherwise (T54S, pulled forward at the owner's request; developer-spec.md
// §10.13, §12.2). Each source is its own chunk.
import dynamic from "next/dynamic";

import { DomainPage, PageLoading } from "@/features/platform/components/DomainPage";

const GoPage = dynamic(() => import("@/features/security-center/components/ApiKeysPage").then((m) => m.ApiKeysPage), { loading: () => <PageLoading /> });
const FirestorePage = dynamic(() => import("@/features/security-center/legacy/LegacyApiKeysPage"), { loading: () => <PageLoading /> });

export default function ApiKeysRoute() {
    return <DomainPage domain="security" go={GoPage} firestore={FirestorePage} />;
}

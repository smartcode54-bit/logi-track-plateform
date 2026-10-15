"use client";

import dynamic from "next/dynamic";
import { PageLoading } from "@/features/platform/components/PageLoading";

// Keep the route loaders together so the four small entrypoints share their localized fallback.
export const NewTruckPage = dynamic(() => import("./form/NewTruckWizard"), { ssr: false, loading: () => <PageLoading /> });
export const EditTruckPage = dynamic(() => import("./form/EditTruckForm"), { ssr: false, loading: () => <PageLoading /> });
export const TruckViewPage = dynamic(() => import("./TruckPreview"), { ssr: false, loading: () => <PageLoading /> });
export const RenewTruckPage = dynamic(() => import("./RenewalDashboard"), { ssr: false, loading: () => <PageLoading /> });

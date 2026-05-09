"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

/**
 * Redirect legacy /admin/users to Security Center User Management
 */
export default function AdminUsersRedirectPage() {
    const router = useRouter();

    useEffect(() => {
        router.replace("/app/security-center/users");
    }, [router]);

    return null;
}

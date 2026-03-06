"use client";

import { useState, useEffect } from "react";
import { useAuth } from "@/context/auth";
import { doc, getDoc } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { DEFAULT_ROLE_CAPABILITIES, RoleId } from "@/lib/roles";
import { CapabilityId } from "@/lib/capabilities";

export function usePermission(capabilityId: CapabilityId) {
    const auth = useAuth() || {};
    const { customClaims, loading: authLoading } = auth as any;
    const [hasPermission, setHasPermission] = useState<boolean>(false);
    const [loading, setLoading] = useState<boolean>(true);

    useEffect(() => {
        let isMounted = true;

        async function checkPermission() {
            if (authLoading) return;

            // Not logged in
            if (!customClaims) {
                if (isMounted) {
                    setHasPermission(false);
                    setLoading(false);
                }
                return;
            }

            // Always grant full access to admins via customClaims or role
            if (customClaims.admin || customClaims.role === "admin") {
                if (isMounted) {
                    setHasPermission(true);
                    setLoading(false);
                }
                return;
            }

            const role = (customClaims.role || "user") as RoleId;

            // Get default capabilities for the role
            const defaultCaps = DEFAULT_ROLE_CAPABILITIES[role] || [];
            const defaultHasAccess = Array.isArray(defaultCaps)
                ? defaultCaps.includes(capabilityId)
                : defaultCaps === "*";

            try {
                // Try to load any overrides from Firestore
                const ref = doc(db, COLLECTIONS.PERMISSIONS_CONFIG, role);
                const snap = await getDoc(ref);

                if (snap.exists() && snap.data()?.capabilities) {
                    const capabilities = snap.data().capabilities as Record<string, boolean>;

                    if (capabilityId in capabilities) {
                        // Use Firestore override if it exists
                        if (isMounted) {
                            setHasPermission(capabilities[capabilityId]);
                            setLoading(false);
                        }
                        return;
                    }
                }

                // Fallback to default if no explicit override in Firestore
                if (isMounted) {
                    setHasPermission(defaultHasAccess);
                    setLoading(false);
                }
            } catch (err) {
                console.error("[usePermission] Error checking permission:", err);
                // Fallback to default
                if (isMounted) {
                    setHasPermission(defaultHasAccess);
                    setLoading(false);
                }
            }
        }

        checkPermission();

        return () => {
            isMounted = false;
        };
    }, [capabilityId, customClaims, authLoading]);

    return { hasPermission, loading };
}

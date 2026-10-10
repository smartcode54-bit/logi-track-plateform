"use client";

/**
 * Forgot password through Go (T18; Appendix C §C.4.9; R4): `POST /api/go/v1/auth/password/forgot
 * {email, locale}` always answers 202, whether or not the address has an account, so the page shows
 * the same message either way. The emailed link opens `/reset-password#token=...`.
 */
import { useState } from "react";

import { useLanguage } from "@/context/language";
import { apiErrorText } from "@/lib/apiError";
import { requestPasswordReset } from "@/lib/authClient";

export function useForgotPassword() {
    const { t, language } = useLanguage();
    const [email, setEmail] = useState("");
    const [loading, setLoading] = useState(false);
    const [sent, setSent] = useState(false);
    const [error, setError] = useState("");

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        setLoading(true);
        setError("");
        try {
            await requestPasswordReset(email.trim(), language);
            setSent(true);
        } catch (err) {
            setError(apiErrorText(err, t));
        } finally {
            setLoading(false);
        }
    };

    return { email, setEmail, loading, sent, error, handleSubmit, t };
}

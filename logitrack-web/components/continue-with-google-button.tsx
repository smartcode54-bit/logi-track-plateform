"use client";

/**
 * The stand-alone Google sign-in of the landing page (T18). The Firebase popup is gone: the GIS
 * button signs in through `POST /api/auth/google` (features/auth/components/GoogleSignInButton.tsx),
 * then `/app` sends the user to the role's home (proxy.ts, R89). A `must_change_password` account
 * sets its new password in a dialog first (R79) and then signs in again.
 */
import { useCallback, useState } from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";

import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { ChangePasswordForm } from "@/features/auth/components/ChangePasswordForm";
import { GoogleSignInButton } from "@/features/auth/components/GoogleSignInButton";
import { apiErrorText } from "@/lib/apiError";
import { passwordChangeTicket } from "@/lib/authClient";
import { APP_PATH } from "@/lib/sessionEnd";

export default function ContinueWithGoogleButton() {
    const auth = useAuth();
    const router = useRouter();
    const { t } = useLanguage();
    const [ticket, setTicket] = useState<string | null>(null);

    const signIn = useCallback(
        async (idToken: string, nonce: string) => {
            if (!auth) return;
            try {
                await auth.loginWithGoogle(idToken, nonce);
                router.push(APP_PATH);
            } catch (err) {
                const changeTicket = passwordChangeTicket(err);
                if (changeTicket) setTicket(changeTicket);
                else toast.error(apiErrorText(err, t));
                throw err;
            }
        },
        [auth, router, t]
    );

    return (
        <>
            <GoogleSignInButton onCredential={signIn} />
            <Dialog open={ticket !== null} onOpenChange={(open) => !open && setTicket(null)}>
                <DialogContent className="sm:max-w-[425px]">
                    <DialogHeader>
                        <DialogTitle>{t("auth.changePassword.title")}</DialogTitle>
                        <DialogDescription>{t("auth.changePassword.subtitle")}</DialogDescription>
                    </DialogHeader>
                    {ticket ? (
                        <ChangePasswordForm
                            ticket={ticket}
                            idPrefix="google-chg"
                            onChanged={() => {
                                setTicket(null);
                                toast.success(t("auth.changePassword.done"));
                            }}
                            onCancel={(messageKey) => {
                                setTicket(null);
                                if (messageKey) toast.error(t(messageKey));
                            }}
                        />
                    ) : null}
                </DialogContent>
            </Dialog>
        </>
    );
}

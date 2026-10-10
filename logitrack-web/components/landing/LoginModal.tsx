"use client";

import { useRouter } from "next/navigation";

import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogHeader,
    DialogTitle,
    DialogTrigger,
} from "@/components/ui/dialog";
import { useLanguage } from "@/context/language";
import { SignInPanel } from "@/features/auth/components/SignInPanel";
import { APP_PATH } from "@/lib/sessionEnd";

interface LoginModalProps {
    children?: React.ReactNode;
    open?: boolean;
    onOpenChange?: (open: boolean) => void;
}

/**
 * The landing page's login dialog: the shared sign-in panel (T18, Go session through the BFF). After a
 * sign-in `/app` sends the user to the role's home (proxy.ts, R89).
 */
export function LoginModal({ children, open, onOpenChange }: LoginModalProps) {
    const { t } = useLanguage();
    const router = useRouter();

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogTrigger asChild>{children}</DialogTrigger>
            <DialogContent className="sm:max-w-[425px]">
                <DialogHeader>
                    <DialogTitle>{t("auth.login.title")}</DialogTitle>
                    <DialogDescription>{t("auth.login.subtitle")}</DialogDescription>
                </DialogHeader>
                <div className="py-2">
                    <SignInPanel
                        idPrefix="modal-login"
                        onSignedIn={() => {
                            onOpenChange?.(false);
                            router.push(APP_PATH);
                        }}
                    />
                </div>
            </DialogContent>
        </Dialog>
    );
}

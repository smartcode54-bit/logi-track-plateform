"use client";

import { useState } from "react";
import ContinueWithGoogleButton from "@/components/continue-with-google-button";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogHeader,
    DialogTitle,
    DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import Link from "next/link";
import { useLanguage } from "@/context/language";
import { useAuth } from "@/context/auth";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { Loader2 } from "lucide-react";

interface LoginModalProps {
    children?: React.ReactNode;
    open?: boolean;
    onOpenChange?: (open: boolean) => void;
}

export function LoginModal({ children, open, onOpenChange }: LoginModalProps) {
    const { t } = useLanguage();
    const auth = useAuth();
    const router = useRouter();
    const [email, setEmail] = useState("");
    const [password, setPassword] = useState("");
    const [loading, setLoading] = useState(false);

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        setLoading(true);

        try {
            if (!auth) {
                throw new Error("Auth context not initialized");
            }
            if (auth) {
                await auth.login(email, password);
            }
            // Close modal if successful
            if (onOpenChange) onOpenChange(false);

            router.push("/admin/dashboard");
            toast.success("Logged in successfully");
        } catch (error: any) {
            console.error("Login error:", error);
            toast.error(error.message || "Failed to login");
        } finally {
            setLoading(false);
        }
    };

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogTrigger asChild>
                {children}
            </DialogTrigger>
            <DialogContent className="sm:max-w-[425px]">
                <DialogHeader>
                    <DialogTitle>{t("auth.login.title") || "Login"}</DialogTitle>
                    <DialogDescription>
                        {t("auth.login.subtitle") || "Enter your email below to login to your account"}
                    </DialogDescription>
                </DialogHeader>
                <div className="grid gap-4 py-4">
                    <form onSubmit={handleSubmit} className="space-y-4">
                        <div className="space-y-2">
                            <Label htmlFor="email">{t("auth.email") || "Email"}</Label>
                            <Input
                                id="email"
                                type="email"
                                placeholder="admin@cjexpress.co.th"
                                required
                                value={email}
                                onChange={(e) => setEmail(e.target.value)}
                            />
                        </div>
                        <div className="space-y-2">
                            <div className="flex items-center justify-between">
                                <Label htmlFor="password">{t("auth.password") || "Password"}</Label>
                                <Link
                                    href="/forgot-password"
                                    className="text-sm font-medium text-muted-foreground hover:text-primary transition-colors"
                                >
                                    {t("auth.forgotPassword") || "Forgot your password?"}
                                </Link>
                            </div>
                            <Input
                                id="password"
                                type="password"
                                required
                                value={password}
                                onChange={(e) => setPassword(e.target.value)}
                            />
                        </div>

                        <Button type="submit" className="w-full" disabled={loading}>
                            {loading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                            {t("auth.login.title") || "Login"}
                        </Button>
                    </form>

                    <div className="relative">
                        <div className="absolute inset-0 flex items-center">
                            <span className="w-full border-t" />
                        </div>
                        <div className="relative flex justify-center text-xs uppercase">
                            <span className="bg-background px-2 text-muted-foreground">
                                {t("auth.or") || "or"}
                            </span>
                        </div>
                    </div>
                    <ContinueWithGoogleButton />
                </div>
            </DialogContent>
        </Dialog>
    );
}

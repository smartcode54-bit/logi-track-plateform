"use client";

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
import { useState } from "react";
import { useLanguage } from "@/context/language";
import { toast } from "sonner";
import { Loader2, Mail } from "lucide-react";
import { collection, addDoc, serverTimestamp } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";

interface WaitlistModalProps {
    children?: React.ReactNode;
    open?: boolean;
    onOpenChange?: (open: boolean) => void;
    onSwitchToLogin?: () => void;
}

export function WaitlistModal({ children, open, onOpenChange, onSwitchToLogin }: WaitlistModalProps) {
    const [email, setEmail] = useState("");
    const [loading, setLoading] = useState(false);
    const [submitted, setSubmitted] = useState(false);
    const { t } = useLanguage();

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        setLoading(true);

        try {
            if (!email) {
                toast.error("Please enter your email address");
                setLoading(false);
                return;
            }

            await addDoc(collection(db, COLLECTIONS.WAITLIST), {
                email,
                createdAt: serverTimestamp(),
            });

            setSubmitted(true);
            toast.success(t("auth.waitlist.successToast") || "Successfully joined the waitlist!");
        } catch (error: any) {
            console.error("Error joining waitlist:", error);
            toast.error(t("auth.waitlist.errorToast") || "Failed to join waitlist. Please try again.");
        } finally {
            setLoading(false);
        }
    };

    const resetForm = () => {
        setSubmitted(false);
        setEmail("");
        if (onOpenChange) onOpenChange(false);
    }

    return (
        <Dialog open={open} onOpenChange={(val) => {
            if (!val) {
                // Reset state when closed if needed, or just let it persist
                if (submitted) {
                    setTimeout(() => {
                        setSubmitted(false);
                        setEmail("");
                    }, 300);
                }
            }
            if (onOpenChange) onOpenChange(val);
        }}>
            <DialogTrigger asChild>
                {children}
            </DialogTrigger>
            <DialogContent className="sm:max-w-[425px]">
                {submitted ? (
                    <div className="text-center py-6">
                        <div className="mx-auto w-12 h-12 bg-green-100 dark:bg-green-900 rounded-full flex items-center justify-center mb-4">
                            <Mail className="w-6 h-6 text-green-600 dark:text-green-300" />
                        </div>
                        <h2 className="text-2xl font-bold mb-2">{t("auth.waitlist.successTitle") || "You're on the list!"}</h2>
                        <p className="text-muted-foreground mb-6">
                            {t("auth.waitlist.successDesc") || "Thank you for your interest. We'll verify your information and contact you when your account is ready."}
                        </p>
                        <Button onClick={resetForm} className="w-full">
                            {t("auth.backToLogin") || "Back to Login"}
                        </Button>
                    </div>
                ) : (
                    <>
                        <DialogHeader>
                            <DialogTitle>{t("auth.register.title") || "Join Waitlist"}</DialogTitle>
                            <DialogDescription>
                                {t("auth.waitlist.description") || "Registration is currently by invitation only. Join the waitlist to request access."}
                            </DialogDescription>
                        </DialogHeader>
                        <div className="grid gap-4 py-4">
                            <form onSubmit={handleSubmit} className="space-y-4">
                                <div className="space-y-2">
                                    <Label htmlFor="waitlist-email">{t("auth.email") || "Email"}</Label>
                                    <Input
                                        id="waitlist-email"
                                        type="email"
                                        placeholder="name@company.com"
                                        value={email}
                                        onChange={(e) => setEmail(e.target.value)}
                                        required
                                        className="bg-muted/5 border-muted-foreground/20 focus-visible:ring-primary"
                                    />
                                </div>

                                <Button type="submit" className="w-full" disabled={loading}>
                                    {loading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                                    {t("auth.waitlist.submit") || "Request Access"}
                                </Button>
                            </form>
                            <div className="flex justify-center pt-2">
                                <Button variant="link" onClick={() => onSwitchToLogin?.()} className="text-sm text-muted-foreground">
                                    {t("auth.alreadyHaveAccount") || "Already have an account? Sign In"}
                                </Button>
                            </div>
                        </div>
                    </>
                )}
            </DialogContent>
        </Dialog>
    );
}

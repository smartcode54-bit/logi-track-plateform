"use client";

import { useAuth } from "@/context/auth";
import { useRouter, usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import Navigation from "@/components/navigation";
import { SidebarProvider, SidebarTrigger, SidebarInset } from "@/components/ui/sidebar";
import { AppSidebar } from "@/components/app-sidebar";
import { Separator } from "@/components/ui/separator";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import {
    Breadcrumb,
    BreadcrumbItem,
    BreadcrumbLink,
    BreadcrumbList,
    BreadcrumbPage,
    BreadcrumbSeparator,
} from "@/components/ui/breadcrumb";
import { House, Sun, Moon, Check, Loader2 } from "lucide-react";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuGroup,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useLanguage } from "@/context/language";
import { BreadcrumbProvider, useBreadcrumb } from "@/context/breadcrumb";
import Link from "next/link"; // Ensure Link is imported

function DynamicBreadcrumb({ pathname }: { pathname: string }) {
    const { customLastItem } = useBreadcrumb();

    // Split path and filter out empty strings and 'admin' (since Home covers it)
    const segments = pathname.split('/').filter(Boolean).filter(seg => seg !== 'admin');

    return (
        <Breadcrumb className="hidden md:flex">
            <BreadcrumbList>
                <BreadcrumbItem>
                    <BreadcrumbLink asChild>
                        <Link href="/admin/dashboard" className="flex items-center gap-1">
                            <House className="h-4 w-4" />
                            Home
                        </Link>
                    </BreadcrumbLink>
                </BreadcrumbItem>
                <BreadcrumbSeparator />

                {segments.map((segment, index) => {
                    const isLast = index === segments.length - 1;
                    const href = `/admin/${segments.slice(0, index + 1).join('/')}`;

                    // Format segment name: "maintenance-logs" -> "Maintenance Logs"
                    const name = segment.replace(/-/g, ' ').replace(/\b\w/g, l => l.toUpperCase());

                    // Use customLastItem if it's the last item and value is set
                    const displayName = (isLast && customLastItem) ? customLastItem : name;

                    return (
                        <div key={href} className="flex items-center">
                            <BreadcrumbItem>
                                {isLast ? (
                                    <BreadcrumbPage className="font-semibold text-foreground">
                                        {displayName}
                                    </BreadcrumbPage>
                                ) : (
                                    <BreadcrumbLink asChild>
                                        <Link href={href}>{displayName}</Link>
                                    </BreadcrumbLink>
                                )}
                            </BreadcrumbItem>
                            {!isLast && <BreadcrumbSeparator className="ml-2" />}
                        </div>
                    );
                })}
            </BreadcrumbList>
        </Breadcrumb>
    );
}

export default function AdminLayout({
    children,
}: {
    children: React.ReactNode;
}) {
    const authContext = useAuth();
    const router = useRouter();
    const currentUser = authContext?.currentUser;
    const pathname = usePathname();
    const { language, setLanguage } = useLanguage();

    const isDashboard = pathname === "/admin/dashboard";

    const [isDark, setIsDark] = useState(false);

    // Theme toggle logic
    useEffect(() => {
        const savedTheme = localStorage.getItem('theme');
        const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
        const shouldBeDark = savedTheme === 'dark' || (!savedTheme && prefersDark);
        setIsDark(shouldBeDark);
        if (shouldBeDark) document.documentElement.classList.add('dark');
        else document.documentElement.classList.remove('dark');
    }, []);

    // Auth Redirect Logic
    useEffect(() => {
        if (!authContext?.loading && !currentUser) {
            router.push("/login");
        }
    }, [currentUser, authContext?.loading, router]);

    const toggleTheme = () => {
        const newIsDark = !isDark;
        setIsDark(newIsDark);
        if (newIsDark) {
            document.documentElement.classList.add('dark');
            localStorage.setItem('theme', 'dark');
        } else {
            document.documentElement.classList.remove('dark');
            localStorage.setItem('theme', 'light');
        }
        window.dispatchEvent(new Event('themechange'));
    };

    if (!authContext || authContext.loading) {
        return (
            <div className="min-h-screen bg-background flex flex-col items-center justify-center">
                <div className="flex flex-col items-center gap-4">
                    <Loader2 className="h-8 w-8 animate-spin text-primary" />
                    <div className="text-center text-muted-foreground">Loading / กำลังโหลด...</div>
                </div>
            </div>
        );
    }

    if (!currentUser) {
        return null; // Will redirect in useEffect
    }

    return (
        <BreadcrumbProvider>
            <SidebarProvider defaultOpen={false}>
                <AppSidebar />
                <SidebarInset>
                    <header className="flex h-16 shrink-0 items-center justify-between gap-2 border-b bg-background px-4 transition-[width,height] ease-linear group-has-[[data-collapsible=icon]]/sidebar-wrapper:h-12">
                        <div className="flex items-center gap-4 flex-1">
                            <SidebarTrigger className="-ml-1" />
                            <Separator orientation="vertical" className="mr-2 h-4" />


                            <div className="relative w-full max-w-md flex items-center">
                                {isDashboard ? (
                                    <div className="relative w-full hidden md:block">
                                        <svg
                                            className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground"
                                            xmlns="http://www.w3.org/2000/svg"
                                            fill="none"
                                            viewBox="0 0 24 24"
                                            stroke="currentColor"
                                        >
                                            <path
                                                strokeLinecap="round"
                                                strokeLinejoin="round"
                                                strokeWidth={2}
                                                d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"
                                            />
                                        </svg>
                                        <input
                                            type="text"
                                            placeholder="Search data..."
                                            className="h-9 w-full rounded-md border border-input bg-muted/50 px-9 py-1 text-sm shadow-sm transition-colors file:border-0 file:bg-transparent file:text-sm file:font-medium placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
                                        />
                                    </div>
                                ) : (
                                    <DynamicBreadcrumb pathname={pathname} />
                                )}
                            </div>
                        </div>

                        <div className="flex items-center gap-4 px-4">
                            {isDashboard && (
                                <div className="flex items-center gap-2">
                                    <span className="text-xs font-medium text-muted-foreground uppercase tracking-wider hidden md:inline-block">Real-Time Operations</span>
                                </div>
                            )}

                            <button
                                onClick={toggleTheme}
                                className="flex items-center justify-center w-8 h-8 rounded-full hover:bg-accent transition-colors cursor-pointer"
                                title={isDark ? "Switch to light mode" : "Switch to dark mode"}
                            >
                                {isDark ? (
                                    <Sun className="w-5 h-5 text-muted-foreground" />
                                ) : (
                                    <Moon className="w-5 h-5 text-muted-foreground" />
                                )}
                            </button>

                            {isDashboard && (
                                <>
                                    <button className="hidden md:flex items-center gap-2 bg-blue-600 hover:bg-blue-700 text-white px-4 py-2 rounded-md text-sm font-medium transition-colors">
                                        <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M5 12h14" /><path d="M12 5v14" /></svg>
                                        Generate Report
                                    </button>

                                    <button className="relative p-2 text-muted-foreground hover:text-foreground transition-colors">
                                        <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9" /><path d="M10.3 21a1.94 1.94 0 0 0 3.4 0" /></svg>
                                        <span className="absolute top-2 right-2 h-2 w-2 rounded-full bg-red-500 border-2 border-background"></span>
                                    </button>

                                    <div className="h-8 w-[1px] bg-border mx-2"></div>
                                </>
                            )}

                            <DropdownMenu>
                                <DropdownMenuTrigger asChild>
                                    <button className="flex items-center gap-3 hover:bg-muted/50 p-2 rounded-lg transition-colors outline-none">
                                        <div className="text-right hidden sm:block">
                                            <p className="text-sm font-medium leading-none">{currentUser?.displayName || "Alex Rivera"}</p>
                                            <p className="text-xs text-muted-foreground mt-1">Administrator</p>
                                        </div>
                                        <Avatar className="h-9 w-9 border border-border">
                                            <AvatarImage src={currentUser?.photoURL || ""} alt={currentUser?.displayName || "User"} className="object-cover" />
                                            <AvatarFallback className="bg-orange-200 text-orange-700 font-semibold">
                                                {currentUser?.displayName?.[0] || "A"}
                                            </AvatarFallback>
                                        </Avatar>
                                    </button>
                                </DropdownMenuTrigger>
                                <DropdownMenuContent align="end" className="w-56">
                                    <DropdownMenuLabel>My Account</DropdownMenuLabel>
                                    <DropdownMenuSeparator />
                                    <DropdownMenuGroup>
                                        <DropdownMenuItem>
                                            Profile
                                        </DropdownMenuItem>
                                        <DropdownMenuItem>
                                            Settings
                                        </DropdownMenuItem>
                                    </DropdownMenuGroup>
                                    <DropdownMenuSeparator />
                                    <DropdownMenuLabel>Language</DropdownMenuLabel>
                                    <DropdownMenuGroup>
                                        <DropdownMenuItem onClick={() => setLanguage('en')} className="justify-between">
                                            <span>English</span>
                                            {language === 'en' && <Check className="h-4 w-4" />}
                                        </DropdownMenuItem>
                                        <DropdownMenuItem onClick={() => setLanguage('th')} className="justify-between">
                                            <span>ไทย</span>
                                            {language === 'th' && <Check className="h-4 w-4" />}
                                        </DropdownMenuItem>
                                    </DropdownMenuGroup>
                                    <DropdownMenuSeparator />
                                    <DropdownMenuItem className="text-red-500 focus:text-red-500" onClick={async () => {
                                        await authContext.logout();
                                        window.location.href = "/";
                                    }}>
                                        Log out
                                    </DropdownMenuItem>
                                </DropdownMenuContent>
                            </DropdownMenu>
                        </div>
                    </header>
                    <div className="flex flex-1 flex-col gap-4 p-4">
                        {children}
                    </div>
                </SidebarInset>
            </SidebarProvider>
        </BreadcrumbProvider>
    );
}

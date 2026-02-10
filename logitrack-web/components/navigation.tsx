"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useAuth } from "@/context/auth";
import { Sun, Moon, Flame, User, LayoutDashboard, LogOut } from "lucide-react";
import { useEffect, useState } from "react";
import { Avatar, AvatarImage, AvatarFallback } from "@/components/ui/avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { LanguageSwitcher } from "./language-switcher";
import { useLanguage } from "@/context/language";




export default function Navigation() {
  const authContext = useAuth();
  const router = useRouter();
  const currentUser = authContext?.currentUser;
  const [isDark, setIsDark] = useState(false);
  const { t } = useLanguage();

  useEffect(() => {
    // Check localStorage for saved theme preference
    const savedTheme = localStorage.getItem('theme');
    const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
    const shouldBeDark = savedTheme === 'dark' || (!savedTheme && prefersDark);

    setIsDark(shouldBeDark);
    // Apply theme immediately
    const html = document.documentElement;
    if (shouldBeDark) {
      html.classList.add('dark');
    } else {
      html.classList.remove('dark');
    }
  }, []);

  const toggleTheme = () => {
    const newIsDark = !isDark;
    setIsDark(newIsDark);

    const html = document.documentElement;
    if (newIsDark) {
      html.classList.add('dark');
      localStorage.setItem('theme', 'dark');
    } else {
      html.classList.remove('dark');
      localStorage.setItem('theme', 'light');
    }

    // Force re-render to update all components
    window.dispatchEvent(new Event('themechange'));
  };

  // 🔓 Logout Function - ใช้ logout จาก Context
  const handleLogout = async () => {
    try {
      // ใช้ logout function จาก Context
      await authContext?.logout();

      // Redirect ไปหน้า home
      router.push("/");

      // Navigation bar จะแสดง "Login | Register" แทน
      // (currentUser จะเป็น null อัตโนมัติจาก onAuthStateChanged)
    } catch (error) {
      console.error("Error signing out:", error);
      // Handle error (optional: show error message to user)
    }
  };

  return (
    <nav className="bg-green-800 text-white p-4 sticky top-0 z-50 shadow-md">
      <div className="w-full flex items-center justify-between px-4">
        <div className="text-2xl font-semibold flex items-center gap-2 font-poppins">
          <Flame className="w-5 h-5" />
          Logi Track
        </div>

        <div className="flex items-center gap-4">
          <LanguageSwitcher />

          {/* Dark/Light Mode Toggle */}
          <button
            onClick={toggleTheme}
            className="flex items-center justify-center w-8 h-8 rounded-full bg-white/20 hover:bg-white/30 transition-colors cursor-pointer"
            title={isDark ? "Switch to light mode" : "Switch to dark mode"}
          >
            {isDark ? (
              <Sun className="w-5 h-5 text-white" />
            ) : (
              <Moon className="w-5 h-5 text-white" />
            )}
          </button>

          {currentUser ? (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <button className="flex items-center gap-2 hover:opacity-80 transition-opacity cursor-pointer">
                  <Avatar className="w-8 h-8">
                    <AvatarImage
                      src={currentUser.photoURL || undefined}
                      alt={currentUser.displayName || currentUser.email || "User"}
                    />
                    <AvatarFallback className="bg-white/20 text-white text-xs">
                      {currentUser.displayName
                        ? currentUser.displayName.split(" ").map(n => n[0]).join("").toUpperCase().slice(0, 2)
                        : currentUser.email?.[0].toUpperCase() || "U"
                      }
                    </AvatarFallback>
                  </Avatar>
                  <span className="text-sm">
                    {/* Hi, {currentUser.displayName || currentUser.email} */}
                  </span>
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-56">
                <DropdownMenuLabel>
                  {currentUser.displayName || currentUser.email}
                </DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem asChild>
                  <Link href="/my-account" className="flex items-center cursor-pointer">
                    <User className="mr-2 h-4 w-4" />
                    <span>{t('nav.myAccount')}</span>
                  </Link>
                </DropdownMenuItem>

                {!!authContext?.customClaims?.admin && (

                  <DropdownMenuItem asChild>
                    <Link href="/admin/dashboard" className="flex items-center cursor-pointer">
                      <LayoutDashboard className="mr-2 h-4 w-4" />
                      <span>{t('nav.adminDashboard')}</span>
                    </Link>
                  </DropdownMenuItem>
                )}

                <DropdownMenuItem asChild>
                  <Link href="/my-account" className="flex items-center cursor-pointer">
                    <User className="mr-2 h-4 w-4" />
                    <span>{t('nav.myFavorite')}</span>
                  </Link>
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem
                  onClick={handleLogout}
                  className="flex items-center cursor-pointer text-red-600 focus:text-red-600"
                >
                  <LogOut className="mr-2 h-4 w-4" />
                  <span>{t('nav.logout')}</span>
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          ) : (
            <ul className="flex items-center gap-4">
            </ul>
          )}
        </div>
      </div>
    </nav>
  );
}

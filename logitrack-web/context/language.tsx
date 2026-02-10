"use client";

import React, { createContext, useContext, useEffect, useState } from "react";

type Language = "en" | "th";

type LanguageContextType = {
    language: Language;
    setLanguage: (lang: Language) => void;
    t: (key: string) => string;
};

const LanguageContext = createContext<LanguageContextType | undefined>(undefined);

// Simple dictionary for translations
const translations = {
    en: {
        // Navigation
        "nav.home": "Home",
        "nav.dashboard": "Dashboard",
        "nav.trucks": "Truck",
        "nav.users": "Users",
        "nav.drivers": "Drivers",
        "nav.packages": "Packages",
        "nav.analytics": "Analytics",
        "nav.settings": "Settings",
        "nav.login": "Login",
        "nav.register": "Join Waitlist",
        "nav.logout": "Logout",
        "nav.myAccount": "My Account",
        "nav.myFavorite": "My Favorite",
        "nav.adminDashboard": "Admin Dashboard",
        "nav.propertyStockSearch": "Property stock search",
        "nav.waitlist": "Waitlist",
        "nav.platform": "Platform",
        "nav.fleets": "Fleets",
        "nav.subcontractors": "Subcontractors",

        // Home
        "home.welcome": "Welcome to Logi Track",

        // Auth
        "auth.login.title": "Login",
        "auth.login.subtitle": "Enter your email below to login to your account",
        "auth.register.title": "Join Waitlist",
        "auth.register.subtitle": "Enter your email to request access",
        "auth.email": "Email",
        "auth.password": "Password",
        "auth.confirmPassword": "Confirm Password",
        "auth.forgotPassword": "Forgot your password?",
        "auth.or": "or",
        "auth.dontHaveAccount": "You need to try our system?",
        "auth.alreadyHaveAccount": "Already have an account? Sign In",
        "auth.passwordsDoNotMatch": "Passwords do not match",
        "auth.passwordMinLength": "Password must be at least 6 characters",
        "auth.creatingAccount": "Creating Account...",
        "auth.signUp": "Join Waitlist",
        "auth.signIn": "Sign In",
        "auth.waitlist.description": "Registration is currently by invitation only. Join the waitlist to request access.",
        "auth.waitlist.submit": "Request Access",
        "auth.signingIn": "Signing in...",
        "auth.waitlist.successTitle": "You're on the list!",
        "auth.waitlist.successDesc": "Thank you for your interest. We'll verify your information and contact you when your account is ready.",
        "auth.backToLogin": "Back to Login",
        "auth.emailRequired": "Please enter your email address",
        "auth.waitlist.successToast": "Successfully joined the waitlist!",
        "auth.waitlist.errorToast": "Failed to join waitlist. Please try again.",

        // Admin Panel
        "admin.panel": "Admin Panel",

        // Dashboard
        "dashboard.title": "Admin Dashboard",
        "dashboard.subtitle": "Manage your logistics operations",
        "dashboard.manageUsers": "Manage Users",
        "dashboard.manageUsersDesc": "View and manage user accounts",
        "dashboard.manageDrivers": "Manage Drivers",
        "dashboard.manageDriversDesc": "View and manage driver accounts",
        "dashboard.managePackages": "Manage Packages",
        "dashboard.managePackagesDesc": "View and manage package deliveries",
        "dashboard.manageTrucks": "Manage Trucks",
        "dashboard.manageTrucksDesc": "View and manage truck fleet",
        "dashboard.analytics": "Analytics",
        "dashboard.analyticsDesc": "View delivery statistics and reports",

        // Trucks
        "trucks.title": "Trucks",
        "trucks.subtitle": "Manage your truck fleet",
        "trucks.addTruck": "Add Truck",
        "trucks.searchPlaceholder": "Search trucks...",
        "trucks.noTrucks": "No trucks yet",
        "trucks.getStarted": "Get started by adding your first truck.",
        "trucks.status.available": "Available",
        "trucks.status.inTransit": "In Transit",
        "trucks.status.maintenance": "Maintenance",
        "trucks.filter.own": "Own Fleet",
        "trucks.filter.subcontractor": "Subcontractor Trucks",
        "trucks.filter.all": "All Trucks",

        // Subcontractors
        "subcontractors.title": "Subcontractors",
        "subcontractors.subtitle": "Manage external truck providers",
        "subcontractors.add": "Register Subcontractor",
        "subcontractors.search": "Search name, contact...",
        "subcontractors.table.name": "Name",
        "subcontractors.table.type": "Type",
        "subcontractors.table.contact": "Contact",
        "subcontractors.table.phone": "Phone / Mobile",
        "subcontractors.table.status": "Status",
        "subcontractors.noData": "No subcontractors found",

        // User Management
        "users.title": "User Management",
        "users.subtitle": "Manage users and their permissions.",
        "users.sync": "Sync Database",
        "users.add": "Add User",
        "users.refresh": "Refresh List",
        "users.allUsers": "All Users",
        "users.allUsersDesc": "List of all registered users in the system.",
        "users.table.user": "User",
        "users.table.role": "Role",
        "users.table.providers": "Providers",
        "users.table.lastSignIn": "Last Sign In",
        "users.role.admin": "Admin",
        "users.role.partner": "Partner",
        "users.role.subcontractor": "Subcontractor",
        "users.role.customer": "Customer",
        "users.role.user": "User",
        "users.editRole": "Edit Role",
        "users.createTitle": "Create New User",
        "users.createDesc": "Add a new user to the system. Assign a specific role.",
        "users.form.displayName": "Display Name",
        "users.form.email": "Email",
        "users.form.password": "Password",
        "users.form.role": "Role",
        "users.form.cancel": "Cancel",
        "users.form.create": "Create User",
        "users.form.save": "Save Changes",

        // Waitlist Admin
        "waitlist.title": "Waitlist",
        "waitlist.description": "Manage users who requested access.",
        "waitlist.requests": "Waitlist Requests",
        "waitlist.requestsDesc": "List of emails waiting for an invitation.",
        "waitlist.noRequests": "No pending requests.",
        "waitlist.entryRemoved": "Entry removed",
        "waitlist.deleteFailed": "Failed to delete entry",
        "waitlist.fetchFailed": "Failed to fetch waitlist",
        "waitlist.confirmDelete": "Are you sure you want to remove this entry?",

        // Common
        "common.loading": "Loading...",
        "common.actions": "Actions",
        "common.joinedAt": "Joined At",
        "common.email": "Email",

        // Landing Page
        "landing.nav.solutions": "Solutions",
        "landing.nav.pricing": "Pricing",
        "landing.nav.about": "About Us",
        "landing.hero.tagline": "Global Logistics Management",
        "landing.hero.title": "Manage Your Supply Chain Professionally",
        "landing.hero.description": "The ultimate dashboard for real-time logistics management and global shipment tracking in one platform.",
        "landing.hero.stat.status": "Status",
        "landing.hero.stat.realtime": "Real-time",
        "landing.hero.stat.security": "Security",
        "landing.hero.stat.enterprise": "Enterprise",
        "landing.hero.login.title": "Admin Login",
        "landing.hero.login.subtitle": "Please sign in to manage your system",
        "auth.continueWithGoogle": "Continue with Google",
        "auth.sso": "Enterprise Login (SSO)",
        "auth.termsPrefix": "By signing in, you agree to our",
        "auth.terms": "Terms of Service",
        "auth.and": "and",
        "auth.privacy": "Privacy Policy",
        "landing.features.title": "Built for the Digital Logistics Age",
        "landing.features.description": "Our platform uses modern technology to provide unprecedented visibility and control over your global operations.",
        "landing.feature.tracking.title": "Real-time Tracking",
        "landing.feature.tracking.desc": "Monitor every shipment in real-time with high precision and live GPS updates.",
        "landing.feature.admin.title": "Admin Management",
        "landing.feature.admin.desc": "Comprehensive tools for fleet management, automated scheduling, and role-based access.",
        "landing.feature.language.title": "Multi-language Support",
        "landing.feature.language.desc": "Localized interfaces including Thai, enabling global partners to collaborate seamlessly.",
        "landing.cta.title": "Ready to optimize your operations?",
        "landing.cta.description": "Join thousands of logistics professionals who choose Logi-Track.",
        "landing.cta.requestDemo": "Request Demo",
        "landing.cta.viewApi": "View API Docs",
        "landing.footer.terms": "Terms of Use",
        "landing.footer.contact": "Contact Us",
        "landing.footer.systemOnline": "System Online",
        "landing.footer.rights": "All rights reserved.",
        "landing.footer.location": "United States",
        "landing.footer.global": "Global",
    },
    th: {
        // Navigation
        "nav.home": "หน้าแรก",
        "nav.dashboard": "แดชบอร์ด",
        "nav.trucks": "รถบรรทุก",
        "nav.users": "ผู้ใช้งาน",
        "nav.drivers": "คนขับรถ",
        "nav.packages": "พัสดุ",
        "nav.analytics": "วิเคราะห์ข้อมูล",
        "nav.settings": "ตั้งค่า",
        "nav.login": "เข้าสู่ระบบ",
        "nav.register": "เข้าร่วมรายการรอ",
        "nav.logout": "ออกจากระบบ",
        "nav.myAccount": "บัญชีของฉัน",
        "nav.myFavorite": "รายการโปรด",
        "nav.adminDashboard": "แดชบอร์ดผู้ดูแล",
        "nav.propertyStockSearch": "ค้นหาคลังสินค้า",
        "nav.waitlist": "รายการรอ",
        "nav.platform": "แพลตฟอร์ม",
        "nav.fleets": "กองรถ",
        "nav.subcontractors": "ผู้รับเหมาช่วง",

        // Home
        "home.welcome": "ยินดีต้อนรับสู่ Logi Track",

        // Auth
        "auth.login.title": "เข้าสู่ระบบ",
        "auth.login.subtitle": "กรอกอีเมลด้านล่างเพื่อเข้าสู่ระบบ",
        "auth.register.title": "เข้าร่วมรายการรอ",
        "auth.register.subtitle": "กรอกอีเมลเพื่อขอเข้าใช้งาน",
        "auth.email": "อีเมล",
        "auth.password": "รหัสผ่าน",
        "auth.confirmPassword": "ยืนยันรหัสผ่าน",
        "auth.forgotPassword": "ลืมรหัสผ่าน?",
        "auth.or": "หรือ",
        "auth.dontHaveAccount": "ต้องการขอทดสอบการใช้งานระบบ?",
        "auth.alreadyHaveAccount": "มีบัญชีอยู่แล้ว? เข้าสู่ระบบ",
        "auth.passwordsDoNotMatch": "รหัสผ่านไม่ตรงกัน",
        "auth.passwordMinLength": "รหัสผ่านต้องมีอย่างน้อย 6 ตัวอักษร",
        "auth.creatingAccount": "กำลังสร้างบัญชี...",
        "auth.signUp": "เข้าร่วมรายการรอ",
        "auth.signIn": "เข้าสู่ระบบด้วยอีเมล",
        "auth.waitlist.description": "การลงทะเบียนเฉพาะผู้ได้รับเชิญเท่านั้น เข้าร่วมรายการรอเพื่อขอสิทธิ์เข้าใช้งาน",
        "auth.waitlist.submit": "ขอสิทธิ์เข้าใช้งาน",
        "auth.signingIn": "กำลังเข้าสู่ระบบ...",
        "auth.waitlist.successTitle": "คุณอยู่ในรายการแล้ว!",
        "auth.waitlist.successDesc": "ขอบคุณที่สนใจ เราจะตรวจสอบข้อมูลของคุณและติดต่อกลับเมื่อบัญชีของคุณพร้อมใช้งาน",
        "auth.backToLogin": "กลับไปหน้าเข้าสู่ระบบ",
        "auth.emailRequired": "กรุณากรอกอีเมล",
        "auth.waitlist.successToast": "เข้าร่วมรายการรอสำเร็จ!",
        "auth.waitlist.errorToast": "ไม่สามารถเข้าร่วมรายการรอได้ กรุณาลองใหม่อีกครั้ง",

        // Admin Panel
        "admin.panel": "แผงควบคุมผู้ดูแล",

        // Dashboard
        "dashboard.title": "แดชบอร์ดผู้ดูแล",
        "dashboard.subtitle": "จัดการการดำเนินงานโลจิสติกส์ของคุณ",
        "dashboard.manageUsers": "จัดการผู้ใช้งาน",
        "dashboard.manageUsersDesc": "ดูและจัดการบัญชีผู้ใช้งาน",
        "dashboard.manageDrivers": "จัดการคนขับรถ",
        "dashboard.manageDriversDesc": "ดูและจัดการบัญชีคนขับรถ",
        "dashboard.managePackages": "จัดการพัสดุ",
        "dashboard.managePackagesDesc": "ดูและจัดการการจัดส่งพัสดุ",
        "dashboard.manageTrucks": "รถบรรทุก",
        "dashboard.manageTrucksDesc": "ดูและจัดการกองรถบรรทุก",
        "dashboard.analytics": "วิเคราะห์ข้อมูล",
        "dashboard.analyticsDesc": "ดูสถิติและรายงานการจัดส่ง",

        // Trucks
        "trucks.title": "รถบรรทุก",
        "trucks.subtitle": "จัดการกองรถบรรทุกของคุณ",
        "trucks.addTruck": "เพิ่มรถบรรทุก",
        "trucks.searchPlaceholder": "ค้นหารถบรรทุก...",
        "trucks.noTrucks": "ยังไม่มีรถบรรทุก",
        "trucks.getStarted": "เริ่มต้นด้วยการเพิ่มรถบรรทุกคันแรกของคุณ",
        "trucks.status.available": "ว่าง",
        "trucks.status.inTransit": "กำลังส่งของ",
        "trucks.status.maintenance": "ซ่อมบำรุง",
        "trucks.filter.own": "กองรถบริษัท",
        "trucks.filter.subcontractor": "รถร่วมบริการ",
        "trucks.filter.all": "รถทั้งหมด",

        // Subcontractors
        "subcontractors.title": "ผู้รับเหมาช่วง",
        "subcontractors.subtitle": "จัดการผู้ให้บริการขนส่งภายนอก",
        "subcontractors.add": "ลงทะเบียนผู้รับเหมา",
        "subcontractors.search": "ค้นหาชื่อ, ผู้ติดต่อ...",
        "subcontractors.table.name": "ชื่อ",
        "subcontractors.table.type": "ประเภท",
        "subcontractors.table.contact": "ผู้ติดต่อ",
        "subcontractors.table.phone": "โทรศัพท์",
        "subcontractors.table.status": "สถานะ",
        "subcontractors.noData": "ไม่พบข้อมูลผู้รับเหมา",

        // User Management
        "users.title": "จัดการผู้ใช้งาน",
        "users.subtitle": "จัดการผู้ใช้และสิทธิ์การใช้งาน",
        "users.sync": "ซิงค์ฐานข้อมูล",
        "users.add": "เพิ่มผู้ใช้งาน",
        "users.refresh": "รีเฟรชรายการ",
        "users.allUsers": "ผู้ใช้งานทั้งหมด",
        "users.allUsersDesc": "รายชื่อผู้ใช้งานที่ลงทะเบียนทั้งหมดในระบบ",
        "users.table.user": "ผู้ใช้งาน",
        "users.table.role": "บทบาท",
        "users.table.providers": "ผู้ให้บริการ",
        "users.table.lastSignIn": "เข้าสู่ระบบล่าสุด",
        "users.role.admin": "ผู้ดูแลระบบ",
        "users.role.partner": "พาร์ทเนอร์",
        "users.role.subcontractor": "ผู้รับเหมาช่วง",
        "users.role.customer": "ลูกค้า",
        "users.role.user": "ผู้ใช้ทั่วไป",
        "users.editRole": "แก้ไขบทบาท",
        "users.createTitle": "สร้างผู้ใช้งานใหม่",
        "users.createDesc": "เพิ่มผู้ใช้งานใหม่เข้าสู่ระบบ กำหนดบทบาทเฉพาะ",
        "users.form.displayName": "ชื่อที่แสดง",
        "users.form.email": "อีเมล",
        "users.form.password": "รหัสผ่าน",
        "users.form.role": "บทบาท",
        "users.form.cancel": "ยกเลิก",
        "users.form.create": "สร้างผู้ใช้งาน",
        "users.form.save": "บันทึกการเปลี่ยนแปลง",

        // Waitlist Admin
        "waitlist.title": "รายการรอ",
        "waitlist.description": "จัดการผู้ใช้ที่ขอสิทธิ์เข้าใช้งาน",
        "waitlist.requests": "คำขอรายการรอ",
        "waitlist.requestsDesc": "รายชื่ออีเมลที่รอการเชิญ",
        "waitlist.noRequests": "ไม่มีคำขอที่รอดำเนินการ",
        "waitlist.entryRemoved": "ลบรายการแล้ว",
        "waitlist.deleteFailed": "ลบรายการไม่สำเร็จ",
        "waitlist.fetchFailed": "ดึงข้อมูลรายการรอไม่สำเร็จ",
        "waitlist.confirmDelete": "คุณแน่ใจหรือไม่ว่าต้องการลบรายการนี้?",

        // Common
        "common.loading": "กำลังโหลด...",
        "common.actions": "การกระทำ",
        "common.joinedAt": "วันที่เข้าร่วม",
        "common.email": "อีเมล",

        // Landing Page
        "landing.nav.solutions": "โซลูชั่น",
        "landing.nav.pricing": "ราคา",
        "landing.nav.about": "เกี่ยวกับเรา",
        "landing.hero.tagline": "การจัดการโลจิสติกส์ระดับโลก",
        "landing.hero.title": "จัดการห่วงโซ่อุปทานของคุณอย่างมืออาชีพ",
        "landing.hero.description": "แดชบอร์ดที่ดีที่สุดสำหรับการจัดการโลจิสติกส์แบบเรียลไทม์และการติดตามการจัดส่งทั่วโลกในแพลตฟอร์มเดียว",
        "landing.hero.stat.status": "สถานะ",
        "landing.hero.stat.realtime": "เรียลไทม์",
        "landing.hero.stat.security": "ความปลอดภัย",
        "landing.hero.stat.enterprise": "องค์กร",
        "landing.hero.login.title": "เข้าสู่ระบบผู้ดูแล",
        "landing.hero.login.subtitle": "กรุณาเข้าสู่ระบบเพื่อจัดการระบบของคุณ",
        "auth.continueWithGoogle": "ดำเนินการต่อด้วย Google",
        "auth.sso": "เข้าสู่ระบบองค์กร (SSO)",
        "auth.termsPrefix": "การเข้าสู่ระบบถือว่าคุณยอมรับ",
        "auth.terms": "เงื่อนไขการให้บริการ",
        "auth.and": "และ",
        "auth.privacy": "นโยบายความเป็นส่วนตัว",
        "landing.features.title": "สร้างขึ้นเพื่อยุคโลจิสติกส์ดิจิทัล",
        "landing.features.description": "แพลตฟอร์มของเราใช้เทคโนโลยีที่ทันสมัยเพื่อให้การมองเห็นและการควบคุมการดำเนินงานทั่วโลกของคุณอย่างที่ไม่เคยมีมาก่อน",
        "landing.feature.tracking.title": "การติดตามแบบเรียลไทม์",
        "landing.feature.tracking.desc": "ติดตามการจัดส่งทุกรายการแบบเรียลไทม์ด้วยความแม่นยำสูงและการอัปเดต GPS สด",
        "landing.feature.admin.title": "การจัดการผู้ดูแล",
        "landing.feature.admin.desc": "เครื่องมือครบวงจรสำหรับการจัดการกองเรือ การจัดตารางอัตโนมัติ และการเข้าถึงตามบทบาท",
        "landing.feature.language.title": "รองรับหลายภาษา",
        "landing.feature.language.desc": "อินเทอร์เฟซที่แปลเป็นภาษาท้องถิ่นรวมถึงภาษาไทย ช่วยให้พันธมิตรทั่วโลกทำงานร่วมกันได้อย่างราบรื่น",
        "landing.cta.title": "พร้อมที่จะเพิ่มประสิทธิภาพการดำเนินงานของคุณหรือยัง?",
        "landing.cta.description": "เข้าร่วมกับผู้เชี่ยวชาญด้านโลจิสติกส์นับพันที่เลือก Logi-Track",
        "landing.cta.requestDemo": "ขอสาธิตการใช้งาน",
        "landing.cta.viewApi": "ดูเอกสาร API",
        "landing.footer.terms": "เงื่อนไขการใช้งาน",
        "landing.footer.contact": "ติดต่อเรา",
        "landing.footer.systemOnline": "ระบบออนไลน์",
        "landing.footer.rights": "สงวนลิขสิทธิ์",
        "landing.footer.location": "สหรัฐอเมริกา",
        "landing.footer.global": "ทั่วโลก",
    },
};

export function LanguageProvider({ children }: { children: React.ReactNode }) {
    const [language, setLanguage] = useState<Language>("en");

    useEffect(() => {
        const savedLanguage = localStorage.getItem("language") as Language;
        if (savedLanguage) {
            setLanguage(savedLanguage);
        }
    }, []);

    const handleSetLanguage = (lang: Language) => {
        setLanguage(lang);
        localStorage.setItem("language", lang);
    };

    const t = (key: string): string => {
        return translations[language][key as keyof typeof translations["en"]] || key;
    };

    return (
        <LanguageContext.Provider value={{ language, setLanguage: handleSetLanguage, t }}>
            {children}
        </LanguageContext.Provider>
    );
}

export function useLanguage() {
    const context = useContext(LanguageContext);
    if (context === undefined) {
        throw new Error("useLanguage must be used within a LanguageProvider");
    }
    return context;
}

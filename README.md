# LogiTrack Platform

A dual-application logistics ecosystem: **LogiTrack Mobile** for drivers in the field and **LogiTrack Web** for central admin control. Built on **Firebase** (Auth, Firestore, Storage, Functions) with **Flutter** (mobile) and **Next.js** (web).

---

## 📱 LogiTrack Mobile — Driver app (Flutter)

- **Tasks & check-in**: First Mile (FM) and Line Haul (LH) tasks via Firestore; sequential check-in guardrails; manual check-in with SOC/Hub selection.
- **Proof of action**: Geo-tagged photos at check-in (GPS, timestamp, address); trip history with images and map links.
- **Loading (Pick up)**: Run-sheet OCR (trip ID, seal, etc.), multi-step photo capture and run-sheet upload.
- **Delivery**: Run-sheet OCR validation, multi-step delivery photos, incident/accident reporting.
- **Vehicle & fleet**: Truck types (e.g. PICKUP, 4WJ, 6WH, 10WH, Van) and assignments tied to driver profile.
- **Expenses**: Refuel and other expenses with receipt upload and OCR.
- **Chat**: Per-trip chat with admin.
- **Broadcasts**: Read admin announcements (read/unread).
- **Working holiday calendar**: View working days and holidays (month/list).
- **Leave requests**: Create and track leave (sick/business, dates, optional evidence upload).
- **Trip & job history**: Past trips and job records.
- **UX**: Bilingual (EN/TH), dark/light mode.

---

## 🌐 LogiTrack Web — Admin dashboard (Next.js)

- **Dashboard**: Overview, job monitor (First Mile / Line Haul), stats (users, drivers, packages).
- **Fleet**: Trucks, assignments, renewals, maintenance costs, subcontractors, customers.
- **Drivers**: Driver management.
- **Chat & waitlist**: Driver chat, join-network waitlist.
- **Accounting**: Fuel, other expenses, expense audit.
- **Operations**: First Mile tasks, Line Haul tasks, source (SOC/Hub) management, driver monitor, incident reports.
- **HR**: Payroll, leave requests, holiday calendar (generate and manage).
- **Auth & security**: Google sign-in, cookie-based sessions, Next.js middleware, RBAC (admin whitelist, capability-based sidebar).
- **Design**: Tailwind, shadcn/ui, Lucide icons, Poppins, EN/TH i18n.

---

## 🛠 Tech stack

| Layer      | Choice |
|-----------|--------|
| **Mobile** | Flutter (Dart), Clean Architecture, Provider/BLoC, Firebase (Auth, Firestore, FCM), easy_localization |
| **Web**    | Next.js (App Router), TypeScript, React, Tailwind, shadcn/ui, Firebase (Auth, Firestore), server logic via **Firebase Callables** |
| **Backend**| Firebase (Auth, Firestore, Storage, Functions in `logitrack-web/functions/`, region `asia-southeast1`) |
| **SSOT**   | `shared-docs/schemas/` (Zod); web imports directly; mobile Dart models kept in sync |

Client → server actions use **onCall + httpsCallable**; sensitive or validated writes go through Cloud Functions, not direct client Firestore writes.

---

## 📁 Repository structure

```
logitrack-platform/
├── logitrack-web/       # Next.js admin app (app/, components/, context/, firebase/, lib/)
├── logitrack-mobile/    # Flutter driver app (lib/features/*, Clean Architecture)
├── shared-docs/         # Schemas (Zod), .vibe-rules.md (project rules, tech stack, patterns)
└── envs/                # Env templates; real .env files are gitignored
```

- **Docs & rules**: See `shared-docs/.vibe-rules.md` for structure, patterns, callable functions, and SSOT.
- **Environment**: Copy `envs/*.example` (or repo docs) into the appropriate `.env` files for web and mobile; never commit real secrets.

---

## 🏁 Summary

**Web admins** configure fleet, tasks, and HR (including holidays and leave); **mobile drivers** execute FM/LH tasks with check-in, photos, OCR, expenses, chat, and leave requests. One Firebase backend keeps both apps in sync for transparent, accountable operations.

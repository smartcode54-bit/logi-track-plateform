# Final source-only Assessment B confirmation — 2026-10-19

**Verdict: the three prior shared-control P1s are fixed, but UI6/UI7 are not yet P1-free.** This one bounded source pass found remaining programmatic field-label gaps outside the new `Field` context. No P0 was established. Root should fix the concrete set below together before issue closure.

Root: `logitrack-web`. No source edits, browser use, agent spawning, detector reruns, test reruns or build runs were performed. Source counts used the existing baseline script once, writing only `/private/tmp`. This report records the source as read before root's subsequent label-fix batch; line numbers refer to that snapshot.

## Five-dimension comparison with UI1

Baseline: `shared-docs/specs/mv-go/ui-audit-baseline.md`, measured `dd8da7df`, 2026-10-14. Both baseline and this confirmation use the same 0–4 code-audit rubric. Current scores are source judgments, not new visual/contrast/performance measurements.

| Scope | A11y | Perf | Theme | Responsive | Integrity | Current /20 | UI1 /20 | Change |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Shared controls | 3 | 3 | 3 | 3 | 4 | **16** | 16 | 0 |
| UI6 | 2 | 3 | 3 | 3 | 3 | **14** | 12 | +2 |
| UI7 | 2 | 3 | 3 | 3 | 3 | **14** | 9 | +5 |

UI6 improves through consistent tokens, responsive page frames and shared control reuse; unresolved unnamed monetary/date controls hold accessibility at2. UI7 improves through tokenized public/admin pages, accessible scope/status text, responsive chat and recoverable role edits; remaining dialog/filter associations hold accessibility at2. Performance stays3: route chunks, lazy heavy dependencies and paged/virtualized lists remain, but a source pass cannot measure runtime speed. Do not infer P1-free status from the Good numerical band.

## Confirmed fixes

- `components/ui/datetime-picker.tsx:65–96`: named real clear button, localized time instruction, hour/minute through shared searchable Select, disabled propagation; no native time selects. Maintenance retains explicit trigger naming via `MaintenanceForm.tsx:130–138`. Prior B2 fixed.
- `features/accounting/components/go/shared.tsx:117–125`: stable label ID + FieldLabelContext. Input/Textarea (`:8–21`) and SearchSelectTrigger (`components/ui/search-select.tsx:365…`) inherit it while explicit names take precedence. Prior B3 fixed **for Field-wrapped consumers**, not plain sibling Label consumers listed below.
- `components/accounting/ImagePreviewGallery.tsx:1–25`: delegates to shared FileViewer and lazy thumbnails. `components/ui/file-viewer.tsx:19–26,45–74,88–107`: native named modal, Escape isolation from its parent detail dialog, Shift+arrow pan, pointer capture/cancel/lost capture, touch-none only when zoomed, named44px mobile actions. Prior B1 fixed in source; root owns real-browser focus/touch confirmation.
- `features/tasks/components/go/TaskBoardPage.tsx:266–290`: mobile summaries show identifier/status, origin→destination, date/time, driver and plate, with44px View action; desktop table remains separately rendered. This is the user-selected mobile behavior.
- `features/security-center/components/RoleMatrixPage.tsx:113–127`: dirty cells activate useUnsavedChanges; layer change prompts before discard; successful save/discard clear edits. `hooks/useUnsavedChanges.ts` handles links, beforeunload, native traversal/fallback; quick jump, tenant switch and sign-out use confirmNavigation. This is the user-selected leave-confirmation behavior.
- BillingDocument filter labels/toggle state, wrapping44px rate-card tabs, container-width chat layout and sheet navigation dismissal, dashboard stale-data retry are present in the current diff.

## Remaining P1: complete concrete control set found

One repeated accessibility mechanism spans the following locations: a visible sibling Label/caption lacks htmlFor/id or aria-labelledby; the control is outside Field/native wrapping label context. The new inherited context therefore does not reach it. A selected value or placeholder does not programmatically associate the separate visible field label. For number/date/file controls with no placeholder there is also no usable fallback field name. Impact: assistive-technology users cannot reliably identify the financial/people field being edited. WCAG1.3.1 and4.1.2; use Field or explicit ID/label relationships. Count files/patterns rather than counting each generated row as a separate issue.

| Source relative to web root | Verified controls / current lines |
|---|---|
| `features/accounting/components/go/AddExpenseDialog.tsx` | `142` SearchPicker(driver); `146` SearchPicker(truck); `150` Input(date); `154` Input(time); `158` Input(amount); `164` Input(liters); `168` Input(price per liter); `172` Input(odometer); `180` SelectTrigger(category); `194` Input(description); `200` Textarea(note); `207` Input(file receipt); `212` Input(file odometer photo) |
| `features/accounting/components/go/BillingResultPage.tsx` | `150` SelectTrigger(status); `166` SelectTrigger(month); `188` SelectTrigger(year) |
| `features/accounting/components/go/ExpenseDetailDialog.tsx` | `176` Input generated by num helper; `182` Input generated by text helper; `213` SelectTrigger(category); `230` SearchPicker(truck); `234` Textarea(note); `273` Textarea(admin note) |
| `features/accounting/components/go/ShopeeReportPage.tsx` | `140` SelectTrigger(month); `155` SelectTrigger(year); `170` SelectTrigger(round) |
| `features/accounting/components/go/TollImportDialog.tsx` | `88` SearchPicker(truck); `92` SearchPicker(driver); `96` Input(file) |
| `features/accounting/components/go/rateCard/EntriesTab.tsx` | `104` SelectTrigger(hub); `119` SelectTrigger(destination); `134` SelectTrigger(vehicle class); `149` SelectTrigger(job category); `160` Input(rate); `164` Input(distance); `168` Input(effective date); `303` SelectTrigger(source filter); `319` SelectTrigger(destination filter); `335` SelectTrigger(vehicle-class filter); `350` Input(search with unassociated visible Label) |
| `features/accounting/components/go/rateCard/FuelAdjustmentsTab.tsx` | `139` Input(effective date); `158` Input(multiplier); `164` Input(add per trip); `171` Input(band baseline); `175` Input(THB per baht); `181` Input(reference fuel price); `200` Input(note) |
| `features/accounting/components/go/rateCard/OtherTabs.tsx` | `88` SelectTrigger(fee type); `103` SelectTrigger(unit); `119` Input(custom type); `124` Input(service amount); `128` Input(service note); `268` Input(standby rate); `272` Input(effective date); `277` Input(standby note) |
| `features/accounting/components/go/rateCard/RateImportDialog.tsx` | `143` Input(effective date); `148` SelectTrigger(job category); `159` Input(file) |
| `features/accounting/components/go/rateCard/VoidDialog.tsx` | `46` Input(reason with unassociated visible Label) |
| `features/driver-compensation/components/go/PayrollPage.tsx` | `162` SearchPicker(driver filter) |
| `features/driver-compensation/components/go/PenaltiesPage.tsx` | `61` SearchPicker(driver filter) |
| `features/leave/components/LeaveRequestsPage.tsx` | `117` SearchPicker(driver filter) |
| `features/holidays/components/ReviewGeneratedDialog.tsx` | `137` Input(date with no accessible name/association) |
| `features/chat/components/DriverPanelGo.tsx` | `157` SelectTrigger(incident cause) |
| `features/users/components/EditUserDialog.tsx` | `219` SelectTrigger(role) |
| `app/app/settings/company-profile/page.tsx` | `297` SelectTrigger(branch type; UI4/settings overlap) |

All17 listed source files contain concrete editable/filter control association gaps (10 UI6 files,6 UI7 files,1 UI4/settings overlap); helper return sites count once although they render several expense fields. These are current source findings, not optional polish.

Excluded from the P1 set: payroll payment fields nested inside native `<label>`; payroll configuration explicit aria labels; MaintenanceForm fieldProps IDs and ref-applied date names; the shared Field provider itself; static display labels/text; placeholder-only search boxes (`HolidaysPage.tsx:303`, `TenantAdminsDialog.tsx:139`). `ReviewGeneratedDialog.tsx:118,127` has language placeholders and table context; `:149` type select has table/selected-value context; `YearSelectDialog.tsx:59`, `PenaltiesPage.tsx:211` and MaintenanceOverview truck picker have a contextual single-field/placeholder name. These weaker candidates are **not** asserted as additional P1s in this bounded pass.

Raw AST candidates82; contextual classification is retained in `/private/tmp/logitrack-ui-final-source-labels-b-2026-10-19.json`. Do not blindly fix82 candidates.

## Every UI6/UI7 page source

All12 UI6 and24 UI7 route entries were traced to their current implementations; five overlapping/settings/public entries were also checked. No page is being marked visually accepted by this source-only inventory. A clean row means no P0/P1 was established in this bounded source pass, not proof of every runtime state.

| Group | Route | Implementation / source disposition |
|---|---|---|
| UI6 | `/app/accounting/audit` | `features/accounting/components/go/ExpenseAuditPage` — P1 in named control or reachable dialog/filter |
| UI6 | `/app/accounting/billing-document` | `features/accounting/components/go/BillingDocumentPage` — no P0/P1 established in source pass; runtime proof remains separate |
| UI6 | `/app/accounting/billing-result` | `features/accounting/components/go/BillingResultPage` — P1 in named control or reachable dialog/filter |
| UI6 | `/app/accounting/fuel` | `features/accounting/components/go/ExpensesPage` — P1 in named control or reachable dialog/filter |
| UI6 | `/app/accounting/fuel-price-history` | `features/accounting/components/go/FuelPriceHistoryPage` — no P0/P1 established in source pass; runtime proof remains separate |
| UI6 | `/app/accounting/income` | `features/accounting/components/go/IncomePage` — no P0/P1 established in source pass; runtime proof remains separate |
| UI6 | `/app/accounting/other` | `features/accounting/components/go/ExpensesPage` — P1 in named control or reachable dialog/filter |
| UI6 | `/app/accounting/rate-card` | `features/accounting/components/go/RateCardPage` — P1 in named control or reachable dialog/filter |
| UI6 | `/app/accounting/shopee-express-report` | `features/accounting/components/go/ShopeeReportPage` — P1 in named control or reachable dialog/filter |
| UI6 | `/app/maintenance` | `features/maintenance/components/go/MaintenanceOverviewPage` — no P0/P1 established in source pass; runtime proof remains separate |
| UI6 | `/app/utilities/backfill` | `features/accounting/components/go/BackfillUtilitiesPage` — no P0/P1 established in source pass; runtime proof remains separate |
| UI6 | `/app/utilities/billing-impact` | `features/accounting/components/go/BillingImpactPage` — no P0/P1 established in source pass; runtime proof remains separate |
| UI7 | `/about` | `components/landing/Footer` — no P0/P1 established in source pass; runtime proof remains separate |
| UI7 | `/app/chat` | `features/chat/components/ChatHomeGo` — P1 in named control or reachable dialog/filter |
| UI7 | `/app/chat/room` | `features/chat/components/ChatRoomGo` — P1 in named control or reachable dialog/filter |
| UI7 | `/app/chat/with-driver` | `features/chat/components/ChatWithDriverGo` — P1 in named control or reachable dialog/filter |
| UI7 | `/app/dashboard` | `features/dashboard/components/DashboardPage` — no P0/P1 established in source pass; runtime proof remains separate |
| UI7 | `/app/holidays` | `features/holidays/components/HolidaysPage` — P1 in named control or reachable dialog/filter |
| UI7 | `/app/leave-requests` | `features/leave/components/LeaveRequestsPage` — P1 in named control or reachable dialog/filter |
| UI7 | `/app/operations/roles` | `redirect → /app/security-center/roles` — no P0/P1 established in source pass; runtime proof remains separate |
| UI7 | `/app/payroll/config` | `features/driver-compensation/components/go/PayrollConfigPage` — no P0/P1 established in source pass; runtime proof remains separate |
| UI7 | `/app/payroll` | `features/driver-compensation/components/go/PayrollPage` — P1 in named control or reachable dialog/filter |
| UI7 | `/app/payroll/penalties` | `features/driver-compensation/components/go/PenaltiesPage` — P1 in named control or reachable dialog/filter |
| UI7 | `/app/security-center/api-keys` | `features/security-center/components/ApiKeysPage` — no P0/P1 established in source pass; runtime proof remains separate |
| UI7 | `/app/security-center/audit` | `features/security-center/components/SecurityAuditPage` — no P0/P1 established in source pass; runtime proof remains separate |
| UI7 | `/app/security-center/mobile-clients` | `features/mobile-release/components/MobileClientsPage` — no P0/P1 established in source pass; runtime proof remains separate |
| UI7 | `/app/security-center/mobile-release` | `features/mobile-release/components/MobileReleasePage` — no P0/P1 established in source pass; runtime proof remains separate |
| UI7 | `/app/security-center` | `features/security-center/components/SecurityOverviewPage` — no P0/P1 established in source pass; runtime proof remains separate |
| UI7 | `/app/security-center/roles` | `features/security-center/components/RoleMatrixPage` — no P0/P1 established in source pass; runtime proof remains separate |
| UI7 | `/app/security-center/status` | `features/security-center/components/SystemStatusPage` — no P0/P1 established in source pass; runtime proof remains separate |
| UI7 | `/app/security-center/tenants` | `features/tenants/components/TenantsPage` — no P0/P1 established in source pass; runtime proof remains separate |
| UI7 | `/app/security-center/users` | `features/users/components/UsersPage` — P1 in named control or reachable dialog/filter |
| UI7 | `/app/users` | `redirect → /app/security-center/users` — P1 in named control or reachable dialog/filter |
| UI7 | `/app/waitlist` | `features/waitlist/components/WaitlistPage` — no P0/P1 established in source pass; runtime proof remains separate |
| UI7 | `/join-network` | `components/landing/Footer` — no P0/P1 established in source pass; runtime proof remains separate |
| UI7 | `/` | `components/landing/LandingPage` — no P0/P1 established in source pass; runtime proof remains separate |
| overlap | `/waitlist` | `features/auth/components/WaitlistForm` — no P0/P1 established in source pass; runtime proof remains separate |
| overlap | `/app/settings/company-profile` | `features/accounting/components/go/shared` — P1 in named control or reachable dialog/filter |
| overlap | `/app/settings/support-access` | `features/support-access/components/SupportAccessPage` — no P0/P1 established in source pass; runtime proof remains separate |
| overlap | `/app/support` | `features/auth/api/me` — no P0/P1 established in source pass; runtime proof remains separate |
| overlap | `/app/trucks/maintenance` | `features/maintenance/components/go/TruckMaintenancePage` — no P0/P1 established in source pass; runtime proof remains separate |

## Measurable source evidence and limits

- The baseline source counter now reports UI6:42 files/12 pages, palette colors0, hex/RGB0, tiny text0, uppercase headers0, side stripes0, blur/glow0, raw selects0, unnamed icon buttons0, missing image alt0. UI7:84 files/24 pages and the same zero palette/typography/decorative-pattern measures. UI7's two gradient counts are the token-based role distribution data chart, not decoration; three inline styles are chart geometry. The remaining maintenance400px width is `w-full md:w-[400px]`, not an unbounded phone width.
- Counter `unlabeledInputs` is only a candidate count (UI6:37,UI7:8), not a verified defect count: contextual Field/native labels, explicit spread props and ref naming explain some. The concrete P1 table above is the reviewed result.
- Counter evidence: `/private/tmp/logitrack-ui-final-source-counts-b-2026-10-19.json` and `.tsv`; route inventory: `/private/tmp/logitrack-ui-final-source-routes-b-2026-10-19.json`.
- Root reports3077 tests passed; this pass did not rerun or independently certify them. The build/bundle check was still in progress when delegated; no passing result is claimed here.
- Root owns final browser confirmation. This source pass cannot prove computed light/dark contrast,360px overflow/touch gestures, native modal focus return, all empty/error states, full keyboard task completion, screenshot before/after coverage or bundle success. No optional polish was added to the closure list.

Questions skipped: source-only final confirmation; coordinator owns fixes, final browser evidence and issue closure.

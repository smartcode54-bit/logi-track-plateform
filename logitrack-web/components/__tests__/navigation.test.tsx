import React from "react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Navigation from "../navigation";
import { LanguageProvider } from "@/context/language";
import { configureSignOutNavigation } from "@/features/auth/hooks/useSignOut";

const mockPush = vi.fn();
let mockPathname = "/about";
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: mockPush, replace: vi.fn() }),
  usePathname: () => mockPathname,
}));

vi.mock("../language-switcher", () => ({
  LanguageSwitcher: () => <div data-testid="language-switcher">LanguageSwitcher</div>,
}));

const mockUseAuth = vi.fn();
vi.mock("@/context/auth", () => ({
  useAuth: () => mockUseAuth(),
  AuthProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

/** Renders inside LanguageProvider and waits until its dictionary is loaded (it renders children then). */
async function renderWithProviders(ui: React.ReactElement) {
  const view = render(<LanguageProvider>{ui}</LanguageProvider>);
  await screen.findByText("LogiTrack Pro");
  return view;
}

const navigated: string[] = [];

describe("Navigation", () => {
  beforeEach(() => {
    mockPathname = "/about";
    navigated.length = 0;
    configureSignOutNavigation((url) => navigated.push(url));
    mockUseAuth.mockReturnValue({
      currentUser: null,
      loading: false,
      customClaims: null,
      login: vi.fn(),
      logout: vi.fn(),
    });
  });
  afterEach(() => {
    configureSignOutNavigation((url) => window.location.assign(url));
  });

  it("names the product LogiTrack Pro by GORATECH (UI3), not Logi-Track", async () => {
    await renderWithProviders(<Navigation />);
    expect(screen.getByText("LogiTrack Pro")).toBeInTheDocument();
    expect(screen.getByText("by GORATECH")).toBeInTheDocument();
    expect(screen.queryByText("Logi-Track")).not.toBeInTheDocument();
  });

  it("shows Sign In / login link when user is not logged in, except on the sign-in page itself", async () => {
    const { unmount } = await renderWithProviders(<Navigation />);
    const loginLink = screen.getByRole("link", { name: /sign in/i });
    expect(loginLink).toHaveAttribute("href", "/login");
    unmount();
    mockPathname = "/login";
    await renderWithProviders(<Navigation />);
    expect(screen.queryByRole("link", { name: /sign in/i })).not.toBeInTheDocument();
  });

  it("links only pages that exist when not logged in: no Pricing (UI3C)", async () => {
    await renderWithProviders(<Navigation />);
    expect(screen.getByRole("link", { name: "Features" })).toHaveAttribute("href", "/#solutions");
    expect(screen.getByRole("link", { name: "About" })).toHaveAttribute("href", "/about");
    expect(screen.getByRole("link", { name: "Join Network" })).toHaveAttribute("href", "/join-network");
    expect(screen.queryByRole("link", { name: /pricing/i })).not.toBeInTheDocument();
    for (const link of screen.getAllByRole("link")) expect(link.getAttribute("href")).not.toBe("#");
  });

  it("names the theme button by what it does", async () => {
    await renderWithProviders(<Navigation />);
    expect(screen.getByRole("button", { name: "Switch to the dark theme" })).toBeInTheDocument();
  });

  it("has an accessible name in the active language", async () => {
    await renderWithProviders(<Navigation />);
    expect(screen.getByRole("navigation", { name: "Main" })).toBeInTheDocument();
  });

  it("points a driver-only account to the driver app instead of a console it cannot open (UI3C)", async () => {
    const user = userEvent.setup();
    mockUseAuth.mockReturnValue({
      currentUser: { uid: "d", email: "d@own.test", displayName: "Driver" },
      me: {
        id: "d",
        email: "d@own.test",
        displayName: "Driver",
        photoUrl: null,
        tenant: { id: "t", nameTh: "กองรถ", nameEn: null, kind: "own_fleet", role: "driver" },
        tenants: [],
        platformRoles: [],
        dispatcher: false,
        steward: false,
        driver: { id: "drv" },
        customerScopes: [],
        capabilities: [],
        mustChangePassword: false,
      },
      loading: false,
      customClaims: { role: "driver" },
      login: vi.fn(),
      logout: vi.fn(),
    });
    await renderWithProviders(<Navigation />);
    await user.click(screen.getByRole("button", { name: "Account menu for Driver" }));
    expect(screen.getByText("Use the LogiTrack driver app")).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Open console" })).toBeNull();
  });

  it("offers every signed-in user their console (the role's home, UI3C), and signs out to the sign-in page", async () => {
    const user = userEvent.setup();
    const logout = vi.fn(async () => undefined);
    mockUseAuth.mockReturnValue({
      currentUser: { uid: "1", email: "a@b.co", displayName: "Customer" },
      // A customer-scope principal without a membership lands on the driver monitor (R89).
      me: {
        id: "1",
        email: "a@b.co",
        displayName: "Customer",
        photoUrl: null,
        tenant: null,
        tenants: [],
        platformRoles: [],
        dispatcher: false,
        steward: false,
        driver: null,
        customerScopes: [{ billingPartyId: "b1", name: "Shipper" }],
        capabilities: ["operations:view_driver_monitor"],
        mustChangePassword: false,
      },
      loading: false,
      customClaims: { role: "customer" },
      login: vi.fn(),
      logout,
    });
    await renderWithProviders(<Navigation />);
    await user.click(screen.getByRole("button", { name: "Account menu for Customer" }));
    expect(screen.getByRole("menuitem", { name: "Open console" })).toHaveAttribute("href", "/app/driver-monitor");
    await user.click(screen.getByRole("menuitem", { name: "Logout" }));
    await waitFor(() => expect(navigated).toEqual(["/login"]));
    expect(logout).toHaveBeenCalledTimes(1);
    expect(mockPush).not.toHaveBeenCalled();
  });
});

import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Input } from "./input";

describe("Input", () => {
  it("renders with placeholder", () => {
    render(<Input placeholder="Enter email" />);
    expect(screen.getByPlaceholderText("Enter email")).toBeInTheDocument();
  });

  it("accepts and displays value", async () => {
    const user = userEvent.setup();
    render(<Input data-testid="input" />);
    const input = screen.getByTestId("input");
    await user.type(input, "hello");
    expect(input).toHaveValue("hello");
  });

  it("can be disabled", () => {
    render(<Input disabled data-testid="input" />);
    expect(screen.getByTestId("input")).toBeDisabled();
  });

  it("supports type password", () => {
    render(<Input type="password" data-testid="input" />);
    const el = screen.getByTestId("input");
    expect(el).toHaveAttribute("type", "password");
  });

  it("has data-slot for styling", () => {
    render(<Input data-testid="input" />);
    expect(screen.getByTestId("input")).toHaveAttribute("data-slot", "input");
  });
});

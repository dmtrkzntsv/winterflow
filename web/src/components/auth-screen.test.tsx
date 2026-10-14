import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import "@/lib/i18n";
import { AuthHeading, AuthScreen } from "@/components/auth-screen";

describe("AuthScreen", () => {
  it("frames the form beside the snowfall, which phones don't show", () => {
    render(
      <AuthScreen>
        <p>the form</p>
      </AuthScreen>,
    );
    expect(screen.getByText("the form")).toBeInTheDocument();
    expect(screen.getByTestId("auth-illustration")).toHaveClass("hidden", "md:block");
    expect(screen.getByRole("link", { name: "Terms of Service" })).toHaveAttribute("rel", "noopener noreferrer");
  });

  it("titles a sign-in page at the page-title size", () => {
    render(<AuthHeading title="Set up Winterflow" subtitle="Create the first account." />);
    expect(screen.getByRole("heading", { level: 1, name: "Set up Winterflow" })).toHaveClass("text-2xl", "font-semibold");
    expect(screen.getByText("Create the first account.")).toBeInTheDocument();
  });
});

import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { NameField } from "@/components/name-field";
import { suggestAppLook } from "@/lib/app-look";

function Harness({ error }: { error?: string }) {
  const [value, setValue] = useState({ name: "", icon: "", color: "" });
  return (
    <>
      <NameField value={value} onChange={setValue} error={error} />
      <output aria-label="look">{JSON.stringify(value)}</output>
    </>
  );
}
const look = () => JSON.parse(screen.getByRole("status", { name: "look" }).textContent ?? "{}");

describe("NameField", () => {
  it("lets the name pick the icon and colour", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await user.type(screen.getByLabelText("Name"), "shop");
    expect(look()).toEqual({ name: "shop", ...suggestAppLook("shop") });
  });

  it("keeps a picked icon when the name changes, until reset", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await user.type(screen.getByLabelText("Name"), "shop");
    await user.click(screen.getByRole("button", { name: "Change icon and colour" }));
    await user.click(await screen.findByRole("button", { name: "Database" }));
    await user.type(screen.getByLabelText("Name"), "-2");
    expect(look()).toMatchObject({ name: "shop-2", icon: "Database" });

    await user.click(screen.getByRole("button", { name: "Change icon and colour" }));
    await user.click(await screen.findByRole("button", { name: "Pick automatically from the name" }));
    expect(look()).toEqual({ name: "shop-2", ...suggestAppLook("shop-2") });
  });

  it("offers the palette's colours", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await user.click(screen.getByRole("button", { name: "Change icon and colour" }));
    await user.click(await screen.findByRole("button", { name: "Colour #6366f1" }));
    expect(look().color).toBe("#6366f1");
  });

  it("shows its message", () => {
    render(<Harness error="Give the app a name." />);
    expect(screen.getByLabelText("Name")).toHaveAccessibleDescription("Give the app a name.");
  });
});

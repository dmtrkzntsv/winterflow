import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { AppIcon } from "@/components/app-icon";

describe("AppIcon", () => {
  it("shows the name's initial", () => {
    expect(render(<AppIcon name="nextcloud" id="01a12cb2" />).container).toHaveTextContent("N");
  });

  it("falls back to the id for an app without a name", () => {
    expect(render(<AppIcon name="" id="01a12cb2" />).container).toHaveTextContent(/^0$/);
    expect(render(<AppIcon id="b7e1" />).container).toHaveTextContent(/^B$/);
  });

  it("shows ? only when neither is known", () => {
    expect(render(<AppIcon />).container).toHaveTextContent(/^\?$/);
  });
});

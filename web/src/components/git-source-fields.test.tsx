import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { GitSourceFields } from "@/components/git-source-fields";
import { newGitSource } from "@/lib/app-editor-io";
import type { FieldErrors } from "@/lib/field-errors";
import type { AppSourceConfig } from "@/types/app-config";

function Harness({ initial = newGitSource(), errors }: { initial?: AppSourceConfig; errors?: FieldErrors }) {
  const [source, setSource] = useState(initial);
  const [token, setToken] = useState("");
  return (
    <>
      <GitSourceFields
        source={source}
        token={token}
        onSource={(patch) => setSource((s) => ({ ...s, ...patch }))}
        onToken={setToken}
        errors={errors}
      />
      <output aria-label="state">{JSON.stringify({ source, token })}</output>
    </>
  );
}
const state = () => JSON.parse(screen.getByRole("status", { name: "state" }).textContent ?? "{}");

describe("GitSourceFields", () => {
  it("asks for the repository and branch, the rest out of the way", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    expect(screen.getByLabelText("Branch")).toHaveValue("main");
    expect(screen.queryByLabelText("Access token")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Compose file path")).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("Repository URL"), "https://github.com/acme/shop");
    await user.click(screen.getByRole("button", { name: "Private repository?" }));
    await user.type(screen.getByLabelText("Access token"), "tok");
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    await user.type(screen.getByLabelText("Compose file path"), "deploy/compose.yml");
    await user.click(screen.getByRole("checkbox", { name: /Redeploy automatically/ }));
    expect(state()).toEqual({
      source: {
        repo_url: "https://github.com/acme/shop",
        branch: "main",
        compose_path: "deploy/compose.yml",
        auto_update: false,
        poll_seconds: 120,
      },
      token: "tok",
    });
  });

  it("opens the token field when one is stored, saying blank keeps it", () => {
    render(<Harness initial={{ ...newGitSource(), token_set: true }} />);
    expect(screen.getByLabelText("Access token")).toHaveAttribute("placeholder", "Stored, leave blank to keep");
  });

  it("shows the URL and branch messages under their fields", () => {
    render(<Harness errors={{ repo_url: "Enter the repository's URL.", branch: "Enter a branch." }} />);
    expect(screen.getByLabelText("Repository URL")).toHaveAccessibleDescription("Enter the repository's URL.");
    expect(screen.getByLabelText("Branch")).toHaveAttribute("aria-invalid", "true");
  });
});

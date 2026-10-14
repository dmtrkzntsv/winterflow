import { useState } from "react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AppEditor } from "@/components/app-editor";
import { emptyEditorState, newGitSource } from "@/lib/app-editor-io";
import type { FieldErrors } from "@/lib/field-errors";
import { fakeAPI } from "@/test/fake-api";
import type { AppEditorState } from "@/types/app-config";

let servers: { activeServer: { id: string; features?: Record<string, boolean> } | null; loading: boolean };
vi.mock("@/context/use-servers", () => ({ useServers: () => servers }));
vi.mock("@/hooks/use-shared-names", () => ({ useSharedNames: () => [] }));
vi.mock("@/components/code-editor", () => ({ CodeEditor: () => <textarea aria-label="code" /> }));
vi.mock("@/components/image-tag-picker", () => ({
  ImageTagPicker: ({ image }: { image: string }) => <button>tags of {image}</button>,
}));

function Harness({ initial, errors, refused }: { initial: AppEditorState; errors?: FieldErrors; refused?: number }) {
  const [state, setState] = useState(initial);
  return (
    <MemoryRouter>
      <AppEditor state={state} onChange={setState} errors={errors} refused={refused} />
      <output aria-label="state">{JSON.stringify(state.config)}</output>
    </MemoryRouter>
  );
}
const config = () => JSON.parse(screen.getByRole("status", { name: "state" }).textContent ?? "{}");

beforeEach(() => {
  servers = { activeServer: null, loading: true };
});
afterEach(() => vi.unstubAllGlobals());

const PROXY = { activeServer: { id: "s1", features: { ingress: true } }, loading: false };
const DOMAINS_API = {
  "/api/v1/domain/get-domains": { domains: [] },
  "/api/v1/domains/check": { available: true, covered: true, registration: null, claims: null },
};
const redirectState = (): AppEditorState => {
  const s = emptyEditorState();
  return {
    ...s,
    config: {
      ...s.config,
      ingress: {
        domains: [{ id: "d1", domain: "", upstream_port: "", ssl: true }],
        redirects: [{ id: "r1", domain: "old.example.com", path: "", to: "nope", code: 301, ssl: true }],
      },
    },
  };
};
const REDIRECT_ERR = { "redirect-to:r1": "Send it to a full address starting with http:// or https://." };

describe("AppEditor storage variables", () => {
  it("lists the referenced storage variables read-only, secrets masked", () => {
    const state: AppEditorState = {
      ...emptyEditorState(),
      storageVariables: [
        { name: "WF_STORAGE_HETZNER_BUCKET", storage: "hetzner", secret: false },
        { name: "WF_STORAGE_HETZNER_SECRET_ACCESS_KEY", storage: "hetzner", secret: true },
      ],
    };
    render(<Harness initial={state} />);
    const rows = within(screen.getByRole("list", { name: "Storage variables" })).getAllByRole("listitem");
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent("WF_STORAGE_HETZNER_BUCKET");
    expect(rows[0]).toHaveTextContent("provided by storage hetzner");
    expect(rows[0]).not.toHaveTextContent("Secret");
    expect(rows[1]).toHaveTextContent("Secret");
    expect(screen.queryByText(/<encrypted>/)).not.toBeInTheDocument();
    expect(within(rows[0]).queryByRole("textbox")).not.toBeInTheDocument();
    expect(screen.queryByText("No variables yet.")).not.toBeInTheDocument();
  });

  it("keeps the empty state without any variable", () => {
    render(<Harness initial={emptyEditorState()} />);
    expect(screen.getByText("No variables yet.")).toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "Storage variables" })).not.toBeInTheDocument();
  });
});

describe("AppEditor", () => {
  it("shows only what runs: no name, icon, colour or description", () => {
    render(<Harness initial={emptyEditorState()} />);
    expect(screen.getByRole("heading", { name: "Files" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Variables" })).toBeInTheDocument();
    expect(screen.queryByLabelText("Name")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Description")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Color")).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Details" })).not.toBeInTheDocument();
  });

  it("edits files through the shared files section", async () => {
    const user = userEvent.setup();
    render(<Harness initial={emptyEditorState()} />);
    await user.click(screen.getAllByRole("button", { name: /Add file/ })[0]);
    expect(config().files).toHaveLength(2);
  });

  it("keeps Deploy from Git closed until used, and opens it as the Git source", async () => {
    const user = userEvent.setup();
    render(<Harness initial={emptyEditorState()} />);
    expect(screen.queryByRole("switch", { name: "Deploy from Git" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Deploy from Git" }));
    await user.click(screen.getByRole("switch", { name: "Deploy from Git" }));
    expect(screen.getByRole("heading", { name: "Git source" })).toBeInTheDocument();
    expect(screen.getByLabelText("Repository URL")).toBeInTheDocument();
    expect(config().source).toEqual(newGitSource());
  });

  it("keeps keyboard focus on Deploy from Git when it is switched on and off", async () => {
    const user = userEvent.setup();
    render(<Harness initial={emptyEditorState()} />);
    await user.click(screen.getByRole("button", { name: "Deploy from Git" }));
    screen.getByRole("switch", { name: "Deploy from Git" }).focus();
    await user.keyboard(" ");
    expect(screen.getByRole("heading", { name: "Git source" })).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: "Deploy from Git" })).toHaveFocus();
    await user.keyboard(" ");
    expect(config().source).toBeUndefined();
    // Off again: the closed disclosure's button has it, not the page.
    expect(screen.getByRole("button", { name: "Deploy from Git" })).toHaveFocus();
  });

  it("shows an app's Git source up front, and says the repository's compose file runs", () => {
    const s = emptyEditorState();
    render(
      <Harness
        initial={{
          ...s,
          config: { ...s.config, files: [], source: { ...newGitSource(), repo_url: "https://x.test/r" } },
          files: {},
        }}
      />,
    );
    expect(screen.getByLabelText("Repository URL")).toHaveValue("https://x.test/r");
    expect(screen.getByRole("switch", { name: "Deploy from Git" })).toBeChecked();
    expect(screen.getByText(/The repository's compose file runs/)).toBeInTheDocument();
    expect(screen.queryByText(/Add at least a/)).not.toBeInTheDocument();
  });

  it("keeps image versions under Advanced", async () => {
    const user = userEvent.setup();
    render(<Harness initial={emptyEditorState()} />);
    expect(screen.queryByRole("button", { name: "tags of nginx:alpine" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    expect(screen.getByRole("heading", { name: /Image versions in/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "tags of nginx:alpine" })).toBeInTheDocument();
  });

  it("has no Domains section without the built-in proxy, only one quiet line", () => {
    servers = { activeServer: { id: "s1", features: {} }, loading: false };
    render(<Harness initial={emptyEditorState()} />);
    expect(screen.queryByRole("heading", { name: "Domains" })).not.toBeInTheDocument();
    expect(screen.getByText("People reach the app on the port its compose file publishes.")).toBeInTheDocument();
    expect(screen.queryByLabelText("Hostname")).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Addresses" })).not.toBeInTheDocument();
  });

  it("puts addresses under Domains and redirects under Advanced, each with its sub-heading", async () => {
    const user = userEvent.setup();
    servers = PROXY;
    fakeAPI(DOMAINS_API);
    render(<Harness initial={redirectState()} />);
    expect(screen.getByRole("heading", { name: "Addresses" })).toBeInTheDocument();
    expect(screen.getByLabelText("Hostname")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Redirects" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Redirect to")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    expect(screen.getByRole("heading", { name: "Redirects" })).toBeInTheDocument();
    expect(screen.getByLabelText("Redirect to")).toBeInTheDocument();
  });

  it("opens Advanced for a refused save and keeps it open while the error is fixed", async () => {
    const user = userEvent.setup();
    servers = PROXY;
    fakeAPI(DOMAINS_API);
    const { rerender } = render(<Harness initial={redirectState()} />);
    expect(screen.queryByLabelText("Redirect to")).not.toBeInTheDocument();
    rerender(<Harness initial={redirectState()} errors={REDIRECT_ERR} refused={1} />);
    const to = screen.getByLabelText("Redirect to");
    expect(to).toHaveAccessibleDescription(REDIRECT_ERR["redirect-to:r1"]);
    // The message clears with the first keystroke: the field stays.
    rerender(<Harness initial={redirectState()} errors={{}} refused={1} />);
    await user.type(screen.getByLabelText("Redirect to"), "h");
    expect(screen.getByLabelText("Redirect to")).toBeInTheDocument();
  });

  it("opens Advanced again on a second refused save after it was closed", async () => {
    const user = userEvent.setup();
    servers = PROXY;
    fakeAPI(DOMAINS_API);
    const { rerender } = render(<Harness initial={redirectState()} />);
    rerender(<Harness initial={redirectState()} errors={REDIRECT_ERR} refused={1} />);
    expect(screen.getByLabelText("Redirect to")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    expect(screen.queryByLabelText("Redirect to")).not.toBeInTheDocument();
    rerender(<Harness initial={redirectState()} errors={REDIRECT_ERR} refused={2} />);
    expect(screen.getByLabelText("Redirect to")).toBeInTheDocument();
  });
});

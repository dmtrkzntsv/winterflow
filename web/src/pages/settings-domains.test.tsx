import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import type { Docker } from "@/context/use-docker";
import { DomainDialog } from "@/components/domain-dialog";
import type { Domain, DomainTLS } from "@/lib/domains";
import { DomainsSettings } from "@/pages/settings-domains";

// Links point into the active server's scope (/server/:id/...).
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));

const toastError = vi.hoisted(() => vi.fn());
const toastSuccess = vi.hoisted(() => vi.fn());
const toastWarning = vi.hoisted(() => vi.fn());
vi.mock("sonner", () => ({ toast: { success: toastSuccess, error: toastError, warning: toastWarning } }));

// No per-keystroke delay: typing stays fast under a loaded CI runner.
const setupUser = () => userEvent.setup({ delay: null });

// pastePEM fills a PEM textarea in one paste, as users do.
async function pastePEM(user: ReturnType<typeof setupUser>, field: HTMLElement, value: string) {
  await user.click(field);
  await user.paste(value);
}

const day = 24 * 60 * 60 * 1000;
const inDays = (n: number) => new Date(Date.now() + n * day).toISOString();

const OFF = "Off — behind Cloudflare Tunnel or another proxy";
const OWN = "My own certificate";
const AUTO = "Automatic (Let's Encrypt)";

const DOMAINS: Domain[] = [
  { name: "*.twillingate.app", tls: "certificate", not_after: inDays(10), issuer: "CF Origin", apps: ["a1", "a2"] },
  { name: "old.example.com", tls: "certificate", not_after: inDays(-1), apps: [] },
  { name: "tunnel.example.com", tls: "off", apps: [] },
  { name: "example.com", tls: "auto", apps: ["a1"] },
  { name: "far.example.com", tls: "certificate", not_after: "2099-01-31T00:00:00Z", apps: [] },
];

function fakeDocker(over: Partial<Docker> = {}): Docker {
  return {
    listDomains: vi.fn().mockResolvedValue(DOMAINS),
    // The agent answers a create with the stored registration.
    createDomain: vi.fn(async (name: string, tls: DomainTLS) => ({ domain: { name, tls, apps: [] }, warnings: [] })),
    deleteDomain: vi.fn().mockResolvedValue([]),
    ...over,
  } as unknown as Docker;
}

function renderPage(docker: Docker) {
  const apps = {
    apps: [
      { id: "a1", name: "Shop" },
      { id: "a2", name: "Blog" },
    ],
  } as unknown as AppsContextValue;
  return render(
    <MemoryRouter>
      <AppsContext.Provider value={apps}>
        <DomainsSettings docker={docker} />
      </AppsContext.Provider>
    </MemoryRouter>,
  );
}

// openMenu opens a domain row's "⋯" menu and picks an item.
async function pick(user: ReturnType<typeof setupUser>, name: string, item: string) {
  await user.click(await screen.findByRole("button", { name: `Actions for ${name}` }));
  await user.click(await screen.findByRole("menuitem", { name: item }));
}

describe("DomainsSettings", () => {
  beforeEach(() => {
    toastError.mockReset();
    toastSuccess.mockReset();
    toastWarning.mockReset();
  });

  it("lists names, HTTPS in plain words, expiry only when it matters, and the apps using each", async () => {
    renderPage(fakeDocker());
    expect(screen.getByRole("heading", { level: 1, name: "Domains" })).toBeInTheDocument();
    expect(await screen.findByText("*.twillingate.app")).toHaveClass("font-mono");
    expect(screen.getAllByText("Own certificate")).toHaveLength(3);
    expect(screen.getByText("HTTPS off")).toBeInTheDocument();
    expect(screen.getByText("Automatic HTTPS")).toBeInTheDocument();
    const soon = screen.getByText("Expires in 10 days");
    // Plain text beside a state dot, never coloured text.
    expect(soon).toHaveClass("text-foreground");
    expect(soon).not.toHaveClass("text-status-warn");
    expect(soon).toHaveAttribute("title", "Issued by CF Origin");
    expect(soon.querySelector("[data-tone]")).toHaveAttribute("data-tone", "warn");
    const expired = screen.getByText(/^Expired /);
    expect(expired).toHaveClass("text-foreground");
    expect(expired.querySelector("[data-tone]")).toHaveAttribute("data-tone", "bad");
    // A certificate far from its end says nothing about it.
    expect(screen.queryByText(/2099/)).not.toBeInTheDocument();
    expect(screen.getByText(/2 apps:/)).toBeInTheDocument();
    expect(screen.getAllByRole("link", { name: "Shop" })[0]).toHaveAttribute("href", "/server/srv-1/app/a1");
    expect(screen.getByRole("link", { name: "Blog" })).toHaveAttribute("href", "/server/srv-1/app/a2");
    expect(screen.getAllByText("Not used")).toHaveLength(3);
  });

  it("adds a domain behind a tunnel", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderPage(docker);

    await user.click(await screen.findByRole("button", { name: /Add domain/ }));
    const dialog = await screen.findByRole("dialog");
    const submit = within(dialog).getByRole("button", { name: "Add" });
    expect(submit).toBeDisabled();

    await user.type(within(dialog).getByLabelText("Name"), "*.*.bad");
    expect(within(dialog).getByText(/Use a lowercase hostname/)).toBeInTheDocument();
    expect(submit).toBeDisabled();

    await user.clear(within(dialog).getByLabelText("Name"));
    await user.type(within(dialog).getByLabelText("Name"), "*.Tunnel.Example.com");
    await user.click(within(dialog).getByRole("radio", { name: OFF }));
    await user.click(submit);

    expect(docker.createDomain).toHaveBeenCalledWith("*.tunnel.example.com", "off", undefined);
    // The answer goes straight into the list, in name order: no new listing.
    const row = (await screen.findByText("*.tunnel.example.com")).closest("li");
    expect(row?.nextElementSibling).toHaveTextContent("*.twillingate.app");
    expect(docker.listDomains).toHaveBeenCalledTimes(1);
    expect(toastSuccess).toHaveBeenCalledWith("Domain added");
  });

  it("asks for a certificate and key when it is your own", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderPage(docker);

    await user.click(await screen.findByRole("button", { name: /Add domain/ }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), "*.secure.app");
    expect(within(dialog).queryByLabelText("Certificate (PEM)")).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole("radio", { name: OWN }));

    expect(within(dialog).getByText(/also serves the automatic-HTTPS addresses it covers/)).toBeInTheDocument();
    expect(within(dialog).queryByText(/Paste the certificate again/)).not.toBeInTheDocument();
    const submit = within(dialog).getByRole("button", { name: "Add" });
    await pastePEM(user, within(dialog).getByLabelText("Certificate (PEM)"), "CERT");
    expect(submit).toBeDisabled(); // the key is still missing
    await pastePEM(user, within(dialog).getByLabelText("Private key (PEM)"), "KEY");
    await user.click(submit);

    expect(docker.createDomain).toHaveBeenCalledWith("*.secure.app", "certificate", { certificate: "CERT", key: "KEY" });
  });

  it("replaces a certificate for an existing name", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderPage(docker);

    await pick(user, "*.twillingate.app", "Edit");
    const dialog = await screen.findByRole("dialog");
    // The name is shown, not editable: only the mode and certificate change.
    expect(within(dialog).queryByLabelText("Name")).not.toBeInTheDocument();
    expect(within(dialog).getByText("*.twillingate.app")).toBeInTheDocument();
    expect(within(dialog).getByRole("radio", { name: OWN })).toBeChecked();
    expect(within(dialog).getByText(/Paste the certificate again/)).toBeInTheDocument();
    await pastePEM(user, within(dialog).getByLabelText("Certificate (PEM)"), "NEW");
    await pastePEM(user, within(dialog).getByLabelText("Private key (PEM)"), "KEY");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(docker.createDomain).toHaveBeenCalledWith("*.twillingate.app", "certificate", { certificate: "NEW", key: "KEY" });
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith("Domain updated"));
  });

  it("edits the HTTPS mode of any registration", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderPage(docker);

    for (const d of DOMAINS) {
      expect(await screen.findByRole("button", { name: `Actions for ${d.name}` })).toBeInTheDocument();
    }
    await pick(user, "example.com", "Edit");
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("radio", { name: AUTO })).toBeChecked();
    expect(within(dialog).queryByLabelText("Certificate (PEM)")).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole("radio", { name: OFF }));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(docker.createDomain).toHaveBeenCalledWith("example.com", "off", undefined);
  });

  it("returns focus to what opened the dialog", async () => {
    const user = setupUser();
    renderPage(fakeDocker());
    const add = await screen.findByRole("button", { name: /Add domain/ });
    await user.click(add);
    await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(add).toHaveFocus());

    await pick(user, "example.com", "Edit");
    await screen.findByRole("dialog");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.getByRole("button", { name: "Actions for example.com" })).toHaveFocus());
  });

  it("shows the proxy's reload warnings after a create or a delete", async () => {
    const user = setupUser();
    const docker = fakeDocker({
      createDomain: vi.fn().mockResolvedValue({
        domain: { name: "x.example.com", tls: "auto", apps: [] },
        warnings: ["ingress reload failed (previous routing still active): boom"],
      }),
      deleteDomain: vi.fn().mockResolvedValue(["ingress: app-1 skipped"]),
    });
    renderPage(docker);

    await user.click(await screen.findByRole("button", { name: /Add domain/ }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), "x.example.com");
    await user.click(within(dialog).getByRole("button", { name: "Add" }));
    await waitFor(() =>
      expect(toastWarning).toHaveBeenCalledWith("Domain added, but the proxy reported a problem", {
        description: "ingress reload failed (previous routing still active): boom",
      }),
    );

    await pick(user, "tunnel.example.com", "Remove");
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Remove domain" }));
    await waitFor(() =>
      expect(toastWarning).toHaveBeenCalledWith("Domain removed, but the proxy reported a problem", {
        description: "ingress: app-1 skipped",
      }),
    );
    expect(toastSuccess).not.toHaveBeenCalled();
  });

  // The dialog is driven directly and reopened WITHOUT a remount, so only
  // the wipe-on-close (not the page's per-open key) can clear the PEMs.
  it("wipes the certificate and key when the dialog closes", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    function Harness() {
      const [open, setOpen] = useState(true);
      return (
        <>
          <button type="button" onClick={() => setOpen(true)}>
            reopen
          </button>
          <DomainDialog docker={docker} open={open} replace={DOMAINS[0]} onOpenChange={setOpen} onSaved={() => {}} />
        </>
      );
    }
    render(<Harness />);
    const cert = () => within(screen.getByRole("dialog")).getByLabelText("Certificate (PEM)");
    const key = () => within(screen.getByRole("dialog")).getByLabelText("Private key (PEM)");
    const closedAndGone = async () => {
      await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
      expect(screen.queryByDisplayValue("SECRET")).not.toBeInTheDocument();
    };
    const reopenEmpty = async () => {
      await user.click(screen.getByRole("button", { name: "reopen" }));
      await screen.findByRole("dialog");
      expect(cert()).toHaveValue("");
      expect(key()).toHaveValue("");
    };

    await pastePEM(user, cert(), "CERT");
    await pastePEM(user, key(), "SECRET");
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Cancel" }));
    await closedAndGone();
    await reopenEmpty();

    await pastePEM(user, key(), "SECRET");
    await user.keyboard("{Escape}");
    await closedAndGone();
    await reopenEmpty();

    await pastePEM(user, cert(), "NEW");
    await pastePEM(user, key(), "SECRET");
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Save" }));
    await closedAndGone();
    expect(docker.createDomain).toHaveBeenCalledWith("*.twillingate.app", "certificate", { certificate: "NEW", key: "SECRET" });
    await reopenEmpty();
  });

  it("keeps the dialog open with the name and mode when saving fails", async () => {
    const user = setupUser();
    const docker = fakeDocker({ createDomain: vi.fn().mockRejectedValue(new Error("certificate does not cover the name")) });
    renderPage(docker);

    await user.click(await screen.findByRole("button", { name: /Add domain/ }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), "*.secure.app");
    await user.click(within(dialog).getByRole("radio", { name: OWN }));
    await pastePEM(user, within(dialog).getByLabelText("Certificate (PEM)"), "CERT");
    await pastePEM(user, within(dialog).getByLabelText("Private key (PEM)"), "KEY");
    await user.click(within(dialog).getByRole("button", { name: "Add" }));

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith("Couldn't add domain", { description: "certificate does not cover the name" }),
    );
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(within(dialog).getByLabelText("Name")).toHaveValue("*.secure.app");
    expect(within(dialog).getByRole("radio", { name: OWN })).toBeChecked();
    expect(within(dialog).getByLabelText("Private key (PEM)")).toHaveValue("KEY");
  });

  it("removes a domain after confirmation and keeps it on a refusal", async () => {
    const user = setupUser();
    const docker = fakeDocker({
      deleteDomain: vi.fn().mockResolvedValueOnce([]).mockRejectedValueOnce(new Error("example.com is used by Shop")),
    });
    renderPage(docker);

    await pick(user, "tunnel.example.com", "Remove");
    const unused = await screen.findByRole("alertdialog");
    expect(unused).toHaveTextContent("Apps on this server will no longer be able to use tunnel.example.com.");
    expect(unused).not.toHaveTextContent("used by");
    await user.click(within(unused).getByRole("button", { name: "Remove domain" }));
    expect(docker.deleteDomain).toHaveBeenCalledWith("tunnel.example.com");
    await waitFor(() => expect(screen.queryByText("tunnel.example.com")).not.toBeInTheDocument());
    expect(toastSuccess).toHaveBeenCalledWith("Domain removed");
    // The row's menu left with the row: focus goes to Add domain.
    await waitFor(() => expect(screen.getByRole("button", { name: /Add domain/ })).toHaveFocus());

    await pick(user, "example.com", "Remove");
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent("It's used by Shop: remove those addresses first");
    await user.click(within(confirm).getByRole("button", { name: "Remove domain" }));
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith("Couldn't remove domain", { description: "example.com is used by Shop" }),
    );
    // A refusal keeps the confirmation (to cancel) and the row.
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(screen.getAllByText("example.com")).toHaveLength(2); // the row, and the confirmation
    expect(docker.listDomains).toHaveBeenCalledTimes(1);
    // Cancelling after a refusal puts focus back on the row's menu.
    await user.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Actions for example.com" })).toHaveFocus());
  });

  it("lets a save answer late without closing the next opening of the dialog", async () => {
    const user = setupUser();
    let finish: (v: { domain: Domain; warnings: string[] }) => void = () => {};
    const docker = fakeDocker({
      createDomain: vi.fn(() => new Promise<{ domain: Domain; warnings: string[] }>((r) => (finish = r))),
    });
    renderPage(docker);

    await user.click(await screen.findByRole("button", { name: /Add domain/ }));
    let dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), "late.example.com");
    await user.click(within(dialog).getByRole("button", { name: "Add" }));
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    await user.click(screen.getByRole("button", { name: /Add domain/ }));
    dialog = await screen.findByRole("dialog");
    await act(async () => finish({ domain: { name: "late.example.com", tls: "auto", apps: [] }, warnings: [] }));
    // The domain was stored, so it is listed; the dialog now open is not the one that saved.
    expect(await screen.findByText("late.example.com")).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("says so when the list can't be loaded, or is empty", async () => {
    const { unmount } = renderPage(fakeDocker({ listDomains: vi.fn().mockRejectedValue(new Error("agent offline")) }));
    expect(await screen.findByText("Couldn't load domains.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
    unmount();

    const docker = fakeDocker({ listDomains: vi.fn().mockResolvedValue([]) });
    renderPage(docker);
    expect(await screen.findByText(/No domains yet/)).toBeInTheDocument();
    // No manual refresh: the list is read on open and after every change.
    expect(screen.queryByRole("button", { name: "Refresh domains" })).not.toBeInTheDocument();
  });
});

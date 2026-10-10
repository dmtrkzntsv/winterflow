import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import type { Docker } from "@/context/use-docker";
import type { RegisteredDomain } from "@/lib/domains";
import { DomainDialog, DomainsCard } from "@/pages/settings-domains";

const toastError = vi.fn();
const toastSuccess = vi.fn();
const toastWarning = vi.fn();
vi.mock("sonner", () => ({
  toast: {
    success: (...args: unknown[]) => toastSuccess(...args),
    error: (...args: unknown[]) => toastError(...args),
    warning: (...args: unknown[]) => toastWarning(...args),
  },
}));

// No per-keystroke delay: typing stays fast under a loaded CI runner.
const setupUser = () => userEvent.setup({ delay: null });

// pastePEM fills a PEM textarea in one paste, as users do (typing a real
// PEM key by key is slow and load-sensitive).
async function pastePEM(user: ReturnType<typeof setupUser>, field: HTMLElement, value: string) {
  await user.click(field);
  await user.paste(value);
}

const day = 24 * 60 * 60 * 1000;
const inDays = (n: number) => new Date(Date.now() + n * day).toISOString();

const DOMAINS: RegisteredDomain[] = [
  { name: "*.twillingate.app", tls: "certificate", not_after: inDays(10), issuer: "CF Origin", apps: ["a1", "a2"] },
  { name: "old.example.com", tls: "certificate", not_after: inDays(-1), apps: [] },
  { name: "tunnel.example.com", tls: "off", apps: [] },
  { name: "example.com", tls: "auto", apps: ["a1"] },
];

function fakeDocker(over: Partial<Docker> = {}): Docker {
  return {
    listDomains: vi.fn().mockResolvedValue(DOMAINS),
    createDomain: vi.fn().mockResolvedValue([]),
    deleteDomain: vi.fn().mockResolvedValue([]),
    ...over,
  } as unknown as Docker;
}

function renderCard(docker: Docker) {
  const apps = {
    apps: [
      { id: "a1", name: "Shop" },
      { id: "a2", name: "Blog" },
    ],
  } as unknown as AppsContextValue;
  return render(
    <MemoryRouter>
      <AppsContext.Provider value={apps}>
        <DomainsCard docker={docker} />
      </AppsContext.Provider>
    </MemoryRouter>,
  );
}

describe("DomainsCard", () => {
  beforeEach(() => {
    toastError.mockReset();
    toastSuccess.mockReset();
    toastWarning.mockReset();
  });

  it("lists names, modes, expiry and the apps using each", async () => {
    renderCard(fakeDocker());

    expect(await screen.findByText("*.twillingate.app")).toHaveClass("font-mono");
    expect(screen.getAllByText("Certificate").length).toBeGreaterThan(0);
    expect(screen.getByText("TLS off")).toBeInTheDocument();
    expect(screen.getByText("Auto TLS")).toBeInTheDocument();
    // Under 30 days is a warning; past is destructive.
    expect(screen.getByText(/^Expires /)).toHaveClass("text-amber-700");
    expect(screen.getByText(/^Expired /)).toHaveClass("text-destructive");
    expect(screen.getByText(/^Expires /)).toHaveAttribute("title", "Issued by CF Origin");
    expect(screen.getByText(/2 apps:/)).toBeInTheDocument();
    expect(screen.getAllByRole("link", { name: "Shop" })[0]).toHaveAttribute("href", "/app/a1");
    expect(screen.getByRole("link", { name: "Blog" })).toHaveAttribute("href", "/app/a2");
    expect(screen.getAllByText("Not used")).toHaveLength(2);
  });

  it("registers a domain with TLS off", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderCard(docker);

    await user.click(await screen.findByRole("button", { name: /Register domain/ }));
    const dialog = await screen.findByRole("dialog");
    const submit = within(dialog).getByRole("button", { name: "Register" });
    expect(submit).toBeDisabled();

    await user.type(within(dialog).getByLabelText("Name"), "*.*.bad");
    expect(within(dialog).getByText(/Use a lowercase hostname/)).toBeInTheDocument();
    expect(submit).toBeDisabled();

    await user.clear(within(dialog).getByLabelText("Name"));
    await user.type(within(dialog).getByLabelText("Name"), "*.Tunnel.Example.com");
    await user.click(within(dialog).getByRole("radio", { name: "TLS off" }));
    await user.click(submit);

    expect(docker.createDomain).toHaveBeenCalledWith("*.tunnel.example.com", "off", undefined);
    expect(docker.listDomains).toHaveBeenCalledTimes(2);
  });

  it("asks for a certificate and key in certificate mode", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderCard(docker);

    await user.click(await screen.findByRole("button", { name: /Register domain/ }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), "*.secure.app");
    expect(within(dialog).queryByLabelText("Certificate (PEM)")).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole("radio", { name: "Certificate" }));

    // A loaded certificate also serves matching auto hosts: the dialog says so.
    expect(within(dialog).getByText(/also serves any Auto TLS hostname it matches/)).toBeInTheDocument();
    const submit = within(dialog).getByRole("button", { name: "Register" });
    await pastePEM(user, within(dialog).getByLabelText("Certificate (PEM)"), "CERT");
    expect(submit).toBeDisabled(); // the key is still missing
    await pastePEM(user, within(dialog).getByLabelText("Private key (PEM)"), "KEY");
    await user.click(submit);

    expect(docker.createDomain).toHaveBeenCalledWith("*.secure.app", "certificate", {
      certificate: "CERT",
      key: "KEY",
    });
  });

  it("replaces a certificate for an existing name", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderCard(docker);

    await user.click(await screen.findByRole("button", { name: "Edit domain *.twillingate.app" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByLabelText("Name")).toHaveValue("*.twillingate.app");
    expect(within(dialog).getByLabelText("Name")).toHaveAttribute("readonly");
    expect(within(dialog).getByRole("radio", { name: "Certificate" })).toBeChecked();
    await pastePEM(user, within(dialog).getByLabelText("Certificate (PEM)"), "NEW");
    await pastePEM(user, within(dialog).getByLabelText("Private key (PEM)"), "KEY");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(docker.createDomain).toHaveBeenCalledWith("*.twillingate.app", "certificate", {
      certificate: "NEW",
      key: "KEY",
    });
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith("Domain updated"));
  });

  it("edits the TLS mode of any registration", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderCard(docker);

    // Every row is editable, not only certificate rows.
    for (const d of DOMAINS) {
      expect(await screen.findByRole("button", { name: `Edit domain ${d.name}` })).toBeInTheDocument();
    }
    await user.click(screen.getByRole("button", { name: "Edit domain example.com" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByLabelText("Name")).toHaveValue("example.com");
    expect(within(dialog).getByRole("radio", { name: "Auto TLS" })).toBeChecked();
    expect(within(dialog).queryByLabelText("Certificate (PEM)")).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole("radio", { name: "TLS off" }));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(docker.createDomain).toHaveBeenCalledWith("example.com", "off", undefined);
  });

  it("shows the proxy's reload warnings after a create or a delete", async () => {
    const user = setupUser();
    const docker = fakeDocker({
      createDomain: vi.fn().mockResolvedValue(["ingress reload failed (previous routing still active): boom"]),
      deleteDomain: vi.fn().mockResolvedValue(["ingress: app-1 skipped"]),
    });
    renderCard(docker);

    await user.click(await screen.findByRole("button", { name: /Register domain/ }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), "x.example.com");
    await user.click(within(dialog).getByRole("button", { name: "Register" }));
    await waitFor(() =>
      expect(toastWarning).toHaveBeenCalledWith("Domain registered, but the proxy reported a problem", {
        description: "ingress reload failed (previous routing still active): boom",
      }),
    );

    await user.click(screen.getByRole("button", { name: "Remove domain tunnel.example.com" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Remove domain" }));
    await waitFor(() =>
      expect(toastWarning).toHaveBeenCalledWith("Domain removed, but the proxy reported a problem", {
        description: "ingress: app-1 skipped",
      }),
    );
    expect(toastSuccess).not.toHaveBeenCalled();
  });

  // The dialog is driven directly and reopened WITHOUT a remount, so only
  // the wipe-on-close (not DomainsCard's per-open key) can clear the PEMs.
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
          <DomainDialog
            docker={docker}
            open={open}
            replace={DOMAINS[0]}
            onOpenChange={setOpen}
            onSaved={() => Promise.resolve()}
          />
        </>
      );
    }
    render(<Harness />);
    const cert = () => within(screen.getByRole("dialog")).getByLabelText("Certificate (PEM)");
    const key = () => within(screen.getByRole("dialog")).getByLabelText("Private key (PEM)");
    const closedAndGone = async () => {
      await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
      expect(screen.queryByDisplayValue("SECRET")).not.toBeInTheDocument();
      expect(screen.queryByLabelText("Private key (PEM)")).not.toBeInTheDocument();
    };
    const reopenEmpty = async () => {
      await user.click(screen.getByRole("button", { name: "reopen" }));
      await screen.findByRole("dialog");
      expect(cert()).toHaveValue("");
      expect(key()).toHaveValue("");
    };

    // Cancel after filling.
    await pastePEM(user, cert(), "CERT");
    await pastePEM(user, key(), "SECRET");
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Cancel" }));
    await closedAndGone();
    await reopenEmpty();

    // Escape after filling.
    await pastePEM(user, key(), "SECRET");
    await user.keyboard("{Escape}");
    await closedAndGone();
    await reopenEmpty();

    // A successful save.
    await pastePEM(user, cert(), "NEW");
    await pastePEM(user, key(), "SECRET");
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Save" }));
    await closedAndGone();
    expect(docker.createDomain).toHaveBeenCalledWith("*.twillingate.app", "certificate", {
      certificate: "NEW",
      key: "SECRET",
    });
    await reopenEmpty();
  });

  it("keeps the dialog open with the name and mode when saving fails", async () => {
    const user = setupUser();
    const docker = fakeDocker({
      createDomain: vi.fn().mockRejectedValue(new Error("certificate does not cover the name")),
    });
    renderCard(docker);

    await user.click(await screen.findByRole("button", { name: /Register domain/ }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), "*.secure.app");
    await user.click(within(dialog).getByRole("radio", { name: "Certificate" }));
    await pastePEM(user, within(dialog).getByLabelText("Certificate (PEM)"), "CERT");
    await pastePEM(user, within(dialog).getByLabelText("Private key (PEM)"), "KEY");
    await user.click(within(dialog).getByRole("button", { name: "Register" }));

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith("Couldn't register domain", {
        description: "certificate does not cover the name",
      }),
    );
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(within(dialog).getByLabelText("Name")).toHaveValue("*.secure.app");
    expect(within(dialog).getByRole("radio", { name: "Certificate" })).toBeChecked();
    // The inputs stay so the user can correct and retry.
    expect(within(dialog).getByLabelText("Private key (PEM)")).toHaveValue("KEY");
  });

  it("removes a domain after confirmation and shows a refusal", async () => {
    const user = setupUser();
    const docker = fakeDocker({
      deleteDomain: vi
        .fn()
        .mockResolvedValueOnce(undefined)
        .mockRejectedValueOnce(new Error("example.com is used by Shop (a1)")),
    });
    renderCard(docker);

    await user.click(await screen.findByRole("button", { name: "Remove domain tunnel.example.com" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Remove domain" }));
    expect(docker.deleteDomain).toHaveBeenCalledWith("tunnel.example.com");

    await user.click(screen.getByRole("button", { name: "Remove domain example.com" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Remove domain" }));
    expect(toastError).toHaveBeenCalledWith("Couldn't remove domain", {
      description: "example.com is used by Shop (a1)",
    });
  });

  it("says so when the list can't be loaded, or is empty", async () => {
    const { unmount } = renderCard(
      fakeDocker({ listDomains: vi.fn().mockRejectedValue(new Error("agent offline")) }),
    );
    expect(await screen.findByText("Couldn't load domains.")).toBeInTheDocument();
    unmount();

    renderCard(fakeDocker({ listDomains: vi.fn().mockResolvedValue([]) }));
    expect(await screen.findByText(/No domains yet/)).toBeInTheDocument();
  });
});

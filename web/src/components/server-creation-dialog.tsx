import { useEffect, useState, type FormEvent } from "react";
import { Check, Copy, ExternalLink, HelpCircle } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { CodeInput } from "@/components/ui/code-input";
import { Label } from "@/components/ui/label";
import { Disclosure } from "@/components/disclosure";
import { InlineError } from "@/components/inline-error";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { apiBase } from "@/config";
import { createJsonClient, jsonPost } from "@/lib/api";
import { copyText } from "@/lib/clipboard";
import { buildInstallCommand } from "@/lib/install-command";

// The install command points the new server at this instance; when it
// finishes it shows the 6-character code that connects the server here.
const INSTALL_COMMAND = buildInstallCommand(apiBase);
const CODE_LENGTH = 6;

type Provider = { name: string; url: string; logo: string; note: string };

// PROVIDERS are places to rent a server, for someone who has none yet.
const PROVIDERS: Provider[] = [
  { name: "Hetzner", url: "https://hetzner.cloud/?ref=qo1lNfZb6s3O", logo: "/partners/hetzner.svg", note: "affordable servers in Europe and the US" },
  { name: "DigitalOcean", url: "https://m.do.co/c/73a0e59acb28", logo: "/partners/digitalocean.svg", note: "simple pricing, easy to start" },
  { name: "Vultr", url: "https://www.vultr.com/?ref=9077581", logo: "/partners/vultr.svg", note: "many locations worldwide" },
  { name: "AWS", url: "https://aws.amazon.com", logo: "/partners/aws.svg", note: "Amazon's cloud, for larger setups" },
  { name: "Azure", url: "https://azure.microsoft.com", logo: "/partners/azure.svg", note: "Microsoft's cloud" },
];

// codeProblem turns the API's refusal of a code into plain words.
function codeProblem(message: string): string {
  if (message === "invalid registration code") {
    return "That code doesn't match a server waiting to connect. Check it and try again.";
  }
  if (message === "registration code has expired") {
    return "That code has expired. Get a new code from the server and try again.";
  }
  if (message === "no hub is available, try again in a minute") {
    return "Winterflow can't take new servers right now. Try again in a minute.";
  }
  if (message === "too many attempts, wait a minute") {
    return "Too many tries. Wait a minute, then try again.";
  }
  return message;
}

type Props = {
  isOpen: boolean;
  onClose: () => void;
  onServerAdded?: () => void;
};

// ServerSelectionDialog adds a server (distributed only: the standalone
// build runs one embedded server and never mounts it). Step 1 installs
// Winterflow on the machine with one command; step 2 enters the code the
// install shows. The API adds the server to the caller's own organization.
export function ServerSelectionDialog({ isOpen, onClose, onServerAdded }: Props) {
  const [step, setStep] = useState<1 | 2>(1);
  const [code, setCode] = useState("");
  const [copied, setCopied] = useState<"" | "yes" | "failed">("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  // reset puts the dialog back at step 1 for the next time it opens.
  // "Copied" is a confirmation, not a state: it goes back to "Copy".
  useEffect(() => {
    if (copied !== "yes") return;
    const timer = setTimeout(() => setCopied(""), 2000);
    return () => clearTimeout(timer);
  }, [copied]);

  const reset = () => {
    setStep(1);
    setCode("");
    setCopied("");
    setError("");
  };

  const close = () => {
    if (busy) return;
    reset();
    onClose();
  };

  const copy = async () => {
    try {
      await copyText(INSTALL_COMMAND);
      setCopied("yes");
    } catch {
      setCopied("failed");
    }
  };

  const submit = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (code.length !== CODE_LENGTH || busy) return;
    setBusy(true);
    setError("");
    try {
      const { server } = await createJsonClient(apiBase).call<{ server: { id: string; name: string } }>(
        "/api/v1/server/register",
        jsonPost({ code }),
      );
      toast.success(server.name ? `Added ${server.name}` : "Server added");
      reset();
      onServerAdded?.();
      onClose();
    } catch (err) {
      setError(codeProblem(err instanceof Error ? err.message : "Couldn't add the server"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <ResponsiveDialog
      open={isOpen}
      onOpenChange={(open) => {
        if (!open) close();
      }}
      title="Add a server"
      description={step === 1 ? "Step 1 of 2 · Install Winterflow on the server" : "Step 2 of 2 · Connect it"}
      className="sm:max-w-lg"
      footer={
        step === 1 ? (
          <>
            <Button variant="outline" className="max-sm:min-h-10" onClick={close}>
              Cancel
            </Button>
            <Button className="max-sm:min-h-10" onClick={() => setStep(2)}>
              Next: enter the code
            </Button>
          </>
        ) : (
          <>
            <Button
              variant="outline"
              className="max-sm:min-h-10"
              disabled={busy}
              onClick={() => {
                setError("");
                setStep(1);
              }}
            >
              Back
            </Button>
            <Button
              type="submit"
              form="add-server-form"
              className="max-sm:min-h-10"
              disabled={busy || code.length !== CODE_LENGTH}
            >
              {busy ? "Adding…" : "Add server"}
            </Button>
          </>
        )
      }
    >
      {step === 1 ? (
        <div className="grid gap-4 text-sm">
          <p className="text-muted-foreground">
            Run this command on the machine you want to add. It works on Ubuntu 22.04 or newer and Debian 12 or newer,
            with at least 1 CPU and 2 GB of memory.
          </p>
          <div className="flex items-start gap-2 rounded-lg border bg-muted/50 p-2">
            <pre
              data-testid="install-command"
              className="min-w-0 flex-1 select-all whitespace-pre-wrap break-all p-1 font-mono text-xs"
            >
              {INSTALL_COMMAND}
            </pre>
            <Button variant="ghost" size="sm" className="min-h-10 shrink-0 sm:min-h-8" onClick={() => void copy()}>
              {copied === "yes" ? <Check className="size-4" /> : <Copy className="size-4" />}
              {copied === "yes" ? "Copied" : "Copy"}
            </Button>
          </div>
          {copied === "failed" ? (
            <p role="status" className="text-muted-foreground">
              Couldn't copy it here. Select the command and copy it yourself.
            </p>
          ) : null}
          <p className="text-muted-foreground">
            When it finishes, it shows a 6-character code. You enter that code in the next step. The code changes every 10 minutes, and the installer stops waiting after 10 minutes, so run it again if it times out.{" "}
            <a
              href="https://docs.winterflow.io/quick-start"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 text-foreground underline underline-offset-2"
            >
              <HelpCircle className="size-3.5" aria-hidden />
              Help
            </a>
          </p>
          <Disclosure label="Don't have a server yet?">
            <p className="text-muted-foreground">Rent a small one from a provider, then run the command on it.</p>
            <ul className="grid gap-1">
              {PROVIDERS.map((p) => (
                <li key={p.name}>
                  <a
                    href={p.url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="flex min-h-10 items-center gap-3 rounded-md px-2 py-1.5 transition-colors duration-150 hover:bg-muted motion-reduce:transition-none"
                  >
                    <img src={p.logo} alt="" className="size-5 shrink-0 object-contain" />
                    <span className="min-w-0 flex-1">
                      <span className="font-medium">{p.name}</span>{" "}
                      <span className="text-muted-foreground">· {p.note}</span>
                    </span>
                    <ExternalLink className="size-4 shrink-0 text-muted-foreground" aria-hidden />
                  </a>
                </li>
              ))}
            </ul>
          </Disclosure>
        </div>
      ) : (
        <form id="add-server-form" className="grid gap-4 text-sm" onSubmit={(e) => void submit(e)}>
          <p className="text-muted-foreground">Enter the 6-character code the install command showed on the server.</p>
          <div className="grid gap-2">
            <Label htmlFor="server-code">Connection code</Label>
            <CodeInput
              id="server-code"
              length={CODE_LENGTH}
              value={code}
              onChange={(v) => {
                setCode(v);
                setError("");
              }}
              autoFocus
              disabled={busy}
            />
          </div>
          {error ? <InlineError icon>{error}</InlineError> : null}
        </form>
      )}
    </ResponsiveDialog>
  );
}

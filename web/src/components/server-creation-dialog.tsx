import * as React from "react";
import { Check, Copy, ExternalLink, HelpCircle, Plus, X } from "lucide-react";
import { Logo } from "@/components/app-logo";
import { Button } from "@/components/ui/button";
import { CodeInput } from "@/components/ui/code-input";
import { apiBaseUrl, isStandalone } from "@/config";
import {
  CreateOrganizationModal,
  type OrganizationPreview,
} from "@/components/create-organization-modal";

interface ServerSelectionDialogProps {
  isOpen: boolean;
  onClose: () => void;
  onServerAdded?: () => void;
}

interface Partner {
  id: string;
  name: string;
  url: string;
  logo: string;
  description: string;
  documentationUrl: string;
  color: string;
}

const partners: Partner[] = [
  {
    id: "hetzner",
    name: "Hetzner",
    url: "https://hetzner.cloud/?ref=qo1lNfZb6s3O",
    logo: "/partners/hetzner.svg",
    description:
      "European cloud provider with competitive pricing and excellent performance. Known for reliable infrastructure and great value for money.",
    documentationUrl: "",
    color: "#d50c2d",
  },
  {
    id: "digitalocean",
    name: "Digital Ocean",
    url: "https://m.do.co/c/73a0e59acb28",
    logo: "/partners/digitalocean.svg",
    description:
      "Developer-friendly cloud platform with simple pricing and intuitive interface. Perfect for startups and small to medium projects.",
    documentationUrl: "",
    color: "#0080ff",
  },
  {
    id: "vultr",
    name: "Vultr",
    url: "https://www.vultr.com/?ref=9077581",
    logo: "/partners/vultr.svg",
    description:
      "High-performance cloud infrastructure with global presence and SSD storage. Offers bare metal and cloud compute options.",
    documentationUrl: "",
    color: "#007bfc",
  },
  {
    id: "aws",
    name: "AWS",
    url: "https://aws.amazon.com",
    logo: "/partners/aws.svg",
    description:
      "Amazon's comprehensive cloud platform with extensive services and global infrastructure. Best for enterprise and complex workloads.",
    documentationUrl: "",
    color: "#ff9900",
  },
  {
    id: "azure",
    name: "Azure",
    url: "https://azure.microsoft.com",
    logo: "/partners/azure.svg",
    description:
      "Microsoft's cloud platform with strong integration with Microsoft ecosystem. Excellent for enterprise Windows workloads.",
    documentationUrl: "",
    color: "#0078d4",
  },
];

const defaultOrganizations: OrganizationPreview[] = [
  { id: "org-1", name: "Personal Workspace" },
  { id: "org-2", name: "Side Project" },
  { id: "org-3", name: "Client ABC" },
];

export function ServerSelectionDialog({
  isOpen,
  onClose,
  onServerAdded,
}: ServerSelectionDialogProps) {
  const [deviceCode, setDeviceCode] = React.useState("");
  const [isCopied, setIsCopied] = React.useState(false);
  const [isRegistering, setIsRegistering] = React.useState(false);
  const [registerError, setRegisterError] = React.useState<string | null>(null);
  const [selectedProvider, setSelectedProvider] = React.useState<Partner>(
    partners[0],
  );
  const [organizations, setOrganizations] =
    React.useState<OrganizationPreview[]>(defaultOrganizations);
  const [selectedOrganizationId, setSelectedOrganizationId] =
    React.useState<string>("");
  const [isCreateOrgModalOpen, setIsCreateOrgModalOpen] = React.useState(false);

  // Pre-select current agent's organization on mount (only when not already selected)
  React.useEffect(() => {
    if (selectedOrganizationId) return;

    if (organizations.length > 0) {
      setSelectedOrganizationId(organizations[0].id);
    }
  }, [organizations, selectedOrganizationId]);

  const copyToClipboard = async () => {
    const command = "curl -fsSL https://get.winterflow.io/agent | sudo bash";
    await navigator.clipboard.writeText(command);
    setIsCopied(true);
    setTimeout(() => setIsCopied(false), 2000);
  };

  const handleConnectAgent = async () => {
    // Standalone resolves the org server-side, so only distributed requires a
    // selected organization.
    if (deviceCode.length !== 6) return;
    if (!isStandalone && !selectedOrganizationId) return;

    setIsRegistering(true);
    setRegisterError(null);

    const base = apiBaseUrl.endsWith("/")
      ? apiBaseUrl.slice(0, -1)
      : apiBaseUrl;

    try {
      const response = await fetch(`${base}/api/v1/server/register`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          code: deviceCode,
          ...(selectedOrganizationId
            ? { organization_id: selectedOrganizationId }
            : {}),
        }),
      });

      const result = await response.json().catch(() => null);
      if (!response.ok || !result?.success) {
        throw new Error(result?.message ?? "Failed to add server");
      }

      setDeviceCode("");
      onServerAdded?.();
      onClose();
    } catch (err) {
      setRegisterError(
        err instanceof Error ? err.message : "Failed to add server",
      );
    } finally {
      setIsRegistering(false);
    }
  };

  const handleOrganizationCreated = (newOrganization: OrganizationPreview) => {
    setOrganizations((prev) => [...prev, newOrganization]);
    setSelectedOrganizationId(newOrganization.id);
    setIsCreateOrgModalOpen(false);
  };

  if (!isOpen) return null;

  if (isStandalone) {
    return (
      <div
        className="fixed inset-0 z-50 overflow-y-auto"
        aria-labelledby="agent-selection-title"
        role="dialog"
        aria-modal="true"
      >
        <div
          className="fixed inset-0 bg-background/80 backdrop-blur-sm transition-opacity"
          onClick={onClose}
        />
        <div className="flex min-h-full items-center justify-center p-4">
          <div className="relative w-full max-w-lg transform overflow-hidden rounded-lg bg-background shadow-2xl transition-all">
            <button
              type="button"
              className="absolute right-4 top-4 rounded-sm opacity-70 ring-offset-background transition-opacity hover:opacity-100 focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 disabled:pointer-events-none cursor-pointer"
              onClick={onClose}
            >
              <X className="h-4 w-4" />
              <span className="sr-only">Close</span>
            </button>
            <div className="p-6 space-y-6">
              <div className="flex flex-col gap-2 items-start">
                <Logo size="md" />
                <h2 className="text-2xl font-semibold">
                  Enter Connection Code
                </h2>
                                <p className="text-muted-foreground">
                                    When Winterflow starts it prints a 6-character device code in your terminal. Enter that code below to finish connecting this server.
                                </p>
              </div>
              <div className="flex justify-center">
                <CodeInput
                  length={6}
                  value={deviceCode}
                  onChange={setDeviceCode}
                  autoFocus
                  className="text-base"
                />
              </div>
              <Button
                variant="outline"
                className="w-full cursor-pointer bg-muted/80 hover:bg-muted/60"
                disabled={deviceCode.length !== 6 || isRegistering}
                onClick={handleConnectAgent}
              >
                {isRegistering ? "Connecting..." : "Connect Server"}
              </Button>
              {registerError && (
                <p className="text-sm text-destructive">{registerError}</p>
              )}
              <div className="rounded-lg border bg-muted/60 p-4 text-sm text-muted-foreground">
                <p className="font-medium text-foreground">Need help?</p>
                <p className="mt-1">
                  If something doesn&apos;t work, open an issue on{" "}
                  <a
                    href="https://github.com/winterflowio/winterflow/issues"
                    target="_blank"
                    rel="noopener noreferrer"
                    className="underline underline-offset-2 text-foreground"
                  >
                    GitHub
                  </a>
                  .
                </p>
              </div>
            </div>
          </div>
        </div>
      </div>
    );
  }

  return (
    <>
      <div
        className="fixed inset-0 z-50 overflow-y-auto"
        aria-labelledby="agent-selection-title"
        role="dialog"
        aria-modal="true"
      >
        {/* Backdrop */}
        <div
          className="fixed inset-0 bg-background/80 backdrop-blur-sm transition-opacity"
          onClick={onClose}
        />

        {/* Dialog */}
        <div className="flex min-h-full items-center justify-center p-0">
          <div className="relative w-full h-full lg:max-w-7xl lg:max-h-[90vh] lg:h-auto transform overflow-hidden lg:rounded-lg bg-background shadow-2xl transition-all">
            {/* Close button */}
            <button
              type="button"
              className="absolute right-4 top-4 rounded-sm opacity-70 ring-offset-background transition-opacity hover:opacity-100 focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 disabled:pointer-events-none cursor-pointer"
              onClick={onClose}
            >
              <X className="h-4 w-4" />
              <span className="sr-only">Close</span>
            </button>

            <div className="flex flex-col lg:flex-row">
              {/* Left side - Cloud Server */}
              <div className="flex-1 lg:w-1/2 p-6 lg:p-8">
                <div className="mb-8">
                  <div className="flex flex-col gap-4">
                    <Logo size="md" />
                    <p className="text-muted-foreground">
                      Don't have a server yet?
                    </p>
                  </div>
                </div>

                <div className="mb-8">
                  <div className="rounded-lg border p-6">
                    <h3 className="mb-4 text-sm font-medium text-muted-foreground">
                      Recommended Providers
                    </h3>

                    {/* Horizontal provider selection */}
                    <div className="flex gap-2 mb-6 overflow-x-auto pb-2">
                      {partners.map((partner) => (
                        <button
                          key={partner.id}
                          onClick={() => setSelectedProvider(partner)}
                          className={`px-4 py-3 rounded-lg border-2 transition-all whitespace-nowrap cursor-pointer ${
                            selectedProvider.id === partner.id
                              ? "bg-primary/5 shadow-sm"
                              : "border-border hover:bg-accent/50"
                          }`}
                          style={{
                            borderColor:
                              selectedProvider.id === partner.id
                                ? partner.color
                                : undefined,
                          }}
                        >
                          <span className="font-medium text-sm">
                            {partner.name}
                          </span>
                        </button>
                      ))}
                    </div>

                    {/* Selected provider description */}
                    <div className="space-y-4">
                      <div
                        className="p-4 bg-accent/30 rounded-lg border-l-4"
                        style={{ borderLeftColor: selectedProvider.color }}
                      >
                        <div className="flex items-start gap-4">
                          <div className="flex-1">
                            <h4 className="font-semibold text-sm mb-2">
                              {selectedProvider.name}
                            </h4>
                            <p className="text-sm text-foreground/80 leading-relaxed">
                              {selectedProvider.description}
                            </p>
                          </div>
                          <img
                            src={selectedProvider.logo}
                            alt={selectedProvider.name}
                            className="h-16 w-16 object-contain flex-shrink-0 cursor-pointer"
                            onClick={() =>
                              window.open(
                                selectedProvider.url,
                                "_blank",
                                "noopener,noreferrer",
                              )
                            }
                          />
                        </div>
                      </div>

                      <div className="flex flex-col sm:flex-row gap-3">
                        {selectedProvider.documentationUrl && (
                          <a
                            href={selectedProvider.documentationUrl}
                            target="_blank"
                            rel="noopener noreferrer"
                            className="flex items-center justify-center gap-2 px-4 py-2 border-2 rounded-lg hover:bg-accent transition-colors text-sm font-medium"
                            style={{ borderColor: selectedProvider.color }}
                          >
                            <span>Configuration Guide</span>
                          </a>
                        )}

                        <a
                          href={selectedProvider.url}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="flex items-center justify-center gap-2 px-4 py-2 rounded-lg hover:opacity-90 transition-colors text-sm font-medium text-white"
                          style={{ backgroundColor: selectedProvider.color }}
                        >
                          <span>Get a server on {selectedProvider.name}</span>
                          <ExternalLink className="h-4 w-4" />
                        </a>
                      </div>
                    </div>
                  </div>
                </div>

                {/* Requirements moved to left side */}
                <div className="mb-8">
                  <div className="rounded-lg border p-6">
                    <h3 className="mb-4 text-sm font-medium text-muted-foreground">
                      Requirements
                    </h3>
                    <ul className="space-y-2 text-sm">
                      <li className="flex items-center gap-2">
                        <svg
                          className="h-4 w-4 text-green-500"
                          fill="none"
                          viewBox="0 0 24 24"
                          stroke="currentColor"
                        >
                          <path
                            strokeLinecap="round"
                            strokeLinejoin="round"
                            strokeWidth="2"
                            d="M5 13l4 4L19 7"
                          />
                        </svg>
                        Ubuntu 22.04+ or Debian 12+
                      </li>
                      <li className="flex items-center gap-2">
                        <svg
                          className="h-4 w-4 text-green-500"
                          fill="none"
                          viewBox="0 0 24 24"
                          stroke="currentColor"
                        >
                          <path
                            strokeLinecap="round"
                            strokeLinejoin="round"
                            strokeWidth="2"
                            d="M5 13l4 4L19 7"
                          />
                        </svg>
                        Minimum 1 vCPU and 2GB RAM are recommended for Docker
                      </li>
                    </ul>
                  </div>
                </div>
              </div>

              {/* Right side - On-Prem Server */}
              <div className="w-full lg:w-1/2 border-t lg:border-t-0 lg:border-l bg-muted/50 p-6 lg:p-8">
                <div className="mb-8">
                  <h2 className="text-2xl font-bold">Bring Your Own Server</h2>
                  <p className="mt-2 text-muted-foreground">
                    Connect your existing server to WinterFlow's platform.
                  </p>
                </div>

                <div className="space-y-6">
                  {/* Organization Selection */}
                  {organizations.length > 0 && (
                    <div className="rounded-lg border p-6">
                      <div className="mb-4">
                        <h3 className="text-sm font-medium text-muted-foreground">
                          Select Organization
                        </h3>
                      </div>
                      <div className="flex gap-2 overflow-x-auto pb-2">
                        {organizations.map((org) => (
                          <button
                            key={org.id}
                            onClick={() => setSelectedOrganizationId(org.id)}
                            className={`px-4 py-3 rounded-lg border-2 transition-all whitespace-nowrap cursor-pointer ${
                              selectedOrganizationId === org.id
                                ? "bg-primary/5 shadow-sm border-primary"
                                : "border-border hover:bg-accent/50"
                            }`}
                          >
                            <span className="font-medium text-sm">
                              {org.name}
                            </span>
                          </button>
                        ))}
                        <button
                          onClick={() => setIsCreateOrgModalOpen(true)}
                          className="px-4 py-3 rounded-lg border-2 border-dashed transition-all whitespace-nowrap cursor-pointer text-muted-foreground hover:bg-accent/50"
                          title="Create new organization"
                        >
                          <span className="flex items-center gap-2 text-sm">
                            <Plus className="h-4 w-4" />
                            New
                          </span>
                        </button>
                      </div>
                    </div>
                  )}

                  <div className="rounded-lg border p-6">
                    <div className="flex items-center justify-between mb-4">
                      <h3 className="text-lg font-semibold">Quick Start</h3>
                      <a
                        href="https://docs.winterflow.io/quick-start"
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-sm text-muted-foreground hover:text-primary transition-colors flex items-center gap-1"
                      >
                        <HelpCircle className="h-4 w-4" />
                        Help
                      </a>
                    </div>
                    <ol className="space-y-4 text-sm">
                      <li className="flex gap-4">
                        <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-primary/10 text-sm font-medium">
                          1
                        </span>
                        <div className="flex-1 min-w-0">
                          <p className="font-medium">
                            Install the WinterFlow Agent
                          </p>
                          <div className="mt-2 flex items-center gap-2">
                            <pre className="flex-1 rounded bg-muted p-2 text-xs whitespace-pre-wrap break-all">
                              curl -fsSL https://get.winterflow.io/agent | sudo
                              bash
                            </pre>
                            <Button
                              variant="ghost"
                              size="icon"
                              className="h-8 w-8 shrink-0 cursor-pointer"
                              onClick={copyToClipboard}
                              title="Copy"
                            >
                              {isCopied ? (
                                <Check className="h-4 w-4 text-green-500" />
                              ) : (
                                <Copy className="h-4 w-4" />
                              )}
                              <span className="sr-only">Copy command</span>
                            </Button>
                          </div>
                        </div>
                      </li>
                      <li className="flex gap-4">
                        <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-primary/10 text-sm font-medium">
                          2
                        </span>
                        <div className="flex-1 min-w-0">
                          <p className="font-medium">Enter the code</p>
                          <p className="mt-1 text-muted-foreground">
                            After installation, you'll receive a 6-character
                            code to connect your server.
                          </p>
                          <div className="mt-4 w-full flex justify-center">
                            <CodeInput
                              length={6}
                              value={deviceCode}
                              onChange={setDeviceCode}
                              className="text-base"
                            />
                          </div>
                        </div>
                      </li>
                    </ol>
                  </div>

                  <div className="flex justify-center">
                    <Button
                      variant="outline"
                      className="w-[200px] cursor-pointer mt-6 bg-muted/80 hover:bg-muted/60"
                      disabled={
                        deviceCode.length !== 6 ||
                        isRegistering ||
                        !selectedOrganizationId
                      }
                      onClick={handleConnectAgent}
                    >
                      {isRegistering ? "Connecting..." : "Connect Server"}
                    </Button>
                    {registerError && (
                      <p className="mt-2 text-sm text-destructive">
                        {registerError}
                      </p>
                    )}
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
      <CreateOrganizationModal
        isOpen={isCreateOrgModalOpen}
        onClose={() => setIsCreateOrgModalOpen(false)}
        onSuccess={handleOrganizationCreated}
      />
    </>
  );
}

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";

const features = [
  "Local-first deploys",
  "Hybrid compute mesh",
  "Built-in tunnels",
  "Agent automation",
];

function App() {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <div className="mx-auto flex w-full max-w-6xl flex-col gap-16 px-6 py-16 lg:py-24">
        <header className="space-y-6 text-center">
          <Badge variant="secondary" className="rounded-full px-4 py-1 text-sm">
            Winterflow · Hybrid-first
          </Badge>
          <div className="space-y-4">
            <h1 className="text-4xl font-semibold tracking-tight text-balance md:text-6xl">
              Launch local apps with cloud-grade reach.
            </h1>
            <p className="text-lg text-muted-foreground md:text-xl">
              Agents, tunnels, and observability running on your own machines.
            </p>
          </div>
          <div className="flex flex-wrap justify-center gap-3">
            <Button size="lg">Deploy an app</Button>
            <Button size="lg" variant="outline">
              View dashboard
            </Button>
          </div>
        </header>

        <section className="grid gap-6 md:grid-cols-2">
          {features.map((feature) => (
            <Card key={feature} className="border-border/60 bg-card/60">
              <CardHeader>
                <CardTitle>{feature}</CardTitle>
                <CardDescription>
                  Everything you need to orchestrate Winterflow workloads.
                </CardDescription>
              </CardHeader>
              <CardContent className="text-sm text-muted-foreground">
                Run apps directly on your laptops or mini servers, keep them in
                sync with the control plane, and expose them via instant
                tunnels.
              </CardContent>
            </Card>
          ))}
        </section>

        <Separator />

        <section className="grid gap-8 lg:grid-cols-[1.1fr_0.9fr]">
          <Card>
            <CardHeader>
              <CardTitle>Unified control plane</CardTitle>
              <CardDescription>
                Switch between agents, logs, and tunnels without leaving the
                dashboard.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <Tabs defaultValue="agents" className="space-y-6">
                <TabsList className="grid w-full grid-cols-3">
                  <TabsTrigger value="agents">Agents</TabsTrigger>
                  <TabsTrigger value="apps">Apps</TabsTrigger>
                  <TabsTrigger value="tunnels">Tunnels</TabsTrigger>
                </TabsList>
                <TabsContent value="agents" className="space-y-3">
                  <p className="text-muted-foreground">
                    Monitor CPU, memory, and connection health in real time.
                  </p>
                  <div className="rounded-lg border border-border/60 bg-background/60 p-4">
                    <p className="font-mono text-sm text-muted-foreground">
                      wf agent register --label workstation
                    </p>
                  </div>
                </TabsContent>
                <TabsContent value="apps" className="space-y-3">
                  <p className="text-muted-foreground">
                    Deploy curated catalog apps to any agent.
                  </p>
                  <div className="rounded-lg border border-border/60 bg-background/60 p-4">
                    <p className="font-mono text-sm text-muted-foreground">
                      wf install metabase --agent=workstation
                    </p>
                  </div>
                </TabsContent>
                <TabsContent value="tunnels" className="space-y-3">
                  <p className="text-muted-foreground">
                    Every exposed port receives a hardened HTTPS endpoint.
                  </p>
                  <div className="rounded-lg border border-border/60 bg-background/60 p-4">
                    <p className="font-mono text-sm text-muted-foreground">
                      wf expose api --private
                    </p>
                  </div>
                </TabsContent>
              </Tabs>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Join the early access</CardTitle>
              <CardDescription>
                Drop your email and we&apos;ll send a download link.
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="name">Name</Label>
                <Input id="name" placeholder="Ada Lovelace" />
              </div>
              <div className="space-y-2">
                <Label htmlFor="email">Email</Label>
                <Input id="email" placeholder="you@winterflow.io" />
              </div>
              <Button className="w-full" size="lg">
                Request access
              </Button>
            </CardContent>
          </Card>
        </section>
      </div>
    </div>
  );
}

export default App;

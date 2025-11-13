Winterflow.io — Architecture & Vision

Overview

Winterflow is a local-first, hybrid-compute platform.
It turns your own machines—laptop, mini-PC, or home server—into cloud-grade servers you control.
•	Local-first: run apps directly on your hardware instead of renting VPS.
•	Hybrid-compute: connect local and remote nodes under one control plane.
•	Unified management: install, expose, and monitor apps with one CLI or dashboard.
•	Instant reach: secure tunnels give your local apps public HTTPS URLs.
•	Open core: written in Go, agent-based, using mTLS for security and Redis for event sync.

In short: Winterflow lets you deploy, manage, and share apps from your own compute—no cloud dependency, no setup friction.

⸻

Identity Model

At launch: 1 user = 1 organization.
•	Every user implicitly owns one isolated org namespace.
•	All agents, apps, and tunnels belong to that org.
•	Multi-user and shared orgs will come later.
•	Simplifies mTLS issuance, routing, and policy.

⸻

Core Architecture

Two planes:
1.	Control Plane — orchestration, coordination, metadata.
2.	Data Plane — tunneled network traffic for exposed apps.

1. Control Plane

UI → API ↔ Bus (Redis) ↔ Hub (gRPC + mTLS) ↔ Agents
DB → PostgreSQL

Components

Component	Role
UI	Built with TypeScript + React. Manages apps, agents, tunnels, and org settings.
API	REST / gRPC entrypoint for UI and CLI. Reads/writes PostgreSQL, publishes Redis events.
Bus (Redis)	Event bus for commands and state changes. Never carries user data.
Hub	Keeps persistent mTLS gRPC sessions with Agents. Dispatches deploys, collects logs, manages tunnel lifecycle.
Agent	Lightweight Go binary running on any node. Controls Docker runtime, executes tasks, opens tunnels.
Database (PostgreSQL)	Stores orgs, users, agents, apps, tunnels, domains, and security metadata.


⸻

2. Data Plane — Tunnels

DNS → Tunnel Gateway ↔ Tunnels (mTLS + JWT / QUIC / WebSocket) ↔ Agents

Component	Role
Tunnel Gateway	Shared ingress endpoint. Terminates TLS, authenticates tunnels, routes requests to the correct Agent/app.
Tunnel	Secure, short-lived channel per exposed app or port. Outbound from Agent → Gateway.
DNS	Maps public domains (app.wf.dev) to the Tunnel Gateway.

Key properties
•	One Agent can maintain multiple concurrent tunnels.
•	Public by default — each exposed app is immediately reachable via its public domain.
•	Optional private mode — restrict traffic to same-org agents (internal hybrid mesh).

⸻

Tunnel Authentication Model

Winterflow separates identity and authorization.

Layer	Mechanism	Purpose
Identity	mTLS certificate	Proves who the Agent is (agent_id, org_id). One long-lived cert per Agent.
Authorization	JWT tunnel token	Proves what the Agent may do. One short-lived token per tunnel.

JWT Claims Example

{
"sub": "tunnel:<tunnel_id>",
"tid": "<tunnel_id>",
"aid": "<agent_id>",
"org": "<org_id>",
"dom": "app1.wf.dev",
"mode": "public",
"exp": 1712345678
}

Gateway validates:
1.	mTLS cert chain → identifies Agent.
2.	JWT signature & expiry → authorizes tunnel.
3.	aid in JWT matches agent_id from cert.

⸻

Gateway Configuration Flow
1.	Source of truth — API + PostgreSQL.
2.	Distribution — Redis Bus.
3.	Consumers — Hubs + Tunnel Gateway.

Event-driven sync
•	API publishes tunnel.created, tunnel.updated, tunnel.closed.
•	Gateway subscribes and updates its in-memory routing map:

domain → tunnel_id → agent_id

	•	On startup, Gateway performs an initial sync via internal API:
GET /internal/tunnels/active.

Lifecycle
1.	CLI → wf expose <app>
2.	API creates tunnel row, publishes event.
3.	Hub instructs Agent to open tunnel.
4.	API/Hub issues short-lived JWT token for that tunnel.
5.	Agent opens outbound mTLS connection to Gateway, sends JWT.
6.	Gateway validates mTLS + JWT → binds live connection to domain.
7.	API marks tunnel as active.
8.	On close/revoke → event → Gateway drops mapping.

⸻

Hybrid Compute Model

All agents under one org form a single logical cluster.

Workload	Runs on	Reason
Frontend	VPS	Always-on, global latency
API backend	VPS or local	Cost vs latency trade-off
Heavy compute (LLM / analytics)	Local workstation / NAS	High power, zero cloud bill
Database	Local or remote	Developer preference

Agents are independent executors coordinated through the Hub.

⸻

Security Summary
•	Mutual TLS between Hub↔Agent and Gateway↔Agent
•	Automatic cert issuance + rotation
•	Per-tunnel JWT tokens (short-lived, revocable)
•	Role-based access control (user / admin / agent)
•	Public by default, optional org-private restriction
•	No Redis / DB on hot path

⸻

Component Summary

Layer	Hybrid Mode	Standalone (later)
Binaries	wf-api, wf-hub, wf-agent, wf-gateway	Single wf
Database	PostgreSQL	SQLite
Message Bus	Redis	In-memory
Runtime	Docker (per agent)	Docker (local)
Networking	gRPC + mTLS	Loopback only
Tunnels	Public default + optional private	None


⸻

Agent Responsibilities

Each Agent:
•	Registers with its Hub via mTLS.
•	Manages local Docker containers.
•	Streams logs / metrics to Hub.
•	Opens and maintains tunnels.
•	Handles multiple apps + tunnels concurrently.

Control plane: single persistent gRPC channel.
Data plane: multiple dedicated TCP / QUIC / WebSocket tunnels.

⸻

CLI Overview

wf init                          # Initialize project
wf deploy <app> --agent <id>     # Deploy to specific agent
wf expose <app>                  # Public tunnel (default)
wf expose <app> --private        # Restrict to same-org agents
wf logs <app>                    # Stream logs
wf agent register                # Register new agent
wf agents list                   # List all agents
wf tunnels list                  # List active tunnels


⸻

App Catalog

Install community apps instantly:

wf install metabase
wf install nocodb
wf install planka

Manifest example:

name: metabase
description: Business Intelligence tool
compose:
version: "3"
services:
metabase:
image: metabase/metabase:latest
ports:
- "3000:3000"
volumes:
- ./data:/data

	•	Docker Compose compatible
	•	Works from public / private repos
	•	Target specific agents

⸻

Monetization

Plan	Includes	Price
Self-Hosted	User runs API, Hub, Gateway, Agents	Free
Winterflow Cloud	Managed Hub + Gateway	$2 / agent / month
Team Bundle	4 agents included	$25 / month

You pay for orchestration + ingress, never for compute.

⸻

Technology Stack

Component	Choice
Language	Go
UI	TypeScript + React
Databases	PostgreSQL / SQLite
Message Bus	Redis
Transport	gRPC + mTLS
Tunnels	TCP / WebSocket / QUIC
Runtime	Docker
Auth	JWT + mTLS
Agents / CLI	Go binaries


⸻

Roadmap (Hybrid-First)
1.	Hybrid MVP — API, Hub, Redis, PostgreSQL, Agent deploys
2.	Identity (1 user = 1 org) — simplified auth and routing
3.	App Catalog v1 — curated open-source templates
4.	Tunnel Gateway — public default + org-private mode
5.	Security Automation — cert bootstrap + JWT rotation
6.	Standalone Mode — single-binary SQLite deployment

⸻

Winterflow Agents are the backbone of the hybrid platform—
a secure, open, extensible system that merges local compute with public reachability.

Public by default. Private when you choose. One user, one organization, total control.
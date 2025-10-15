<!--
Sync Impact Report
Version change: (none prior) → 1.0.0
Modified principles: (initial creation)
Added sections: Core Principles; Quality Gates & Standards; Development Workflow & Practices; Governance
Removed sections: None
Templates requiring updates:
 - .specify/templates/plan-template.md ✅ updated
 - .specify/templates/tasks-template.md ✅ updated
 - .specify/templates/spec-template.md ✅ updated
 - .specify/templates/agent-file-template.md ✅ no change needed (derives dynamically)
Deferred TODOs: None
-->

# Winterflow Constitution

## Core Principles

### 1. Readable & Deterministic Code
Code MUST be written so an unfamiliar engineer can explain any function's purpose and
side‑effects within 2 minutes. Public APIs MUST be pure or clearly document side
effects. Non-determinism (time, randomness, network) MUST be isolated behind
interfaces that provide deterministic test seams (e.g., clock, UUID, external
service ports). Cyclomatic complexity > 10 or functions > 40 LOC MUST be justified
in review or refactored. Hidden magic, implicit global state, and undocumented
concurrency are prohibited.

Rationale: Predictable, inspectable code lowers defect rate and speeds onboarding.

### 2. Modular Maintainability & Clear Boundaries
Modules MUST have a single, documented responsibility and stable contracts.
Cross-module calls MUST go through explicit interfaces (no deep internal imports).
Any new dependency MUST document: why needed, alternative considered, removal
strategy. Breaking changes MUST provide a migration note before merge. Dead code
is removed in the same PR it is detected. Architecture decisions impacting
multiple modules require an ADR (Architecture Decision Record) referenced in the
PR.

Rationale: Sharp module boundaries reduce cascade changes and enable parallel work.

### 3. Automated Quality Assurance (Tests + Static Gates)
Every user story MUST add or update automated unit tests for the domain layer. 
New or changed logic without tests is a hard review stop. Minimum coverage target:
90% project-wide AND 100% of changed lines (diff coverage). Static analysis (lint, type checks, formatting) MUST pass before merge. Test names MUST describe intent (behavior-first). Flaky tests are
quarantined within 24h and fixed before next release. Security / input validation
paths MUST have negative tests.

Rationale: Continuous assurance prevents regression drift and encodes intent.

### 4. Consistent & Accessible User Experience
User-facing outputs (CLI, API responses, UI states, logs intended for users) MUST
follow a documented style: consistent casing, error shape, and latency budget.
APIs MUST be versioned; removing or changing fields requires deprecation process
(announce → grace period → removal). Accessibility: textual outputs must be
parsable by screen readers where applicable (e.g., structured JSON) and avoid
color-only signaling. Performance is part of UX: p95 latency for primary actions
MUST meet published success criteria in the spec. Error messages MUST state what
happened, why (if determinable), and next step.

Rationale: Consistency lowers cognitive load and raises user trust.

### 5. Observability, Performance & Reliability as UX
All critical paths MUST emit structured logs (at INFO for success, WARN/ERROR for
failures) and key metrics (latency, error rate). Any feature adding async or
distributed behavior MUST include tracing spans. Performance regressions >10% p95
in critical endpoints block merges unless justified. Feature toggles MUST default
off until observability dashboards exist. Recoverability: unhandled exceptions are
treated as defects; graceful degradation paths MUST be tested.

Rationale: You cannot improve what you cannot observe; reliability is perceived UX.

## Quality Gates & Standards

1. Pre-Design Gate (Plan Phase): Complexity justification required for additions
	exceeding baseline (new module, external dependency, or data store).
2. Pre-Implementation Gate: Constitution Check in plan MUST enumerate how each
	principle is satisfied or note a justified deviation with expiry date.
3. Pre-Merge Gate: All automated checks green; diff coverage 100%; no TODO/FIXME
	in changed lines unless linked to an issue.
4. Release Gate: No open high-severity defects, no quarantined flaky tests older
	than 7 days, observability dashboards green for new features.
5. Post-Release Review (within 5 business days): Validate success criteria & UX
	consistency; capture learnings in ADR or retrospective note.

Standards:
 - Code Style: Enforced by formatter; manual style debates disallowed.
 - Dependency Hygiene: Age of unused feature flags > 60 days triggers removal.
 - Documentation: Public modules MUST have top-level usage example.

## Development Workflow & Practices

1. Plan → Spec → Tasks pipeline: each artifact references constitution compliance.
2. TDD encouraged: write failing contract + unit tests before implementation.
3. PR Reviews: MUST include checklist: readability, boundaries, tests, UX impact.
4. ADRs: Stored in /docs/adr/ with sequential numbering; referenced in PR body.
5. Branch Naming: <issue|feature>-<slug>; feature branches short-lived (<5 days).
6. Continuous Integration: Fails fast on lint/type/test; performance smoke tests
	run daily; full performance suite weekly.
7. Incident Learnings: Postmortem required for Sev1/Sev2 within 48h; action items
	tracked and reviewed during governance cycle.

## Governance

Authority: This constitution supersedes prior undocumented conventions.

Amendments:
 - Proposal PR must include: change rationale, impact analysis, version bump type,
	migration/deprecation steps if applicable.
 - MINOR bump: new principle or substantial expansion.
 - MAJOR bump: removal or breaking redefinition of a principle / governance rule.
 - PATCH bump: clarifications or editorial fixes only.

Review Cadence:
 - Quarterly compliance audit sampling recent PRs, test health, coverage, UX
	consistency, and observability completeness.
 - Metrics reported in a governance summary doc.

Enforcement:
 - Blocking CI checks enforce gates.
 - Reviewers MUST reject changes violating principles without documented
	justification.
 - Repeated non-compliance escalated to maintainers for remediation plan.

Versioning Policy: Semantic (MAJOR.MINOR.PATCH) as defined above. CHANGELOG entry
required for each MINOR or MAJOR governance change.

Exception Process: Temporary deviations require: issue link, expiry date, owner,
rollback plan. Expired exceptions auto-fail the Constitution Check.

**Version**: 1.0.0 | **Ratified**: 2025-10-14 | **Last Amended**: 2025-10-14

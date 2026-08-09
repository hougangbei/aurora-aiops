# Asset Management and Project Deployment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver the complete asset onboarding, inventory, project-center installation, single-node Kubernetes deployment, and persistent progress experience defined by the approved design.

**Architecture:** The work is split into four dependency-ordered plans. `assets` owns servers, encrypted credentials, host keys, SSH and inventory; `deployment` owns durable tasks, steps and events; project installers plug into that task engine without exposing arbitrary commands. The React client consumes separate asset/project APIs and resumes task progress through SSE with polling fallback.

**Tech Stack:** Go 1.25, Gin, SQLite, `golang.org/x/crypto/ssh`, React 19, TypeScript, TanStack Query, Ant Design, Vitest.

---

## Execution order

1. [Phase 1 — Asset inventory and agentless SSH](./2026-08-09-asset-inventory-ssh.md)
2. [Phase 2 — Durable deployment tasks and progress](./2026-08-09-deployment-task-progress.md)
3. [Phase 3 — Aurora AIOps project installation](./2026-08-09-aurora-project-installation.md)
4. [Phase 4 — Single-node kubeadm deployment](./2026-08-09-kubeadm-project-installation.md)

Each plan must be implemented and verified before starting the next. Do not collapse phases into one commit: the accepted boundary requires asset read paths to remain usable even when the deployment engine or an installer is unavailable.

## Cross-plan invariants

- Never return or log credential plaintext, credential ciphertext, nonces, private keys, passphrases, sudo data, kubeconfig or project environment values.
- All new routes remain under the existing Session and `EnforcePlatformRBAC` middleware.
- Read APIs allow viewer; connection tests and inventory refresh allow operator/admin; all credential, host-key and deployment mutations require admin.
- A server can exist without a successful connection or host-key confirmation.
- A server has at most one non-terminal deployment task.
- Task percentage is monotonic; terminal states are immutable; retries create a new linked task.
- Remote mutation comes only from compiled-in, versioned installers. No request field can become an arbitrary shell command.
- Every plan ends with `go test ./...`, `go vet ./...`, `npm test`, `npm run build`, `git diff --check`, and a clean secret scan.

## Completion evidence

| Approved requirement | Authoritative evidence |
| --- | --- |
| Add an uninitialized/offline server | Phase 1 route and UI tests create a `pending` server without opening SSH |
| View server status and installed software | Phase 1 collector/parser tests plus asset-details UI tests |
| Open server details and lists | Phase 1 router/UI tests for overview, software, installations and tasks tabs |
| Project center | Phase 3 project catalog API and project-center UI tests |
| Install Aurora AIOps | Phase 3 fake-remote integration test and Linux release artifact verification |
| One-click Kubernetes | Phase 4 recorded-command installer test and blank-VM acceptance script |
| Progress bar | Phase 2 monotonic state-machine tests and SSE reconnect/polling UI tests |
| Safe credentials and remote execution | Phase 1 cipher/host-key tests and all-phase redaction tests |

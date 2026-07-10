# Control Tower

Control Tower V1 is an independent monitoring and light-operations system for multiple new-api instances.

## V1 Boundaries

- Control Tower is deployed independently from new-api.
- Each new-api server runs one outbound-only Go agent.
- The agent is not AI and does not call any model.
- The agent reads the new-api `logs` table through a read-only account.
- The agent reports to Control Tower Server over HTTPS.
- Control Tower does not modify new-api source code, routes, Nginx, or database schema.
- Control Tower does not enter the user request path.
- Control Tower stores and queries its own database.
- Control Tower does not store full request bodies or full response bodies.
- Automatic weight execution is disabled by default.

## Phase 1 Scope

Phase 1 builds the smallest production-shaped loop:

1. Workspace and documentation skeleton.
2. Agent and Server JSON contracts.
3. Control Tower database schema draft.
4. Agent configuration, state, safe log-event conversion, and reporter.
5. Server agent gateway for heartbeat/report ingestion.

Web/H5, alerts, notification sending, controlled operations, and deployment hardening are later phases.


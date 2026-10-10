# Control Tower Docs

This folder stores Control Tower-specific design contracts, schema notes, and production checklists.

Current phase documents:

- [Monitor error statistics repair](tasks/2026-10-09-monitor-error-statistics-repair.md): Agent minute aggregation, idempotent statistics ingestion, coverage-aware error-code trends and upgrade requirements.

- [Request monitor implementation](tasks/2026-10-09-request-monitor-implementation.md): ALB site-wide minute trends, latest-minute selection, merged-histogram slow channels, tests and production acceptance gaps.

- `tasks/2026-10-09-alb-access-log-connection.md`: ALB access-log connection settings, encrypted database storage, manual connectivity tests, verification and rollout requirements.

- `design-request-pressure-monitor.md`: Request pressure monitoring review draft — request-size trends, channel latency, resource risk alerts, collection contracts, and acceptance criteria (design only, not implemented).

- `devlog/index.html`: Local dev-log site — timeline of releases, bugfixes, incidents, reviews, and decisions (open directly in a browser; data in `devlog/devlog-data.js`, appended by the review workflow).
- `iteration-log.md`: Version-by-version iteration log — release rationale, dev/deploy issues, known limits, and next steps for every shipped version (start here to catch up).
- `design-v1.1-early-warning.md`: v1.1 alerting design — active channel probing, completion-silence detection, trend pre-warnings; closes the 600s timeout blind spot.
- `codex-batches-plan.md`: Batch execution plan for Codex (v1.1 B1-B4 then M1) with per-batch dev approach and human verification checkpoints.
- `codex-task-v1.1-b1.md`: Detailed Codex instructions for batch B1 (slow-return rule + episode event log).
- `development-plan.md`: Full delivery plan (milestones M0-M5) from current state to a shippable product with Web and mobile App.
- `development-progress.md`: V1 development stage board, current phase, and verification checkpoints.
- `deployment-error-alert.md`: Frozen snapshot of the v1.0 error-alert agent deployment (superseded by iteration-log.md).
- `api-contracts.md`: Agent-to-Server API payload contract.
- `schema.md`: Control Tower-owned database schema notes.

# Control Tower Docs

This folder stores Control Tower-specific design contracts, schema notes, and production checklists.

Current phase documents:

- `devlog/index.html`: Local dev-log site — timeline of releases, bugfixes, incidents, reviews, and decisions (open directly in a browser; data in `devlog/devlog-data.js`, appended by the review workflow).
- `iteration-log.md`: Version-by-version iteration log — release rationale, dev/deploy issues, known limits, and next steps for every shipped version (start here to catch up).
- `design-v1.1-early-warning.md`: v1.1 alerting design — active channel probing, completion-silence detection, trend pre-warnings; closes the 600s timeout blind spot.
- `development-plan.md`: Full delivery plan (milestones M0-M5) from current state to a shippable product with Web and mobile App.
- `development-progress.md`: V1 development stage board, current phase, and verification checkpoints.
- `deployment-error-alert.md`: Frozen snapshot of the v1.0 error-alert agent deployment (superseded by iteration-log.md).
- `api-contracts.md`: Agent-to-Server API payload contract.
- `schema.md`: Control Tower-owned database schema notes.

# Dynamic weighting automatic execution

Dynamic weighting supports three tuning modes:

- `observe`: record `rebalance` recommendations only.
- `confirm`: create pending recommendations and wait for an operator.
- `auto`: persist the recommendation, then atomically create a
  `channel.update` command and an operation audit.

A successful automatic action is marked `auto_executed`. Command delivery and
Agent execution remain visible through the existing command lifecycle. A
transaction failure leaves the recommendation pending and never reports a
false successful execution.

Automatic weighting only compares enabled, non-demoted channels at the same
highest priority for one model. Every channel must meet `min_samples`, and at
least two eligible channels are required.

The proposed multiplier is clamped, smoothed, and rate-limited before it is
applied to the current weight. Automatic changes share the scheduling cooldown
and daily action limit with priority changes. Fixed degradation and recovery
rules run before dynamic weighting, so an unhealthy channel is degraded instead
of receiving a weight adjustment.

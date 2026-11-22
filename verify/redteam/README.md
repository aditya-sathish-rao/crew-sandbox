# `verify/redteam/`

Adversarial simulations. Each script plays the part of a compromised agent and tries to
escape the boundary the way a prompt injection would.

- `exfil_sim` — attempts to reach an off-list host, read a credential from the
  environment or `/proc`, and reach the broker port. Every attempt must fail.

A red-team script is expected to fail closed: if the attack succeeds, the script exits
non-zero. These are the tests that catch a boundary that looks correct and leaks.

None of these are ported yet.

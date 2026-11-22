# `verify/redteam/`

Adversarial simulations. Each script plays the part of a compromised agent and tries to
escape the boundary the way a prompt injection would.

- `exfil_sim` — dumps the environment, attempts unauthorized HTTP and DNS egress, and
  confirms containment.

A red-team script is expected to fail closed: if the attack succeeds, the script exits
non-zero. These are the tests that catch a boundary that looks correct but leaks.

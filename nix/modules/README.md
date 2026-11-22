# `nix/modules/`

Nix modules exported to consumers. `darwinModules.default` is the intended entry point
for `agentic-dev-env`.

A module here configures the host: the guest definition, the egress allowlist, the
broker's secret provider. It must not assume a username, a home directory, or a dotfiles
layout — every path is a module option with a documented default.

The guest's configuration is the other kind of module, and it lives in
[`../guest/`](../guest/): it *is* the agent user's home, so it fixes that layout rather
than accepting it.

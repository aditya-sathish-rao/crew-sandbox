# `verify/gates/`

One script per invariant. A gate proves a property of the running sandbox and fails
loudly when the property does not hold.

- `env_audit` — no secret pattern appears in guest process environments or `/proc`.
- `fs_boundary` — host credential paths are unreachable from inside the guest.

A gate is a test of the boundary, not of the code that implements it. It should fail
when the boundary is broken even if every unit test passes.

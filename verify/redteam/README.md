# `verify/redteam/`

Adversarial simulations: scripts that try to escape the boundary the way a
prompt-injected agent would.

Nothing is here yet. The original system had no red-team scripts either — its
adversarial cases live inside the phase gates, as assertions that an attack fails. For
example `proxy.sh` asserts that a spoofed `Host` header cannot retarget a plain-HTTP
request and that a tunnel whose SNI names another host is refused. Those are attacks,
written as gates.

A red-team script belongs here when an attack needs more than one assertion to describe,
or when it needs to run against a deliberately broken build to prove the gate catches it.

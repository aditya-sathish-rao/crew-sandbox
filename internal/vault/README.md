# `internal/vault`

The secret provider interface. The broker and the proxy need a GitHub App private key
and a model API key; neither should know where those come from.

`AtomicVault` shells out to `av inject`, which is how the original `crew-sandbox.sh`
read secrets. `Static` and `FromEnv` exist for tests and for operators who export
secrets. `Chain` tries providers in order and stops on a real error, so a broken vault
does not silently fall through to an environment variable.

A refusal from `av` — "human approval required" in a non-interactive context — is
classified as not-found so the chain can fall through. A genuine failure is not.

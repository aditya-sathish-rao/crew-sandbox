# `examples/`

Minimal consumer flakes showing how to import this repo.

Orchestrator wiring — `herdr`, `first-mate`, or anything else that runs a crew — lives
here and nowhere else. Core logic stays tool-agnostic; an example is how a specific tool
adapts to the contract, not how the contract adapts to a tool.

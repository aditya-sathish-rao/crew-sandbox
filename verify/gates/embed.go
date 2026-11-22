// Package gates carries the phase gates: bash scripts that prove a property of
// the running sandbox from the side that could break it.
//
// They are embedded rather than read from the source tree because the operator's
// binary is the only thing that ships. A gate left beside its source would be
// invisible to anyone who installed the flake, and `crew-sandbox check` would have
// nothing to run.
package gates

import "embed"

// Scripts is everything a phase needs: the driver, one script per phase, and the
// ruleset the broker gate tells the operator to apply. `crew-sandbox check` stages
// the whole set into a private temporary directory and runs the driver from there,
// so the driver can find its siblings by path.
//
//go:embed *.sh *.json
var Scripts embed.FS

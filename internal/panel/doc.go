// Package panel is the tree of bull-Tunnel's control panel.
//
// The panel is MIT licensed and the tunnel engine (internal/engine/) is
// AGPL-3.0. The two are synchronized by contract, not by linking: the panel
// reads the engine's files — the configuration it writes, the metrics the
// engine writes — and drives the engine over SSH, and it never imports a
// package of the engine.
//
// That rule is not a style: a program that links AGPL code is AGPL, whatever
// else is in it. Two programs that talk over a socket are two works, and
// each keeps its own licence. The test in this directory keeps the rule
// true as the panel grows.
//
// The panel's own packages arrive with the phases of the rewrite: the model
// and the store, the SSH layer, the driver of the engine, the DNS clients,
// the orchestrator and the API.
package panel

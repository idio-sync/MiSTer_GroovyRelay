package launchcore

// EmptyHostMessage is the operator-facing error used when a launch-core
// action is requested before bridge.mister.host is configured. It is shared
// by cmd/mister-groovy-relay's SSH launcher and internal/chassis's handler so
// the two surfaces cannot drift.
const EmptyHostMessage = "MiSTer host not configured (set bridge.mister.host)"

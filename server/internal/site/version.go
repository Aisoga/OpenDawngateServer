package site

import "dawngate/server/internal/version"

// Version is the running build's version for the status page: version.String, the same one the players' server list
// shows (a release's name, or dev-<commit> for a developer build).
func Version() string { return version.String() }

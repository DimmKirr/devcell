package main

import "github.com/google/uuid"

// devcellNamespace is a fixed UUIDv5 namespace for deriving deterministic
// session IDs from APP_NAME. Generated once, never changes.
var devcellNamespace = uuid.NewSHA1(uuid.NameSpaceDNS, []byte("devcell.sh"))

// sessionUUID returns a deterministic UUIDv5 derived from appName.
// Same appName (project+pane) always produces the same UUID, so agents
// resume the same conversation when relaunched in the same tmux pane.
func sessionUUID(appName string) string {
	return uuid.NewSHA1(devcellNamespace, []byte(appName)).String()
}

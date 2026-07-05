package serve

import (
	"fmt"
	"strings"
)

func BuildRDPFile(host string, port int, username string, appProgram string) string {
	host = sanitizeRDPValue(host)
	username = sanitizeRDPValue(username)

	lines := []string{
		fmt.Sprintf("full address:s:%s:%d", host, port),
		fmt.Sprintf("server port:i:%d", port),
		fmt.Sprintf("username:s:%s", username),
		"screen mode id:i:1",
		"desktopwidth:i:1920",
		"desktopheight:i:1080",
		"prompt for credentials:i:0",
		"authentication level:i:0",
	}
	if appProgram != "" {
		lines = append(lines,
			"remoteapplicationmode:i:1",
			fmt.Sprintf("remoteapplicationprogram:s:%s", sanitizeRDPValue(appProgram)),
			"remoteapplicationcmdline:s:",
		)
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}

func sanitizeRDPValue(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

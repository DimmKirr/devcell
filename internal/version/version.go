package version

var (
	Version   = "v0.0.0"
	GitCommit = "none"
	BuildDate = "unknown"
)

func Full() string {
	return Version + "-" + BuildDate + "-" + GitCommit
}

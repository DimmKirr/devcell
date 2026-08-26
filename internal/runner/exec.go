package runner

// ExecSpec holds the parameters for building a docker exec argv.
type ExecSpec struct {
	ContainerName string
	Binary        string
	Args          []string
	TTY           bool
}

// BuildExecArgv constructs a docker exec argv for attaching to a running container.
func BuildExecArgv(spec ExecSpec) []string {
	argv := []string{"docker", "exec"}
	if spec.TTY {
		argv = append(argv, "-it")
	}
	argv = append(argv, spec.ContainerName, spec.Binary)
	argv = append(argv, spec.Args...)
	return argv
}

package serve

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

type DockerAPIClient interface {
	ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error)
}

type DockerEnumerator struct {
	client DockerAPIClient
}

func NewDockerEnumerator() (*DockerEnumerator, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &DockerEnumerator{client: cli}, nil
}

func (d *DockerEnumerator) ListCells() []Cell {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	opts := container.ListOptions{
		Filters: filters.NewArgs(filters.Arg("label", "devcell.basedir")),
	}

	containers, err := d.client.ContainerList(ctx, opts)
	if err != nil {
		log.Printf("workspace: docker list failed: %v", err)
		return nil
	}

	var cells []Cell
	for _, c := range containers {
		rdpPort := findRDPPort(c.Ports)
		if rdpPort == 0 {
			continue
		}

		basedir := c.Labels["devcell.basedir"]
		cellid := c.Labels["devcell.cellid"]
		project := filepath.Base(basedir)

		name := containerShortName(c.Names)

		cells = append(cells, Cell{
			ID:    fmt.Sprintf("%s-%s", name, cellid),
			Title: fmt.Sprintf("%s-%s", project, cellid),
			Host:  "localhost",
			Port:  int(rdpPort),
			Type:  "Desktop",
		})
	}
	return cells
}

func findRDPPort(ports []container.Port) uint16 {
	for _, p := range ports {
		if p.PrivatePort == 3389 && p.PublicPort > 0 {
			return p.PublicPort
		}
	}
	return 0
}

func containerShortName(names []string) string {
	if len(names) == 0 {
		return "cell"
	}
	name := strings.TrimPrefix(names[0], "/")
	name = strings.TrimSuffix(name, "-run")
	return name
}

package serve

import (
	"context"
	"testing"

	"github.com/docker/docker/api/types/container"
)

type fakeDockerClient struct {
	containers []container.Summary
	err        error
}

func (f *fakeDockerClient) ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
	return f.containers, f.err
}

func TestDockerEnumerator_ListCells_BasicDiscovery(t *testing.T) {
	client := &fakeDockerClient{
		containers: []container.Summary{
			{
				Names: []string{"/cell-myproject-42-run"},
				Labels: map[string]string{
					"devcell.basedir": "/Users/dmitry/dev/myorg/myproject",
					"devcell.cellid":  "42",
					"devcell.stack":   "ultimate",
				},
				Ports: []container.Port{
					{PrivatePort: 3389, PublicPort: 14289, Type: "tcp"},
					{PrivatePort: 5900, PublicPort: 14250, Type: "tcp"},
				},
			},
		},
	}

	enum := &DockerEnumerator{client: client}
	cells := enum.ListCells()

	if len(cells) != 1 {
		t.Fatalf("got %d cells, want 1", len(cells))
	}
	c := cells[0]
	if c.ID != "cell-myproject-42-42" {
		t.Errorf("ID = %q, want cell-myproject-42-42", c.ID)
	}
	if c.Title != "myproject-42" {
		t.Errorf("Title = %q, want myproject-42", c.Title)
	}
	if c.Port != 14289 {
		t.Errorf("Port = %d, want 14289", c.Port)
	}
	if c.Type != "Desktop" {
		t.Errorf("Type = %q, want Desktop", c.Type)
	}
}

func TestDockerEnumerator_ListCells_MultipleContainers(t *testing.T) {
	client := &fakeDockerClient{
		containers: []container.Summary{
			{
				Names:  []string{"/cell-projectA-1-run"},
				Labels: map[string]string{"devcell.basedir": "/dev/projectA", "devcell.cellid": "1"},
				Ports:  []container.Port{{PrivatePort: 3389, PublicPort: 10089, Type: "tcp"}},
			},
			{
				Names:  []string{"/cell-projectB-2-run"},
				Labels: map[string]string{"devcell.basedir": "/dev/projectB", "devcell.cellid": "2"},
				Ports:  []container.Port{{PrivatePort: 3389, PublicPort: 20089, Type: "tcp"}},
			},
		},
	}

	enum := &DockerEnumerator{client: client}
	cells := enum.ListCells()

	if len(cells) != 2 {
		t.Fatalf("got %d cells, want 2", len(cells))
	}
	if cells[0].Title != "projectA-1" {
		t.Errorf("cells[0].Title = %q", cells[0].Title)
	}
	if cells[1].Title != "projectB-2" {
		t.Errorf("cells[1].Title = %q", cells[1].Title)
	}
}

func TestDockerEnumerator_ListCells_SkipsContainersWithoutRDP(t *testing.T) {
	client := &fakeDockerClient{
		containers: []container.Summary{
			{
				Names:  []string{"/cell-noport-1-run"},
				Labels: map[string]string{"devcell.basedir": "/dev/noport", "devcell.cellid": "1"},
				Ports:  []container.Port{{PrivatePort: 8080, PublicPort: 8080, Type: "tcp"}},
			},
		},
	}

	enum := &DockerEnumerator{client: client}
	cells := enum.ListCells()

	if len(cells) != 0 {
		t.Errorf("got %d cells, want 0 (no RDP port)", len(cells))
	}
}

func TestDockerEnumerator_ListCells_DockerError_ReturnsEmpty(t *testing.T) {
	client := &fakeDockerClient{
		err: context.DeadlineExceeded,
	}

	enum := &DockerEnumerator{client: client}
	cells := enum.ListCells()

	if len(cells) != 0 {
		t.Errorf("got %d cells on error, want 0", len(cells))
	}
}

func TestDockerEnumerator_ListCells_TitleFromBasedir(t *testing.T) {
	tests := []struct {
		basedir string
		want    string
	}{
		{"/Users/dmitry/dev/dimmkirr/devcell", "devcell-1"},
		{"/Users/dmitry/dev/kirr/kirr.dev", "kirr.dev-1"},
		{"/Users/dmitry/dev/evercars/evercars-backend", "evercars-backend-1"},
		{"/simple", "simple-1"},
	}

	for _, tt := range tests {
		client := &fakeDockerClient{
			containers: []container.Summary{{
				Names:  []string{"/cell-test-1-run"},
				Labels: map[string]string{"devcell.basedir": tt.basedir, "devcell.cellid": "1"},
				Ports:  []container.Port{{PrivatePort: 3389, PublicPort: 3389, Type: "tcp"}},
			}},
		}
		enum := &DockerEnumerator{client: client}
		cells := enum.ListCells()
		if len(cells) != 1 {
			t.Errorf("basedir=%q: got %d cells", tt.basedir, len(cells))
			continue
		}
		if cells[0].Title != tt.want {
			t.Errorf("basedir=%q: Title=%q, want %q", tt.basedir, cells[0].Title, tt.want)
		}
	}
}

var _ DockerAPIClient = (*fakeDockerClient)(nil)

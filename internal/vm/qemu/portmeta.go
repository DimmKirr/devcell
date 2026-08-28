package qemu

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// PortMeta records the allocated ports for a running QEMU VM instance.
// Written to ports.json in the instance directory for discovery by cell vnc/rdp.
type PortMeta struct {
	SSHPort uint16 `json:"ssh"`
	VNCPort uint16 `json:"vnc"`
	RDPPort uint16 `json:"rdp"`
}

func WritePortMeta(instanceDir string, pm PortMeta) error {
	data, err := json.Marshal(pm)
	if err != nil {
		return fmt.Errorf("marshal port meta: %w", err)
	}
	return os.WriteFile(filepath.Join(instanceDir, "ports.json"), data, 0644)
}

func ReadPortMeta(instanceDir string) (PortMeta, error) {
	data, err := os.ReadFile(filepath.Join(instanceDir, "ports.json"))
	if err != nil {
		return PortMeta{}, err
	}
	var pm PortMeta
	if err := json.Unmarshal(data, &pm); err != nil {
		return PortMeta{}, fmt.Errorf("unmarshal port meta: %w", err)
	}
	return pm, nil
}

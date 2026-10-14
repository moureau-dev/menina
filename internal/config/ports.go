package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// PortsSpec declares raw TCP/UDP ports to expose for an app (L4, bypassing the
// HTTP proxy). It accepts several shapes:
//
//	ports: 25565                     # one TCP port
//	ports: [25565, 25566]            # TCP ports
//	ports: {tcp: 25565, udp: 19132}  # explicit
//	ports: {tcp: [25565], udp: [19132, 19133]}
//
// A bare number or list defaults to TCP.
type PortsSpec struct {
	TCP []int
	UDP []int
}

// IsZero reports whether no ports are declared.
func (p PortsSpec) IsZero() bool { return len(p.TCP) == 0 && len(p.UDP) == 0 }

func (p *PortsSpec) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var n int
		if err := value.Decode(&n); err != nil {
			return fmt.Errorf("ports: %w", err)
		}
		p.TCP = []int{n}
	case yaml.SequenceNode:
		var list intOrList
		if err := value.Decode(&list); err != nil {
			return fmt.Errorf("ports: %w", err)
		}
		p.TCP = []int(list)
	case yaml.MappingNode:
		var raw struct {
			TCP intOrList `yaml:"tcp"`
			UDP intOrList `yaml:"udp"`
		}
		if err := value.Decode(&raw); err != nil {
			return fmt.Errorf("ports: %w", err)
		}
		p.TCP = []int(raw.TCP)
		p.UDP = []int(raw.UDP)
	default:
		return fmt.Errorf("ports must be a number, a list, or {tcp, udp}")
	}
	return nil
}

func (p PortsSpec) MarshalYAML() (any, error) {
	if len(p.UDP) == 0 {
		switch len(p.TCP) {
		case 0:
			return nil, nil
		case 1:
			return p.TCP[0], nil
		default:
			return p.TCP, nil
		}
	}
	out := map[string]any{}
	if len(p.TCP) > 0 {
		out["tcp"] = p.TCP
	}
	out["udp"] = p.UDP
	return out, nil
}

// intOrList decodes a YAML scalar or sequence of ints.
type intOrList []int

func (l *intOrList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var n int
		if err := value.Decode(&n); err != nil {
			return err
		}
		*l = []int{n}
	case yaml.SequenceNode:
		var list []int
		if err := value.Decode(&list); err != nil {
			return err
		}
		*l = list
	default:
		return fmt.Errorf("expected a port number or list of numbers")
	}
	return nil
}

package config

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPortsSpecUnmarshal(t *testing.T) {
	cases := []struct {
		in       string
		tcp, udp []int
	}{
		{"ports: 25565", []int{25565}, nil},
		{"ports: [25565, 25566]", []int{25565, 25566}, nil},
		{"ports: {tcp: 25565, udp: 19132}", []int{25565}, []int{19132}},
		{"ports: {udp: [19132, 19133]}", nil, []int{19132, 19133}},
	}
	for _, c := range cases {
		var e ExposureSpec
		if err := yaml.Unmarshal([]byte(c.in), &e); err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if !reflect.DeepEqual(e.Ports.TCP, c.tcp) || !reflect.DeepEqual(e.Ports.UDP, c.udp) {
			t.Fatalf("%q -> tcp=%v udp=%v, want tcp=%v udp=%v", c.in, e.Ports.TCP, e.Ports.UDP, c.tcp, c.udp)
		}
	}
}

func TestValidateExposedPorts(t *testing.T) {
	reserved := &ResolvedApp{Project: "p", Environment: "prod", Name: "web", Exposure: ExposureSpec{Ports: PortsSpec{TCP: []int{80}}}}
	if err := validateExposedPorts(reserved, map[string]string{}); err == nil {
		t.Fatal("exposing tcp/80 must be rejected")
	}
	bad := &ResolvedApp{Project: "p", Environment: "prod", Name: "web", Exposure: ExposureSpec{Ports: PortsSpec{TCP: []int{70000}}}}
	if err := validateExposedPorts(bad, map[string]string{}); err == nil {
		t.Fatal("out-of-range port must be rejected")
	}
	seen := map[string]string{}
	a := &ResolvedApp{Project: "a", Environment: "prod", Name: "x", Exposure: ExposureSpec{Ports: PortsSpec{UDP: []int{19132}}}}
	if err := validateExposedPorts(a, seen); err != nil {
		t.Fatal(err)
	}
	b := &ResolvedApp{Project: "b", Environment: "prod", Name: "y", Exposure: ExposureSpec{Ports: PortsSpec{UDP: []int{19132}}}}
	if err := validateExposedPorts(b, seen); err == nil {
		t.Fatal("duplicate udp/19132 across apps must be rejected")
	}
}

func TestPortsSpecMarshal(t *testing.T) {
	out, err := yaml.Marshal(ExposureSpec{Ports: PortsSpec{UDP: []int{19132}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "udp:") || !strings.Contains(string(out), "19132") {
		t.Fatalf("marshal = %s", out)
	}
}

package deployassets

import (
	"strings"
	"testing"
)

func TestServeUnitWritesAndSetgidDirs(t *testing.T) {
	unit, err := RenderUnit("menina-serve.service", map[string]string{
		"CONFIG_DIR": "/etc/menina",
		"DATA_DIR":   "/var/lib/menina",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(unit, "ReadWritePaths=/etc/menina /var/lib/menina /run/menina") {
		t.Fatalf("serve unit must allow writes to the config dir:\n%s", unit)
	}
	if !strings.Contains(unit, "RestrictSUIDSGID=false") {
		t.Fatalf("serve unit must allow setgid config directories:\n%s", unit)
	}
}

func TestProxyUnitKeepsSUIDSGIDRestriction(t *testing.T) {
	unit, err := RenderUnit("menina-proxy.service", map[string]string{
		"CONFIG_DIR": "/etc/menina",
		"DATA_DIR":   "/var/lib/menina",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(unit, "RestrictSUIDSGID=true") {
		t.Fatalf("proxy unit should keep RestrictSUIDSGID:\n%s", unit)
	}
}

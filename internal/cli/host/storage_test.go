package host

import (
	"strings"
	"testing"

	"github.com/moureau-dev/menina/internal/core/config"
)

func TestParseStorageSpecs(t *testing.T) {
	tests := []struct {
		name    string
		values  []string
		mount   string
		want    []config.StorageSpec
		wantErr string
	}{
		{name: "none"},
		{
			name:   "legacy single",
			values: []string{"1Gi"},
			mount:  "/data",
			want:   []config.StorageSpec{{Name: "data", Enabled: true, Size: "1Gi", MountPath: "/data"}},
		},
		{
			name:   "named",
			values: []string{"data=1Gi:/data", "cache=512Mi:/cache"},
			want: []config.StorageSpec{
				{Name: "data", Enabled: true, Size: "1Gi", MountPath: "/data"},
				{Name: "cache", Enabled: true, Size: "512Mi", MountPath: "/cache"},
			},
		},
		{
			name:   "named with default mount",
			values: []string{"data=1Gi", "cache=512Mi:/cache"},
			mount:  "/srv",
			want: []config.StorageSpec{
				{Name: "data", Enabled: true, Size: "1Gi", MountPath: "/srv"},
				{Name: "cache", Enabled: true, Size: "512Mi", MountPath: "/cache"},
			},
		},
		{
			name:    "multiple needs names",
			values:  []string{"1Gi", "512Mi"},
			wantErr: "needs a name",
		},
		{
			name:    "duplicate name",
			values:  []string{"data=1Gi:/data", "data=512Mi:/cache"},
			wantErr: "duplicate storage name",
		},
		{
			name:    "duplicate path",
			values:  []string{"data=1Gi:/data", "cache=512Mi:/data"},
			wantErr: "same mount path",
		},
		{
			name:    "missing size",
			values:  []string{"data=:/data"},
			wantErr: "needs a size",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseStorageSpecs(tc.values, tc.mount)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("storage = %+v, want %+v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("storage[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestSelectStorage(t *testing.T) {
	single := config.Storages{{Name: "data", Enabled: true}}
	if got, err := selectStorage(single, ""); err != nil || got.Name != "data" {
		t.Fatalf("single select = %+v, %v", got, err)
	}

	multi := config.Storages{{Name: "data", Enabled: true}, {Name: "cache", Enabled: true}}
	if _, err := selectStorage(multi, ""); err == nil || !strings.Contains(err.Error(), "--volume") {
		t.Fatalf("expected a --volume error, got %v", err)
	}
	if got, err := selectStorage(multi, "cache"); err != nil || got.Name != "cache" {
		t.Fatalf("named select = %+v, %v", got, err)
	}
	if _, err := selectStorage(multi, "missing"); err == nil || !strings.Contains(err.Error(), "no volume named") {
		t.Fatalf("expected an unknown-volume error, got %v", err)
	}
}

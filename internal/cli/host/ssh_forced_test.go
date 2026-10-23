package host

import "testing"

func TestParseForcedCommand(t *testing.T) {
	for _, input := range []string{"menina status", "status --json", "app status api"} {
		args, err := parseForcedCommand(input)
		if err != nil || len(args) == 0 {
			t.Fatalf("%q: args=%v err=%v", input, args, err)
		}
	}
	for _, input := range []string{"", "setup", "menina deploy", "bash -c id", "identity profile list"} {
		if _, err := parseForcedCommand(input); err == nil {
			t.Fatalf("%q was allowed", input)
		}
	}
}

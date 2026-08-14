package actions

import (
	"os"
	"regexp"
	"sort"
	"testing"
)

// pythonSource is the original the registry is checked against.
const pythonSource = "../../original/dockerapi/modules/DockerApi.py"

// pythonMethod finds the method definitions main.py resolved with getattr.
var pythonMethod = regexp.MustCompile(`(?m)^\s*def (container_post__\w+)\s*\(`)

// The strongest evidence of completeness: the key set has to match the method names
// of the Python original exactly. If an action is missing or named differently, this
// test fails.
func TestRegistryCoversEveryPythonMethod(t *testing.T) {
	src, err := os.ReadFile(pythonSource)
	if err != nil {
		t.Skipf("the original is not readable: %v", err)
	}

	matches := pythonMethod.FindAllStringSubmatch(string(src), -1)
	if len(matches) == 0 {
		t.Fatal("no methods found in the original — check the pattern")
	}

	expected := make(map[string]bool, len(matches))
	for _, m := range matches {
		expected[m[1]] = true
	}

	var missing, extra []string

	for name := range expected {
		if _, ok := Registry[name]; !ok {
			missing = append(missing, name)
		}
	}
	for name := range Registry {
		if !expected[name] {
			extra = append(extra, name)
		}
	}

	sort.Strings(missing)
	sort.Strings(extra)

	if len(missing) > 0 {
		t.Errorf("the registry is missing %d actions: %v", len(missing), missing)
	}
	if len(extra) > 0 {
		t.Errorf("the registry knows %d unknown actions: %v", len(extra), extra)
	}

	t.Logf("%d actions compared", len(expected))
}

// The count is pinned as well, so the test still says something when the original is
// no longer in the tree.
func TestRegistrySize(t *testing.T) {
	const want = 29

	if got := len(Registry); got != want {
		t.Errorf("the registry holds %d actions, want %d", got, want)
	}
}

func TestRegistryHasNoNilEntries(t *testing.T) {
	for name, fn := range Registry {
		if fn == nil {
			t.Errorf("%s has no function", name)
		}
	}
}

func TestLookup(t *testing.T) {
	if _, ok := Lookup("container_post__stop"); !ok {
		t.Error("container_post__stop was not found")
	}
	if _, ok := Lookup("container_post__does_not_exist"); ok {
		t.Error("an unknown name resolved")
	}
}

// MethodName has to build the same names as main.py:155 and main.py:222.
func TestMethodName(t *testing.T) {
	tests := []struct {
		name       string
		postAction string
		cmd        string
		task       string
		want       string
	}{
		{"a simple action", "stop", "", "", "container_post__stop"},
		{"an action with irrelevant extras", "restart", "whatever", "whatever", "container_post__restart"},
		{"exec", "exec", "mailq", "delete", "container_post__exec__mailq__delete"},
		{"exec with an underscore", "exec", "mailq", "super_delete", "container_post__exec__mailq__super_delete"},
		{"an unknown exec", "exec", "foo", "bar", "container_post__exec__foo__bar"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MethodName(tt.postAction, tt.cmd, tt.task); got != tt.want {
				t.Errorf("MethodName(%q, %q, %q) = %q, want %q",
					tt.postAction, tt.cmd, tt.task, got, tt.want)
			}
		})
	}
}

// Every registry entry has to be reachable through MethodName.
func TestEveryRegistryKeyIsReachableViaMethodName(t *testing.T) {
	reachable := map[string]bool{}

	for name := range Registry {
		parts := splitMethodName(name)
		switch len(parts) {
		case 2:
			reachable[MethodName(parts[1], "", "")] = true
		case 4:
			reachable[MethodName(parts[1], parts[2], parts[3])] = true
		default:
			t.Errorf("%s has an unexpected shape", name)
		}
	}

	for name := range Registry {
		if !reachable[name] {
			t.Errorf("%s is not reachable through MethodName", name)
		}
	}
}

// splitMethodName splits a registry key at the double underscores — the same
// separator main.py used when composing it.
func splitMethodName(name string) []string {
	return regexp.MustCompile(`__`).Split(name, -1)
}

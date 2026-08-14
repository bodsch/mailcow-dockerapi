package actions

import (
	"os"
	"regexp"
	"sort"
	"testing"
)

// pythonSource ist die Vorlage, gegen die die Registry geprüft wird.
const pythonSource = "../../original/dockerapi/modules/DockerApi.py"

// pythonMethod findet die Methodendefinitionen, die main.py per getattr auflöste.
var pythonMethod = regexp.MustCompile(`(?m)^\s*def (container_post__\w+)\s*\(`)

// Der stärkste Nachweis für Vollständigkeit: die Schlüsselmenge muss exakt
// den Methodennamen der Python-Vorlage entsprechen. Fehlt eine Action oder
// heißt sie anders, schlägt der Test fehl.
func TestRegistryCoversEveryPythonMethod(t *testing.T) {
	src, err := os.ReadFile(pythonSource)
	if err != nil {
		t.Skipf("Vorlage nicht lesbar: %v", err)
	}

	matches := pythonMethod.FindAllStringSubmatch(string(src), -1)
	if len(matches) == 0 {
		t.Fatal("keine Methoden in der Vorlage gefunden – Muster pruefen")
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
		t.Errorf("in der Registry fehlen %d Actions: %v", len(missing), missing)
	}
	if len(extra) > 0 {
		t.Errorf("die Registry kennt %d unbekannte Actions: %v", len(extra), extra)
	}

	t.Logf("%d Actions abgeglichen", len(expected))
}

// Die Anzahl ist zusätzlich festgeschrieben, damit der Test auch dann etwas
// aussagt, wenn die Vorlage nicht mehr im Baum liegt.
func TestRegistrySize(t *testing.T) {
	const want = 29

	if got := len(Registry); got != want {
		t.Errorf("Registry umfasst %d Actions, want %d", got, want)
	}
}

func TestRegistryHasNoNilEntries(t *testing.T) {
	for name, fn := range Registry {
		if fn == nil {
			t.Errorf("%s ist nicht belegt", name)
		}
	}
}

func TestLookup(t *testing.T) {
	if _, ok := Lookup("container_post__stop"); !ok {
		t.Error("container_post__stop nicht gefunden")
	}
	if _, ok := Lookup("container_post__gibtsnicht"); ok {
		t.Error("unbekannter Name wurde aufgeloest")
	}
}

// MethodName muss dieselben Namen bilden wie main.py:155 und main.py:222.
func TestMethodName(t *testing.T) {
	tests := []struct {
		name       string
		postAction string
		cmd        string
		task       string
		want       string
	}{
		{"einfache aktion", "stop", "", "", "container_post__stop"},
		{"aktion mit leeren zusaetzen", "restart", "egal", "egal", "container_post__restart"},
		{"exec", "exec", "mailq", "delete", "container_post__exec__mailq__delete"},
		{"exec mit unterstrich", "exec", "mailq", "super_delete", "container_post__exec__mailq__super_delete"},
		{"exec unbekannt", "exec", "foo", "bar", "container_post__exec__foo__bar"},
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

// Jeder Registry-Eintrag muss über MethodName erreichbar sein.
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
			t.Errorf("%s hat eine unerwartete Form", name)
		}
	}

	for name := range Registry {
		if !reachable[name] {
			t.Errorf("%s ist ueber MethodName nicht erreichbar", name)
		}
	}
}

// splitMethodName zerlegt einen Registry-Schlüssel an den doppelten
// Unterstrichen – dieselbe Trennung, die main.py beim Zusammenbau verwendete.
func splitMethodName(name string) []string {
	return regexp.MustCompile(`__`).Split(name, -1)
}

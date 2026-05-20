package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultComposeFilenames is the order nyx tries when no -f/--file is given:
// nyxd-specific first, then Docker / Compose spec defaults, then Podman.
var DefaultComposeFilenames = []string{
	"nyx-compose.yaml", "nyx-compose.yml",
	"docker-compose.yaml", "docker-compose.yml",
	"compose.yaml", "compose.yml",
	"podman-compose.yaml", "podman-compose.yml",
}

// DefaultComposePath returns the absolute path to the first existing compose file
// in dir among [DefaultComposeFilenames].
func DefaultComposePath(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = "."
	}
	base, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for _, name := range DefaultComposeFilenames {
		p := filepath.Join(base, name)
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue
		}
		return filepath.Abs(p)
	}
	return "", fmt.Errorf("no compose file in %s (looked for %s)", base, strings.Join(DefaultComposeFilenames, ", "))
}

// DefaultProjectFromComposeFile returns the stack project name when the client omits an
// explicit project. It uses the compose file's parent directory basename (Docker Compose
// style) so two stacks that both use e.g. docker-compose.yml in different folders do not
// share the same default project or container IDs. If the parent directory is not a
// usable label (e.g. compose file at filesystem root), it falls back to the compose
// filename stem.
func DefaultProjectFromComposeFile(abs string) string {
	abs = filepath.Clean(abs)
	dir := filepath.Dir(abs)
	base := strings.TrimSpace(filepath.Base(dir))
	// Unix: filepath.Base("/") == "/". Treat drive roots and odd dirs as unusable.
	if base == "" || base == "." || base == "/" || strings.HasSuffix(base, ":") {
		return strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
	}
	return base
}

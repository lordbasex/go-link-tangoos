package appsys

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Binarios que el Centro de apps ejecuta (ADR 0009: exec como último recurso, con
// argumentos armados por código). Corren como unidades transitorias de systemd,
// fuera del sandbox del agente (apt necesita escribir /usr; compose, crear redes).
var (
	AptGet  = "/usr/bin/apt-get"
	Dockerd = "/usr/sbin/dockerd"
	// docker compose v2: Debian trae el binario standalone y/o el plugin del CLI.
	composeCandidates = [][]string{
		{"/usr/bin/docker-compose"},
		{"/usr/bin/docker", "compose"}, // con /usr/libexec/docker/cli-plugins/docker-compose
	}
	composePlugins = []string{"/usr/libexec/docker/cli-plugins/docker-compose", "/usr/lib/docker/cli-plugins/docker-compose"}
)

// ComposeBin devuelve el argv base de docker compose, o error si no está instalado.
func ComposeBin() ([]string, error) {
	if isExec(composeCandidates[0][0]) {
		return composeCandidates[0], nil
	}
	if isExec("/usr/bin/docker") {
		for _, p := range composePlugins {
			if isExec(p) {
				return composeCandidates[1], nil
			}
		}
	}
	return nil, errors.New("docker compose no está instalado")
}

func isExec(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

var appIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ComposeArgs arma la invocación de compose para la app id con su directorio de proyecto.
// sub son los argumentos del subcomando (p. ej. "up", "-d"), siempre de una lista fija.
func ComposeArgs(base []string, id, dir string, sub ...string) ([]string, error) {
	if !appIDRe.MatchString(id) {
		return nil, errors.New("id de app inválido")
	}
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || !strings.HasSuffix(dir, "/@apps/"+id) {
		return nil, errors.New("directorio de app inválido")
	}
	for _, s := range sub {
		if !allowedSub[s] {
			return nil, errors.New("subcomando de compose no permitido: " + s)
		}
	}
	argv := append([]string{}, base...)
	argv = append(argv, "--ansi", "never", "--progress", "plain",
		"-p", ProjectPrefix+id,
		"--project-directory", dir,
		"-f", filepath.Join(dir, "compose.yaml"),
		"--env-file", filepath.Join(dir, ".env"))
	return append(argv, sub...), nil
}

var allowedSub = map[string]bool{
	"pull": true, "up": true, "-d": true, "--remove-orphans": true, "--wait": true,
	"stop": true, "start": true, "restart": true, "down": true, "--volumes": true,
	"--rmi": true, "all": true, "--quiet": true,
}

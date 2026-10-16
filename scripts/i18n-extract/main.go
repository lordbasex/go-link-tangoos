// i18n-extract junta los textos en español que tangod y tango-agent le pueden mandar a la
// interfaz (errores, avisos, títulos y progreso de jobs) y los escribe, ordenados, en
// web/packages/client/src/i18n/server.es.json. La interfaz los traduce con los formatos
// de web/packages/client/src/i18n/server.en.ts (i18n.tr); un test de la web exige que
// cada uno tenga traducción.
//
//	go run ./scripts/i18n-extract          regenera la lista
//	go run ./scripts/i18n-extract -check   falla si la lista no está al día (make check)
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	accent    = regexp.MustCompile(`[áéíóúñÁÉÍÓÚÑ¿¡]`)
	stopwords = regexp.MustCompile(`(?i)(^|[\s(«"])(el|la|los|las|de|del|un|una|no|se|que|para|con|sin|en|es|ya|hay|está|están|tiene|desde|hasta|puede|pudo|falta|elegí|existe|todavía|ahora|otro|otra|nodo|volumen|carpeta|disco|grupo|pool)\s`)
	codey     = regexp.MustCompile("^[\\w./:@=-]*$|^\\^|\\\\[dswb]|^[-\\w]+ [-\\w]+=|^/|^\\{|^<|^#|^\\[|^\\$|^%[sdv]$")
)

// skip: directorios que no le hablan a la interfaz.
var skip = []string{"internal/installer", "internal/webui", "cmd/tango-installer", "cmd/tango-bundlesign", "cmd/tango-aptrepo", "cmd/connect-web"}

func main() {
	check := flag.Bool("check", false, "solo verificar que server.es.json esté al día")
	flag.Parse()
	root := "."
	set := map[string]bool{}
	for _, dir := range []string{"cmd", "internal", "pkg"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			for _, s := range skip {
				if strings.HasPrefix(p, s) {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			collect(p, set)
			return nil
		})
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	b, _ := json.MarshalIndent(out, "", "  ")
	dst := filepath.Join(root, "web/packages/client/src/i18n/server.es.json")
	if *check {
		cur, _ := os.ReadFile(dst)
		if !bytes.Equal(cur, append(b, '\n')) {
			fmt.Fprintln(os.Stderr, "los textos del NAS cambiaron: go run ./scripts/i18n-extract y traducí los nuevos en web/packages/client/src/i18n/server.en.ts")
			os.Exit(1)
		}
		return
	}
	_ = os.MkdirAll(filepath.Dir(dst), 0o755)
	if err := os.WriteFile(dst, append(b, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%d textos en %s\n", len(out), dst)
}

func collect(path string, set map[string]bool) {
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return
	}
	ast.Inspect(f, func(n ast.Node) bool {
		// Las claves de logs (slog: log.Warn("msg", ...)) no van a la interfaz.
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				switch sel.Sel.Name {
				case "Debug", "Info", "Warn", "Error", "Println", "Printf":
					if id, ok := sel.X.(*ast.Ident); ok && (id.Name == "log" || id.Name == "fmt" || strings.HasSuffix(strings.ToLower(id.Name), "log")) {
						return false
					}
					if s, ok := sel.X.(*ast.SelectorExpr); ok && (s.Sel.Name == "Log" || s.Sel.Name == "log") {
						return false
					}
				}
			}
		}
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		s = strings.TrimSpace(s)
		if len(s) < 3 || strings.Contains(s, "\n") || codey.MatchString(s) {
			return true
		}
		if !strings.Contains(s, " ") && !accent.MatchString(s) {
			return true
		}
		if accent.MatchString(s) || stopwords.MatchString(" "+s+" ") {
			set[s] = true
		}
		return true
	})
}

// Command tangoctl talks tango/1 to a tangod over WebRTC, like the desktop
// does (docs/03 §10): `tangoctl rpc storage.disks.list`.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/rtc"
)

func main() {
	url := flag.String("url", envOr("TANGO_URL", "wss://127.0.0.1/signal?v=1"), "signaling URL (wss://host/signal?v=1, or ws:// for tangod --dev)")
	insecure := flag.Bool("insecure", false, "accept a self-signed certificate (first boot)")
	user := flag.String("user", envOr("TANGO_USER", ""), "log in as this user before the call")
	password := flag.String("password", os.Getenv("TANGO_PASSWORD"), "password (or TANGO_PASSWORD)")
	totp := flag.String("totp", os.Getenv("TANGO_TOTP"), "código de verificación en dos pasos (o de recuperación), si el usuario lo tiene activo")
	timeout := flag.Duration("timeout", 20*time.Second, "overall timeout")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage:\n  tangoctl [flags] hello\n  tangoctl [flags] rpc <method> [json-params]\n  tangoctl [flags] metrics [n]\n  tangoctl [flags] events <topic|*> <segundos> [método json]\n  tangoctl [flags] upload <local> <carpeta-remota>\n  tangoctl [flags] download <ruta-remota> <local>\n  tangoctl [flags] term-exec <comando>   (solo admin)\n  tangoctl wsd-probe [segundos]           (WS-Discovery en la LAN, como Windows)\n\nflags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if flag.Arg(0) == "wsd-probe" { // no necesita sesión: lo que haría el Explorador de Windows
		wsdProbe(ctx, flag.Arg(1))
		return
	}

	c, err := rtc.Dial(ctx, *url, *insecure)
	if err != nil {
		fail(err)
	}
	defer c.Close()
	if *user != "" {
		if err := c.LoginTOTP(ctx, *user, *password, *totp); err != nil {
			fail(err)
		}
		// Cerrar la sesión al salir, también si algo falla (fail sale con os.Exit y no
		// corre los defer): si no, cada tangoctl deja un token vivo 12 h.
		cleanup = func() {
			lctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_, _ = c.Call(lctx, "auth.logout", nil)
			cancel()
		}
		defer runCleanup()
	}

	switch flag.Arg(0) {
	case "hello":
		printJSON(c.Hello)
	case "rpc":
		if flag.NArg() < 2 {
			flag.Usage()
			os.Exit(2)
		}
		var params any
		if flag.NArg() > 2 {
			params = json.RawMessage(flag.Arg(2))
		}
		res, err := c.Call(ctx, flag.Arg(1), params)
		if err != nil {
			fail(err)
		}
		var buf bytes.Buffer
		if json.Indent(&buf, res, "", "  ") == nil {
			fmt.Println(buf.String())
		} else {
			fmt.Println(string(res))
		}
	case "events":
		// events <topic|*> <segundos> [método json]: opcionalmente hace una llamada (p. ej.
		// files.list para que tangod vigile esa carpeta) y muestra los eventos que lleguen.
		if flag.NArg() < 3 {
			flag.Usage()
			os.Exit(2)
		}
		var secs int
		fmt.Sscan(flag.Arg(2), &secs)
		if flag.NArg() > 3 {
			var params any
			if flag.NArg() > 4 {
				params = json.RawMessage(flag.Arg(4))
			}
			res, err := c.Call(ctx, flag.Arg(3), params)
			if err != nil {
				fail(err)
			}
			fmt.Println(string(res))
		}
		until := time.After(time.Duration(secs) * time.Second)
		for done := false; !done; {
			select {
			case e := <-c.Events:
				var ev struct {
					Topic string `json:"topic"`
				}
				if json.Unmarshal(e, &ev) == nil && (flag.Arg(1) == "*" || ev.Topic == flag.Arg(1)) {
					fmt.Println(string(e))
				}
			case <-until:
				done = true
			case <-ctx.Done():
				fail(ctx.Err())
			}
		}
	case "metrics":
		n := 4
		if flag.NArg() > 1 {
			fmt.Sscan(flag.Arg(1), &n)
		}
		for i := 0; i < n; i++ {
			select {
			case m := <-c.Metrics:
				fmt.Println(string(m))
			case <-ctx.Done():
				fail(ctx.Err())
			}
		}
	case "upload", "download":
		if flag.NArg() < 3 {
			flag.Usage()
			os.Exit(2)
		}
		var st rtc.TransferStats
		if flag.Arg(0) == "upload" {
			st, err = c.Upload(ctx, flag.Arg(1), flag.Arg(2), false)
		} else {
			st, err = c.Download(ctx, flag.Arg(1), flag.Arg(2))
		}
		if err != nil {
			fail(err)
		}
		fmt.Printf("%s %d bytes en %.2f s · %.1f MB/s · sha256 %s\n", flag.Arg(0), st.Bytes, st.Elapsed.Seconds(), st.MBps(), st.SHA256)
	case "term-exec":
		if flag.NArg() < 2 {
			flag.Usage()
			os.Exit(2)
		}
		out, err := c.TermExec(ctx, flag.Arg(1))
		fmt.Print(out)
		if err != nil {
			fail(err)
		}
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

// cleanup cierra la sesión (si hubo login); corre una sola vez, al terminar o al fallar.
var cleanup func()

func runCleanup() {
	if f := cleanup; f != nil {
		cleanup = nil
		f()
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "tangoctl:", err)
	runCleanup()
	os.Exit(1)
}

func envOr(k, d string) string {
	if v, ok := os.LookupEnv(k); ok {
		return v
	}
	return d
}

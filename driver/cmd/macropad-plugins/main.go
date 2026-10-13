// Command macropad-plugins is the plugin host: it runs every Go plugin
// named in one config file, in one process, over one connection to
// macropadd. See task 0055 and driver/README.md's "Plugin host" section.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"

	"github.com/kgheacock/macro-pad/driver/api"
	"github.com/kgheacock/macro-pad/driver/host"
	"github.com/kgheacock/macro-pad/driver/plugin"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// dial opens one connection to macropadd. A test replaces it.
var dial = func(ctx context.Context, addr string) (host.Conn, error) {
	return api.Dial(addr)
}

// defaultConfigPath is ~/.config/macropad/plugins.json on every OS, so
// the path in the README holds everywhere.
func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "plugins.json"
	}
	return filepath.Join(home, ".config", "macropad", "plugins.json")
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("macropad-plugins", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", defaultConfigPath(), "plugin host config file")
	addr := fs.String("addr", "", fmt.Sprintf("host:port of macropadd's plugin server; overrides the config's addr (default 127.0.0.1:%d)", plugin.DefaultPort))
	list := fs.Bool("list", false, "print every plugin this binary carries and exit")
	check := fs.Bool("check", false, "validate the config file and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *list {
		factories := append([]host.Factory(nil), registry...)
		sort.Slice(factories, func(i, j int) bool { return factories[i].Name < factories[j].Name })
		for _, f := range factories {
			fmt.Fprintf(stdout, "%-16s %s\n", f.Name, f.Description)
		}
		return 0
	}

	cfg, err := host.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "macropad-plugins: %v\n", err)
		return 2
	}
	if err := cfg.Validate(registry); err != nil {
		fmt.Fprintf(stderr, "macropad-plugins: %s:\n%v\n", *configPath, err)
		return 2
	}
	if *check {
		fmt.Fprintf(stdout, "%s: ok, %d plugin instances on pages %v\n", *configPath, len(cfg.Plugins), cfg.Pages())
		return 0
	}

	target := cfg.Addr
	if *addr != "" {
		target = *addr
	}
	if target == "" {
		target = fmt.Sprintf("127.0.0.1:%d", plugin.DefaultPort)
	}

	logger := log.New(stderr, "", log.LstdFlags)
	r, err := host.New(cfg, registry, host.Options{
		Dial: func(ctx context.Context) (host.Conn, error) { return dial(ctx, target) },
		Logf: logger.Printf,
	})
	if err != nil {
		fmt.Fprintf(stderr, "macropad-plugins: %v\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "macropad-plugins: %d plugin instances, connecting to %s\n", len(cfg.Plugins), target)
	if err := r.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "macropad-plugins: %v\n", err)
		return 1
	}
	return 0
}

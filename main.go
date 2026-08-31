// Command trellis-proxy-sync synchronizes proxy configuration from Trellis.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"text/template"
	"time"

	"github.com/clofour/trellis/internal/client"
)

type upstream struct {
	Address string
	Port    int
	Weight  int
}

type templateData struct {
	Upstreams []upstream
}

func main() {
	var (
		labelFilter  string
		templateFile string
		outputFile   string
		reloadCmd    string
		interval     time.Duration
	)

	flag.StringVar(&labelFilter, "label", "", "label filter for allocations (e.g. route:my-app)")
	flag.StringVar(&templateFile, "template", "", "path to proxy config template")
	flag.StringVar(&outputFile, "output", "", "path to write rendered config")
	flag.StringVar(&reloadCmd, "reload-cmd", "", "command to run after config update")
	flag.DurationVar(&interval, "interval", 5*time.Second, "poll interval")
	flag.Parse()

	if labelFilter == "" || templateFile == "" || outputFile == "" {
		fmt.Fprintln(os.Stderr, "usage: trellis-proxy-sync -label <key:value> -template <path> -output <path> [-reload-cmd <cmd>] [-interval <duration>]")
		os.Exit(1)
	}

	token := os.Getenv("TRELLIS_TOKEN")
	addr := os.Getenv("TRELLIS_ADDR")
	if token == "" || addr == "" {
		fmt.Fprintln(os.Stderr, "TRELLIS_TOKEN and TRELLIS_ADDR must be set (use api_access: true on the task group)")
		os.Exit(1)
	}

	log := slog.Default()

	tmplContent, err := os.ReadFile(templateFile)
	if err != nil {
		log.Error("read template", "error", err)
		os.Exit(1)
	}
	tmpl, err := template.New("config").Parse(string(tmplContent))
	if err != nil {
		log.Error("parse template", "error", err)
		os.Exit(1)
	}

	c := client.NewServerClient(token, addr, nil)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	var lastConfig string
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	sync := func() {
		allocs, err := c.ListAllocations(ctx, labelFilter)
		if err != nil {
			log.Error("poll allocations", "error", err)
			return
		}

		var upstreams []upstream
		for _, alloc := range *allocs {
			if alloc.Status != "healthy" || alloc.Address == "" || len(alloc.Ports) == 0 {
				continue
			}
			port := alloc.Ports[0].HostPort
			if port <= 0 {
				continue
			}
			weight := 1
			if w, ok := alloc.Labels["trellis/weight"]; ok {
				if parsed, err := strconv.Atoi(w); err == nil && parsed > 0 {
					weight = parsed
				}
			}
			upstreams = append(upstreams, upstream{
				Address: alloc.Address,
				Port:    port,
				Weight:  weight,
			})
		}

		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, templateData{Upstreams: upstreams}); err != nil {
			log.Error("render template", "error", err)
			return
		}
		rendered := buf.String()

		if rendered == lastConfig {
			return
		}

		if err := os.WriteFile(outputFile, []byte(rendered), 0644); err != nil {
			log.Error("write config", "error", err)
			return
		}
		log.Info("config updated", "upstreams", len(upstreams), "output", outputFile)

		if reloadCmd != "" {
			if out, err := exec.CommandContext(ctx, "sh", "-c", reloadCmd).CombinedOutput(); err != nil {
				log.Error("reload command failed", "error", err, "output", string(out))
				return
			}
			log.Info("proxy reloaded")
		}
		lastConfig = rendered
	}

	sync()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sync()
		}
	}
}

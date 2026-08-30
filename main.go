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
		route        string
		templateFile string
		outputFile   string
		reloadCmd    string
		interval     time.Duration
	)

	flag.StringVar(&route, "route", "", "route label value to match")
	flag.StringVar(&templateFile, "template", "", "path to proxy config template")
	flag.StringVar(&outputFile, "output", "", "path to write rendered config")
	flag.StringVar(&reloadCmd, "reload-cmd", "", "command to run after config update")
	flag.DurationVar(&interval, "interval", 5*time.Second, "poll interval")
	flag.Parse()

	if route == "" || templateFile == "" || outputFile == "" {
		fmt.Fprintln(os.Stderr, "usage: trellis-proxy-sync -route <value> -template <path> -output <path> [-reload-cmd <cmd>] [-interval <duration>]")
		os.Exit(1)
	}

	token := os.Getenv("TRELLIS_TOKEN")
	addr := os.Getenv("TRELLIS_ADDR")
	if token == "" || addr == "" {
		fmt.Fprintln(os.Stderr, "TRELLIS_TOKEN and TRELLIS_ADDR must be set (use api_access: true)")
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
		services, err := c.ListDiscovery(ctx)
		if err != nil {
			log.Error("poll discovery", "error", err)
			return
		}

		var upstreams []upstream
		for _, svc := range *services {
			if svc.Labels["route"] != route || svc.Status != "healthy" {
				continue
			}
			weight := 1
			if w, ok := svc.Labels["weight"]; ok {
				if parsed, err := strconv.Atoi(w); err == nil && parsed > 0 {
					weight = parsed
				}
			}
			port := 0
			if len(svc.Ports) > 0 {
				port = svc.Ports[0].ContainerPort
			}
			upstreams = append(upstreams, upstream{
				Address: svc.Address,
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
		lastConfig = rendered

		if err := os.WriteFile(outputFile, []byte(rendered), 0644); err != nil {
			log.Error("write config", "error", err)
			return
		}
		log.Info("config updated", "upstreams", len(upstreams), "output", outputFile)

		if reloadCmd != "" {
			if out, err := exec.CommandContext(ctx, "sh", "-c", reloadCmd).CombinedOutput(); err != nil {
				log.Error("reload command failed", "error", err, "output", string(out))
			} else {
				log.Info("proxy reloaded")
			}
		}
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


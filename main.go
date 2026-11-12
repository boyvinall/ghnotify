package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/boyvinall/ghnotify/internal/auth"
	"github.com/boyvinall/ghnotify/internal/config"
	"github.com/boyvinall/ghnotify/internal/github"
	"github.com/boyvinall/ghnotify/internal/notify"
	"github.com/boyvinall/ghnotify/internal/poller"
	"github.com/boyvinall/ghnotify/internal/tray"
	"github.com/boyvinall/ghnotify/internal/updater"
)

var version = "dev"

func main() {
	app := &cli.Command{
		Name:    "ghnotify",
		Usage:   "GitHub PR monitor in your menubar",
		Version: version,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "level",
				Aliases: []string{"l"},
				Value:   "info",
				Usage:   "log level (debug, info, warn, error)",
			},
		},
		Action: run,
		Commands: []*cli.Command{
			{
				Name:      "pr-detail",
				Usage:     "fetch and print PR detail as JSON",
				ArgsUsage: "<pr-url>",
				Action:    runPRDetail,
			},
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			var level slog.Level
			if err := level.UnmarshalText([]byte(cmd.String("level"))); err != nil {
				return ctx, fmt.Errorf("invalid log level %q: %w", cmd.String("level"), err)
			}
			slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
			return ctx, nil
		},
	}
	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// parsePRURL parses a GitHub PR URL into its components.
// Expected path: /<owner>/<repo>/pull/<number>
func parsePRURL(raw string) (host, owner, repo string, number int, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", "", 0, fmt.Errorf("invalid URL: %w", err)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" {
		return "", "", "", 0, fmt.Errorf("URL path must be /<owner>/<repo>/pull/<number>")
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil {
		return "", "", "", 0, fmt.Errorf("invalid PR number %q", parts[3])
	}
	return u.Hostname(), parts[0], parts[1], n, nil
}

func runPRDetail(ctx context.Context, cmd *cli.Command) error {
	if cmd.NArg() != 1 {
		return fmt.Errorf("expected a PR URL argument")
	}
	host, owner, repo, number, err := parsePRURL(cmd.Args().Get(0))
	if err != nil {
		return err
	}
	mgr := auth.NewManager()
	token, err := mgr.GetToken(host)
	if err != nil {
		return fmt.Errorf("get token for %s: %w", host, err)
	}
	client := github.NewClient(host, token, nil)
	pr, err := client.FetchPRDetail(ctx, owner, repo, number)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(pr)
}

func run(ctx context.Context, cmd *cli.Command) error {
	// Register the notification click handler and request permission
	// before the first notification fires.
	notify.Init()

	// A bad config file no longer stops the app: Load falls back to the
	// defaults and the tray reports the error until the file is fixed.
	cfg, cfgRaw, cfgErr := config.Load()
	cfgStore := config.NewStore(cfg)

	snoozePath, err := config.SnoozeFilePath()
	if err != nil {
		return fmt.Errorf("snooze path: %w", err)
	}

	ackPath, err := config.AcknowledgeFilePath()
	if err != nil {
		return fmt.Errorf("acknowledge path: %w", err)
	}

	mgr := auth.NewManager()
	snooze := poller.NewSnoozeStore(snoozePath)
	ack := poller.NewAcknowledgeStore(ackPath)
	poll := poller.NewManager(mgr, cfgStore)
	poll.Start()
	defer poll.Stop()

	// Run the tray. This will block until the user quits.
	slog.Debug("starting tray")
	tray.Run(tray.Options{
		Config:      cfgStore,
		ConfigErr:   cfgErr,
		ConfigRaw:   cfgRaw,
		Auth:        mgr,
		Poll:        poll,
		Snooze:      snooze,
		Acknowledge: ack,
		Notif:       notify.NewNotifier(cfgStore, snooze),
		Updater:     updater.NewUpdater(version),
	})
	return nil
}

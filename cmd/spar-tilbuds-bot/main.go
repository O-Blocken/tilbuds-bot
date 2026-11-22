package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	appconfig "spar-tilbuds-bot/internal/config"
	"spar-tilbuds-bot/internal/discordbot"
	"spar-tilbuds-bot/internal/service"
	"spar-tilbuds-bot/internal/store"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	configPath := appconfig.GetEnv("CONFIG_PATH")
	if configPath == "" {
		configPath = appconfig.Path
	}

	// Container healthcheck: report liveness and exit without starting the bot.
	if len(os.Args) > 1 && os.Args[1] == "--healthcheck" {
		os.Exit(runHealthcheck(configPath, time.Now(), os.Stdout))
	}

	token := appconfig.GetEnv("DISCORD_BOT_TOKEN")
	if token == "" {
		logger.Error("missing DISCORD_BOT_TOKEN in environment or .env file")
		os.Exit(1)
	}

	configPath = resolveConfigPath(configPath, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	startHeartbeat(ctx, configPath, logger)

	configStore := appconfig.NewConfigStore(configPath)
	dealService := service.NewDealService(nil, nil, logger)

	// SQLite state lives next to the TOML config so both persist together.
	var db *store.Store
	if opened, err := store.Open(storePathFor(configPath)); err != nil {
		logger.Warn("sqlite unavailable; running without price history and /want management",
			slog.String("error", err.Error()))
	} else {
		defer opened.Close()
		if imported := importTOMLWants(configStore, opened, logger); imported > 0 {
			logger.Info("imported TOML wants into SQLite", slog.Int("count", imported))
		}
		db = opened
		dealService.SetStore(db)
	}

	bot := discordbot.NewDiscordBot(
		dealService,
		configStore,
		logger,
	)
	bot.SetStore(db)
	if err := bot.Start(ctx, token); err != nil {
		logger.Error("bot stopped", slog.String("error", err.Error()))
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// storePathFor places bot.db next to the TOML config file.
func storePathFor(configPath string) string {
	dir := filepath.Dir(configPath)
	if dir == "" || dir == "." {
		return "bot.db"
	}
	return filepath.Join(dir, "bot.db")
}

// importTOMLWants seeds SQLite from TOML/keywords on first run. Existing
// database rows always win; nothing is overwritten.
func importTOMLWants(configStore *appconfig.ConfigStore, db *store.Store, logger *slog.Logger) int {
	config, err := configStore.Load()
	if err != nil {
		logger.Warn("cannot load config for wants import", slog.String("error", err.Error()))
		return 0
	}
	empty, err := db.WantsEmpty()
	if err != nil || !empty {
		return 0
	}
	imported, err := db.ImportMatchWants(config.EffectiveWants())
	if err != nil {
		logger.Warn("wants import failed", slog.String("error", err.Error()))
		return 0
	}
	return imported
}

// resolveConfigPath returns the first candidate config file path whose
// directory allows create, write, and replace of a temporary file. If the
// primary CONFIG_PATH is not writable the bot keeps the state it can still
// reach and falls back to a writable path (work directory, then /tmp inside a
// container) instead of exiting at startup. A read-only bind mount must not
// crash-loop the bot; state stored on the fallback path does not survive
// container updates, so the log tells the operator how to restore the
// primary path.
func resolveConfigPath(primary string, logger *slog.Logger) string {
	diagnose := ""
	if err := probeConfigDir(filepath.Dir(primary)); err != nil {
		diagnose = configDirDiagnose(err)
	} else {
		logger.Info("config store is writable", slog.String("path", primary))
		return primary
	}

	for _, candidate := range []string{
		filepath.Join("work", "config.toml"),
		filepath.Join(os.TempDir(), "spar-tilbuds-bot-config.toml"),
	} {
		if probeConfigDir(filepath.Dir(candidate)) == nil {
			logger.Warn("config store on primary path is not writable; falling back so the bot can keep running",
				slog.String("primary", primary),
				slog.String("fallback", candidate),
				slog.String("hint", diagnose))
			migrateConfigState(primary, candidate, logger)
			logger.Info("config store is writable", slog.String("path", candidate))
			return candidate
		}
	}
	logger.Error("no writable config store found; state changes will not persist",
		slog.String("primary", primary),
		slog.String("hint", diagnose),
	)
	return primary
}

// probeConfigDir verifies that a config store directory can create, write,
// and replace a file. Empty and "." mean the current directory.
func probeConfigDir(dir string) error {
	if dir == "" || dir == "." {
		dir = ""
	}
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("kunne ikke oprette mappe %s: %w", dir, err)
		}
	}

	probe, err := os.CreateTemp(dir, ".config-probe-")
	if err != nil {
		return fmt.Errorf("kunne ikke oprette config-fil i %s: %w", orCurrent(dir), err)
	}
	probeName := probe.Name()
	defer os.Remove(probeName)

	if _, err := probe.WriteString("ok\n"); err != nil {
		probe.Close()
		return fmt.Errorf("kunne ikke skrive til %s: %w", probeName, err)
	}
	if err := probe.Close(); err != nil {
		return fmt.Errorf("kunne ikke lukke %s: %w", probeName, err)
	}
	if err := os.WriteFile(probeName, []byte("replace\n"), 0o644); err != nil {
		return fmt.Errorf("kunne ikke erstatne config-fil %s: %w", probeName, err)
	}
	return nil
}

func orCurrent(dir string) string {
	if dir == "" {
		return "."
	}
	return dir
}

// configDirDiagnose turns a probe failure into operator-facing hints for the
// most common Linux container causes: a host directory the container user
// cannot write to (wrong owner, rootless Docker or userns-remap mapping) and
// a security module blocking access to the bind mount (SELinux/AppArmor).
func configDirDiagnose(probeErr error) string {
	if probeErr == nil {
		return ""
	}
	switch {
	case errors.Is(probeErr, syscall.EPERM):
		return "skrivning blev blokeret, f.eks. af en sikkerheds-module (SELinux/AppArmor) eller af rootless Docker/userns-remap, der mapper container-root til en bruger uden adgang til mappen"
	case errors.Is(probeErr, syscall.EACCES):
		return "container-brugeren mangler skriveadgang til mappen på hosten; ved rootless Docker eller userns-remap skal mappen kunne skrives af den bruger, der kører Docker, ellers af root"
	default:
		return "mappen på hosten skal kunne skrives af container-brugeren (root ved almindeligt Docker, den tilsvarende bruger ved rootless Docker)"
	}
}

// migrateConfigState copies previously stored state from the primary path
// into the fallback file so channel and posted-message tracking survive the
// switch instead of silently resetting to defaults.
func migrateConfigState(from, to string, logger *slog.Logger) {
	if from == to {
		return
	}
	existing, err := os.ReadFile(from)
	if err != nil {
		return
	}
	if _, err := os.Stat(to); err == nil {
		logger.Warn("fallback config already exists; keeping fallback state and ignoring primary state",
			slog.String("primary", from), slog.String("fallback", to))
		return
	}
	if err := os.WriteFile(to, existing, 0o644); err != nil {
		logger.Error("kunne ikke kopiere config-state til fallback",
			slog.String("from", from), slog.String("to", to), slog.String("error", err.Error()))
		return
	}
	logger.Info("migrated config state to fallback path",
		slog.String("from", from), slog.String("to", to))
}

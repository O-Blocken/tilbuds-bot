package discordbot

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

	appconfig "spar-tilbuds-bot/internal/config"
	"spar-tilbuds-bot/internal/match"
	"spar-tilbuds-bot/internal/search"
	"spar-tilbuds-bot/internal/service"
	"spar-tilbuds-bot/internal/store"

	"github.com/bwmarrin/discordgo"
)

// errPublishBusy reports that another publish run holds the lock.
var errPublishBusy = errors.New("en anden tilbudskørsel er allerede i gang, prøv igen om lidt")

// botSession is the slice of the Discord API the bot stores and uses outside
// interaction handlers: posting, deleting tracked messages and alert pings.
// It is an interface so the posting state machine can be tested without a live
// gateway connection.
//
// *discordgo.Session satisfies it.
type botSession interface {
	Channel(id string, opts ...discordgo.RequestOption) (*discordgo.Channel, error)
	ChannelMessageSend(id, content string, opts ...discordgo.RequestOption) (*discordgo.Message, error)
	ChannelMessageSendComplex(id string, data *discordgo.MessageSend, opts ...discordgo.RequestOption) (*discordgo.Message, error)
	ChannelMessageDelete(channelID, messageID string, opts ...discordgo.RequestOption) error
}

type DiscordBot struct {
	dealService *service.DealService
	configStore *appconfig.ConfigStore
	logger      *slog.Logger
	db          *store.Store
	// registry is the effective concept registry (concepts.toml base plus
	// SQLite overrides) installed into the matchers by rebuildConcepts.
	registry *search.Registry
	// publishMu serializes publishing so the cron loop and a manual
	// /send_deals cannot post the same publication twice.
	publishMu sync.Mutex
	// guildID limits the bot to one guild when DISCORD_GUILD_ID is set.
	// Empty means "accept any guild that invites the bot".
	guildID string

	session     botSession
	selfID      string
	ctx         context.Context
	autoStarted atomic.Bool
}

func NewDiscordBot(dealService *service.DealService, configStore *appconfig.ConfigStore, logger *slog.Logger) *DiscordBot {
	if logger == nil {
		logger = slog.Default()
	}
	if dealService == nil {
		dealService = service.NewDealService(nil, nil, logger)
	}
	if configStore == nil {
		configStore = appconfig.NewConfigStore(appconfig.Path)
	}

	return &DiscordBot{
		dealService: dealService,
		configStore: configStore,
		logger:      logger,
		guildID:     strings.TrimSpace(appconfig.GetEnv("DISCORD_GUILD_ID")),
	}
}

// authorized reports whether an interaction may run a state-changing command.
//
// Discord's DefaultMemberPermissions only hides the commands from members
// without the permission; a guild admin can re-grant them in Server Settings →
// Integrations. The bot therefore re-checks the invoking member's effective
// guild permissions on every interaction, and ignores guilds other than the
// configured one.
func (b *DiscordBot) authorized(event *discordgo.InteractionCreate) bool {
	if event == nil || event.Interaction == nil {
		return false
	}
	// Guild-only: DMs and group DMs have no member permissions to check.
	if event.GuildID == "" || event.Member == nil {
		return false
	}
	if b.guildID != "" && event.GuildID != b.guildID {
		return false
	}
	return event.Member.Permissions&discordgo.PermissionAdministrator != 0
}

// rejectUnauthorized answers and logs a refused interaction.
func (b *DiscordBot) rejectUnauthorized(session *discordgo.Session, event *discordgo.InteractionCreate) {
	userID, guildID := "", event.GuildID
	if event.User != nil {
		userID = event.User.ID
	}
	b.logger.Warn("refused slash command from unauthorized interaction",
		slog.String("user_id", userID),
		slog.String("guild_id", guildID),
		slog.String("command", event.ApplicationCommandData().Name),
	)
	b.replyEphemeral(session, event.Interaction, "Kun serveradministratorer kan bruge denne kommando.")
}

// SetStore attaches SQLite state for /want management and /stats.
// A nil store keeps TOML-only behavior.
func (b *DiscordBot) SetStore(db *store.Store) {
	b.db = db
}

// loadWants returns the matching wants: SQLite when it holds any (managed
// via /want), otherwise the TOML config (EffectiveWants).
func (b *DiscordBot) loadWants(config appconfig.BotConfig) ([]match.Want, error) {
	if b.db != nil {
		rows, err := b.db.ListWants()
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			return store.ToMatchWants(rows), nil
		}
	}
	return config.EffectiveWants(), nil
}

func (b *DiscordBot) Start(ctx context.Context, token string) error {
	defer func() {
		if err := b.dealService.Close(); err != nil {
			b.logger.Warn("failed to close tjekapi PDF processor", slog.String("error", err.Error()))
		}
	}()

	session, err := discordgo.New(normalizeDiscordToken(token))
	if err != nil {
		return err
	}

	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages
	session.AddHandler(b.onReady)
	session.AddHandler(b.onInteractionCreate)

	b.session = session
	b.ctx = ctx
	defer session.Close()
	if err := session.Open(); err != nil {
		return err
	}

	<-ctx.Done()
	return nil
}

func (b *DiscordBot) onReady(session *discordgo.Session, event *discordgo.Ready) {
	if event.User != nil {
		b.selfID = event.User.ID
		b.logger.Info("logged in", slog.String("name", event.User.Username), slog.String("id", event.User.ID))
	}

	if err := b.registerCommands(session, event); err != nil {
		b.logger.Error("failed to sync slash commands", slog.String("error", err.Error()))
	} else {
		b.logger.Info("slash commands synced")
	}

	// Install the concept registry (concepts.toml + database overrides)
	// into the matchers before any matching runs.
	if source, err := b.rebuildConcepts(); err != nil {
		b.logger.Warn("failed to load concept registry; matching stays literal",
			slog.String("error", err.Error()))
	} else {
		b.logger.Info("concept registry loaded",
			slog.String("source", sourceLabel(source)),
			slog.Int("custom", source.Custom), slog.Int("removed", source.Removed))
	}

	b.startAutoPosting()
}

func (b *DiscordBot) onInteractionCreate(session *discordgo.Session, event *discordgo.InteractionCreate) {
	if event.Type != discordgo.InteractionApplicationCommand {
		return
	}

	data := event.ApplicationCommandData()
	if !b.authorized(event) {
		b.rejectUnauthorized(session, event)
		return
	}
	switch data.Name {
	case "set_deals_channel":
		b.handleSetDealsChannel(session, event, data)
	case "send_deals":
		b.handleSendDeals(session, event, data)
	case "clear_deals":
		b.handleClearDeals(session, event, data)
	case "preview_deals":
		b.handlePreviewDeals(session, event, data)
	case "search":
		b.handleSearch(session, event, data)
	case "want":
		b.handleWant(session, event, data)
	case "concepts":
		b.handleConcepts(session, event, data)
	case "prisalarm":
		b.handlePrisalarm(session, event, data)
	case "top":
		b.handleTop(session, event, data)
	case "stats":
		b.handleStats(session, event, data)
	}
}

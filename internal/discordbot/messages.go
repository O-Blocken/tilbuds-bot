package discordbot

import (
	"errors"
	"net/http"

	appconfig "spar-tilbuds-bot/internal/config"

	"github.com/bwmarrin/discordgo"
)

func trackedMessage(message *discordgo.Message) appconfig.PostedMessage {
	if message == nil {
		return appconfig.PostedMessage{}
	}
	return appconfig.PostedMessage{ChannelID: message.ChannelID, MessageID: message.ID}
}

// messageDeleteFailure records one tracked message that survived cleanup
// along with why its deletion failed.
type messageDeleteFailure struct {
	message appconfig.PostedMessage
	err     error
}

// clearTrackedMessages deletes the given tracked messages on a best-effort
// basis and removes exactly those messages from tracking — including ones
// that no longer exist or cannot be deleted. A poisoned message ID (archived
// thread, lost permissions, …) must never block future runs, so every
// processed ID is dropped from the stored list; failures are returned for
// logging only.
//
// Only the input messages are removed from tracking; other entries (e.g.
// another retailer's feed) are left untouched.
//
// The returned error is non-nil only if the tracking list itself could not
// be persisted. In that case the deletions already attempted still stand:
// messages that are actually gone are picked up as 404s on the next run.
func (b *DiscordBot) clearTrackedMessages(messages []appconfig.PostedMessage) ([]messageDeleteFailure, error) {
	failures := b.deleteTrackedMessages(messages)
	if _, updateErr := b.configStore.Update(func(config *appconfig.BotConfig) {
		config.PostedMessages = removeTrackedMessages(config.PostedMessages, messages)
	}); updateErr != nil {
		return failures, updateErr
	}
	return failures, nil
}

// removeTrackedMessages returns stored minus every entry matching one of
// remove by exact struct equality. Entries for other feeds are preserved,
// as are stored entries the caller did not ask about.
func removeTrackedMessages(stored, remove []appconfig.PostedMessage) []appconfig.PostedMessage {
	drop := make(map[appconfig.PostedMessage]struct{}, len(remove))
	for _, message := range remove {
		drop[message] = struct{}{}
	}
	kept := make([]appconfig.PostedMessage, 0, len(stored))
	for _, message := range stored {
		if _, ok := drop[message]; !ok {
			kept = append(kept, message)
		}
	}
	return kept
}

// deleteTrackedMessages attempts to delete each tracked message, skipping
// malformed entries. Messages that are already gone (404) count as cleaned.
// Anything else that fails is reported with its underlying error so logs
// show exactly which message survived and why.
func (b *DiscordBot) deleteTrackedMessages(messages []appconfig.PostedMessage) []messageDeleteFailure {
	var failures []messageDeleteFailure
	for _, message := range messages {
		if message.ChannelID == "" || message.MessageID == "" {
			continue
		}
		if err := b.session.ChannelMessageDelete(message.ChannelID, message.MessageID); err != nil && !isMissingDiscordMessage(err) {
			failures = append(failures, messageDeleteFailure{message: message, err: err})
		}
	}
	return failures
}

func isMissingDiscordMessage(err error) bool {
	var restErr *discordgo.RESTError
	if !errors.As(err, &restErr) || restErr.Response == nil {
		return false
	}
	return restErr.Response.StatusCode == http.StatusNotFound
}

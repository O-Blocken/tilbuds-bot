package discordbot

import (
	"errors"
	"log/slog"
	"os"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"spar-tilbuds-bot/internal/search"

	"github.com/bwmarrin/discordgo"
)

// userFacingError marks an error whose message is written for the operator and
// may be shown in Discord verbatim.
type userFacingError struct{ message string }

func (e userFacingError) Error() string { return e.message }

// userFacing wraps a message that is safe to show to a Discord user.
func userFacing(err error) error {
	if err == nil {
		return nil
	}
	return userFacingError{message: err.Error()}
}

// userError turns an error into text that may be shown in Discord.
//
// Errors meant for users (query parse errors, validation failures, a publish
// already in progress) pass through. Anything else is logged in full and
// reported generically, so upstream URLs, SQL errors and file paths stay in the
// log instead of a public channel.
func (b *DiscordBot) userError(context string, err error) string {
	var parseErr *search.ParseError
	var facing userFacingError
	switch {
	case err == nil:
		return context
	case errors.Is(err, errPublishBusy):
		return errPublishBusy.Error()
	case errors.As(err, &parseErr):
		return parseErr.UserMessage()
	case errors.As(err, &facing):
		return facing.Error()
	}
	b.logger.Error(context, slog.String("error", err.Error()))
	return context + " – se bottens log."
}

// Discord rejects message content longer than 2000 characters, measured in
// UTF-16 code units. Command output (concept lists, preview summaries, alert
// reports) grows with the data, so every reply is clamped at this boundary
// instead of failing with "Invalid Form Body" and leaving the user with no
// answer at all.
const (
	discordContentLimit = 2000
	truncationSuffix    = "\n… (afkortet)"
)

// truncateForDiscord clamps content to Discord's message limit.
func truncateForDiscord(content string) string {
	return truncateForDiscordLimit(content, discordContentLimit)
}

// truncateForDiscordLimit clamps content to limit UTF-16 code units, cutting on
// a line boundary when possible and marking the truncation.
func truncateForDiscordLimit(content string, limit int) string {
	if utf16Length(content) <= limit {
		return content
	}
	budget := limit - utf16Length(truncationSuffix)
	if budget <= 0 {
		return truncationSuffix
	}

	cut := 0
	used := 0
	for index, r := range content {
		width := utf16.RuneLen(r)
		if width <= 0 {
			width = 1
		}
		if used+width > budget {
			break
		}
		used += width
		cut = index + utf8.RuneLen(r)
	}

	head := content[:cut]
	// Prefer a whole number of lines so a truncated entry is not half-shown.
	if newline := strings.LastIndex(head, "\n"); newline > 0 {
		head = head[:newline]
	}
	return head + truncationSuffix
}

// utf16Length counts UTF-16 code units, which is how Discord measures content.
func utf16Length(value string) int {
	total := 0
	for _, r := range value {
		width := utf16.RuneLen(r)
		if width <= 0 {
			width = 1
		}
		total += width
	}
	return total
}

func textChannelOption(session *discordgo.Session, data discordgo.ApplicationCommandInteractionData, name string) *discordgo.Channel {
	option := data.GetOption(name)
	if option == nil {
		return nil
	}

	channel := option.ChannelValue(session)
	if !isTextChannel(channel) {
		return nil
	}
	return channel
}

func isTextChannel(channel *discordgo.Channel) bool {
	return channel != nil && channel.Type == discordgo.ChannelTypeGuildText
}

func (b *DiscordBot) replyEphemeral(session *discordgo.Session, interaction *discordgo.Interaction, content string) {
	if err := session.InteractionRespond(interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: truncateForDiscord(content),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}); err != nil {
		b.logger.Error("failed to reply to interaction", slog.String("error", err.Error()))
	}
}

func (b *DiscordBot) deferEphemeral(session *discordgo.Session, interaction *discordgo.Interaction) error {
	return session.InteractionRespond(interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})
}

func (b *DiscordBot) followupEphemeral(session *discordgo.Session, interaction *discordgo.Interaction, content string) {
	if _, err := session.FollowupMessageCreate(interaction, true, &discordgo.WebhookParams{
		Content: truncateForDiscord(content),
		Flags:   discordgo.MessageFlagsEphemeral,
	}); err != nil {
		b.logger.Error("failed to send followup", slog.String("error", err.Error()))
	}
}

func normalizeDiscordToken(token string) string {
	token = strings.TrimSpace(token)
	if strings.HasPrefix(token, "Bot ") || strings.HasPrefix(token, "Bearer ") {
		return token
	}
	return "Bot " + token
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

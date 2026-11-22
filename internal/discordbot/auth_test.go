package discordbot

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

// adminInteraction builds a guild interaction with the given member
// permissions. A nil permissions slice simulates a DM (no Member).
func adminInteraction(guildID string, perms int64, withMember bool) *discordgo.InteractionCreate {
	interaction := &discordgo.Interaction{
		Type:    discordgo.InteractionApplicationCommand,
		GuildID: guildID,
		Data: discordgo.ApplicationCommandInteractionData{
			Name: "send_deals",
		},
	}
	event := &discordgo.InteractionCreate{Interaction: interaction}
	if withMember {
		event.Member = &discordgo.Member{Permissions: perms}
	}
	return event
}

func TestAuthorizedAcceptsAdministrator(t *testing.T) {
	bot := &DiscordBot{guildID: "guild-1"}
	event := adminInteraction("guild-1", discordgo.PermissionAdministrator, true)
	if !bot.authorized(event) {
		t.Fatal("administrator in the configured guild must be authorized")
	}
}

func TestAuthorizedRejectsNonAdministrator(t *testing.T) {
	bot := &DiscordBot{}
	// Manage Server, Send Messages, Manage Messages and Kick Members are the
	// permissions an over-eager guild admin might re-grant; none of them is
	// enough on its own.
	perms := int64(discordgo.PermissionManageGuild |
		discordgo.PermissionSendMessages |
		discordgo.PermissionManageMessages |
		discordgo.PermissionKickMembers)
	event := adminInteraction("guild-1", perms, true)
	if bot.authorized(event) {
		t.Fatal("non-administrator must not be authorized")
	}
}

func TestAuthorizedRejectsDirectMessages(t *testing.T) {
	bot := &DiscordBot{}
	if bot.authorized(adminInteraction("", discordgo.PermissionAdministrator, false)) {
		t.Fatal("DM interaction without member permissions must not be authorized")
	}
	if bot.authorized(adminInteraction("guild-1", discordgo.PermissionAdministrator, false)) {
		t.Fatal("guild interaction without member payload must not be authorized")
	}
}

func TestAuthorizedRejectsForeignGuild(t *testing.T) {
	bot := &DiscordBot{guildID: "guild-1"}
	if bot.authorized(adminInteraction("guild-2", discordgo.PermissionAdministrator, true)) {
		t.Fatal("administrator of a foreign guild must not control the bot")
	}
}

func TestAuthorizedWithoutGuildAllowlistAcceptsAnyGuild(t *testing.T) {
	bot := &DiscordBot{}
	if !bot.authorized(adminInteraction("guild-9", discordgo.PermissionAdministrator, true)) {
		t.Fatal("without DISCORD_GUILD_ID any guild administrator is authorized")
	}
}

func TestAuthorizedRejectsNilEvent(t *testing.T) {
	bot := &DiscordBot{}
	if bot.authorized(nil) {
		t.Fatal("nil event must not be authorized")
	}
	if bot.authorized(&discordgo.InteractionCreate{}) {
		t.Fatal("event without interaction must not be authorized")
	}
}

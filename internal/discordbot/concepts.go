package discordbot

import (
	"fmt"
	"path/filepath"
	"strings"

	appconfig "spar-tilbuds-bot/internal/config"
	"spar-tilbuds-bot/internal/match"
	"spar-tilbuds-bot/internal/search"
	"spar-tilbuds-bot/internal/store"

	"github.com/bwmarrin/discordgo"
)

// conceptsPath returns the concepts.toml location: CONCEPTS_PATH when
// set, else next to the config file. In the Docker deployment both live
// on the /data bind mount (/data/concepts.toml), so the file and the
// SQLite overrides in /data/bot.db survive container updates.
func (b *DiscordBot) conceptsPath() string {
	if override := strings.TrimSpace(appconfig.GetEnv("CONCEPTS_PATH")); override != "" {
		return override
	}
	return filepath.Join(filepath.Dir(b.configStore.Path()), "concepts.toml")
}

// rebuildConcepts assembles the effective registry from the concepts.toml
// base layer plus SQLite overrides and installs it into the matchers.
// Returns where the base data came from for admin listings.
func (b *DiscordBot) rebuildConcepts() (search.ConceptSource, error) {
	var custom []search.Concept
	var removed []string
	if b.db != nil {
		rows, err := b.db.ListConcepts()
		if err != nil {
			return search.ConceptSource{}, err
		}
		for _, row := range rows {
			if row.Deleted {
				removed = append(removed, row.Name)
				continue
			}
			custom = append(custom, search.Concept{
				Name:        row.Name,
				Description: row.Description,
				Aliases:     row.Aliases,
				Refs:        row.Refs,
			})
		}
	}
	registry, source, err := search.BuildRegistry(b.conceptsPath(), custom, removed)
	if err != nil {
		return source, err
	}
	b.registry = registry
	match.SetConcepts(registry)
	return source, nil
}

// handleConcepts is the admin command for the concept/alias registry:
// listing concepts, showing one, editing persisted overrides, hiding
// concepts and reloading the base file.
func (b *DiscordBot) handleConcepts(session *discordgo.Session, event *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	if len(data.Options) == 0 {
		b.replyEphemeral(session, event.Interaction, "Brug /concepts list, /concepts show, /concepts set, /concepts remove, /concepts restore eller /concepts reload.")
		return
	}
	sub := data.Options[0]
	switch sub.Name {
	case "list":
		b.handleConceptsList(session, event, sub.Options)
	case "show":
		b.handleConceptsShow(session, event, sub.Options)
	case "set":
		b.handleConceptsSet(session, event, sub.Options)
	case "remove":
		b.handleConceptsRemove(session, event, sub.Options)
	case "restore":
		b.handleConceptsRestore(session, event, sub.Options)
	case "reload":
		b.handleConceptsReload(session, event)
	default:
		b.replyEphemeral(session, event.Interaction, "Ukendt /concepts-underkommando.")
	}
}

func (b *DiscordBot) conceptsAvailable(session *discordgo.Session, interaction *discordgo.Interaction) bool {
	if b.db == nil {
		b.replyEphemeral(session, interaction, "Konceptdatabasen er ikke tilgængelig; kør uden SQLite kan koncepter kun læses fra concepts.toml.")
		return false
	}
	return true
}

// handleConceptsList lists every concept with its source: built into or
// loaded from concepts.toml, overridden/added in the database, or hidden
// by a tombstone.
func (b *DiscordBot) handleConceptsList(session *discordgo.Session, event *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	filter := strings.ToLower(subcommandString(options, "filter"))
	source, err := b.rebuildConcepts()
	if err != nil {
		b.replyEphemeral(session, event.Interaction, b.userError("Kunne ikke indlæse koncepter", err))
		return
	}

	baseNames := map[string]bool{}
	if base, _, err := search.LoadConcepts(b.conceptsPath()); err == nil {
		for _, concept := range base {
			// Key on the normalized name: concepts.toml entries and
			// database overrides use different spellings ("øl" vs "oel")
			// for the same concept.
			baseNames[search.Normalize(concept.Name)] = true
		}
	}
	overrides := map[string]store.ConceptRow{}
	if b.db != nil {
		if rows, err := b.db.ListConcepts(); err == nil {
			for _, row := range rows {
				overrides[search.Normalize(row.Name)] = row
			}
		}
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "Konceptregister (%s", sourceLabel(source))
	if source.Custom > 0 {
		fmt.Fprintf(&builder, ", %d tilpassede", source.Custom)
	}
	if source.Removed > 0 {
		fmt.Fprintf(&builder, ", %d skjulte", source.Removed)
	}
	builder.WriteString("):\n")

	shown := 0
	for _, name := range hiddenThenNames(overrides, baseNames) {
		if filter != "" && !strings.Contains(name, filter) {
			continue
		}
		row, isOverride := overrides[name]
		mark := "toml"
		if isOverride {
			if row.Deleted {
				mark = "skjult"
			} else {
				mark = "database"
			}
		}
		fmt.Fprintf(&builder, "\n• %s [%s]", name, mark)
		shown++
		if shown >= 200 {
			builder.WriteString("\n…")
			break
		}
	}
	if shown == 0 {
		builder.WriteString("\nIngen koncepter matcher.")
	}
	b.replyEphemeral(session, event.Interaction, builder.String())
}

// hiddenThenNames lists tombstoned names first (they are otherwise
// invisible), then all remaining names sorted.
func hiddenThenNames(overrides map[string]store.ConceptRow, baseNames map[string]bool) []string {
	names := map[string]bool{}
	for name := range overrides {
		names[name] = true
	}
	for name := range baseNames {
		names[name] = true
	}
	hidden := make([]string, 0)
	rest := make([]string, 0, len(names))
	for name := range names {
		if row, ok := overrides[name]; ok && row.Deleted {
			hidden = append(hidden, name)
			continue
		}
		rest = append(rest, name)
	}
	sortStrings(hidden)
	sortStrings(rest)
	return append(hidden, rest...)
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func sourceLabel(source search.ConceptSource) string {
	if source.Embedded {
		return "indbygget concepts.toml"
	}
	return source.Path
}

// handleConceptsShow displays one concept: its metadata, the effective
// alias expansion and which layer it came from.
func (b *DiscordBot) handleConceptsShow(session *discordgo.Session, event *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	name := strings.TrimSpace(subcommandString(options, "navn"))
	if name == "" {
		b.replyEphemeral(session, event.Interaction, "Angiv et konceptnavn.")
		return
	}
	source, err := b.rebuildConcepts()
	if err != nil {
		b.replyEphemeral(session, event.Interaction, b.userError("Kunne ikke indlæse koncepter", err))
		return
	}

	registry := match.DefaultConcepts()
	var builder strings.Builder
	fmt.Fprintf(&builder, "Koncept %q (fra %s):", name, sourceLabel(source))

	concept, ok := registry.Resolve(name)
	if !ok {
		builder.WriteString("\n\nFindes ikke (måske skjult?). Brug /concepts list.")
		b.replyEphemeral(session, event.Interaction, builder.String())
		return
	}
	if concept.Description != "" {
		fmt.Fprintf(&builder, "\n\n%s", concept.Description)
	}
	fmt.Fprintf(&builder, "\n\nAliases: %s", strings.Join(concept.Aliases, ", "))
	if len(concept.ExactAliases) > 0 {
		fmt.Fprintf(&builder, "\nKun hele ord: %s", strings.Join(concept.ExactAliases, ", "))
	}
	if len(concept.Refs) > 0 {
		fmt.Fprintf(&builder, "\nReferencer: %s", strings.Join(concept.Refs, ", "))
	}
	if b.registry != nil {
		if expansion, err := b.registry.Expand(name, 0); err == nil {
			fmt.Fprintf(&builder, "\n\nUdvidelse (%d termer): %s", len(expansion.Aliases), expansion.Joined())
		}
	}
	if row, ok := databaseOverride(b, name); ok {
		if row.Deleted {
			builder.WriteString("\n\nStatus: skjult i database (synlig med /concepts restore).")
		} else {
			builder.WriteString("\n\nStatus: tilpasset i database (nulstil med /concepts restore).")
		}
	}
	b.replyEphemeral(session, event.Interaction, builder.String())
}

func databaseOverride(b *DiscordBot, name string) (store.ConceptRow, bool) {
	if b.db == nil {
		return store.ConceptRow{}, false
	}
	rows, err := b.db.ListConcepts()
	if err != nil {
		return store.ConceptRow{}, false
	}
	norm := search.Normalize(name)
	for _, row := range rows {
		if search.Normalize(row.Name) == norm {
			return row, true
		}
	}
	return store.ConceptRow{}, false
}

// handleConceptsSet persists a concept override (or a brand new concept).
// The result is validated as a full registry before saving, so a broken
// reference never reaches the matcher.
func (b *DiscordBot) handleConceptsSet(session *discordgo.Session, event *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	if !b.conceptsAvailable(session, event.Interaction) {
		return
	}
	name := search.Normalize(subcommandString(options, "navn"))
	if name == "" {
		b.replyEphemeral(session, event.Interaction, "Angiv et konceptnavn.")
		return
	}
	row := store.ConceptRow{
		Name:        name,
		Description: subcommandString(options, "beskrivelse"),
		Aliases:     splitList(subcommandString(options, "aliases")),
		Refs:        splitList(subcommandString(options, "refs")),
	}
	if len(row.Aliases) == 0 {
		b.replyEphemeral(session, event.Interaction, "Angiv mindst ét alias, komma-separeret.")
		return
	}

	// Validate the would-be registry before persisting anything.
	if err := b.validateConceptChange(row); err != nil {
		b.replyEphemeral(session, event.Interaction, b.userError("Konceptet kan ikke gemmes", err))
		return
	}
	if err := b.db.UpsertConcept(row); err != nil {
		b.replyEphemeral(session, event.Interaction, b.userError("Kunne ikke gemme konceptet", err))
		return
	}
	if _, err := b.rebuildConcepts(); err != nil {
		b.replyEphemeral(session, event.Interaction, b.userError("Gemt, men register kunne ikke genindlæses", err))
		return
	}
	b.replyEphemeral(session, event.Interaction,
		fmt.Sprintf("Gemte koncept %q med %d aliaser. Det gælder med det samme.", name, len(row.Aliases)))
}

// validateConceptChange builds the registry that a row change would
// produce and reports why it is invalid, if it is.
func (b *DiscordBot) validateConceptChange(row store.ConceptRow) error {
	return b.validateRegistryWith([]search.Concept{{Name: row.Name, Description: row.Description, Aliases: row.Aliases, Refs: row.Refs}}, search.Normalize(row.Name))
}

// validateConceptDelete builds the registry that hiding a concept would
// produce. Removing a concept that others reference must fail before the
// tombstone is written, otherwise the database holds a state the matcher
// cannot load.
func (b *DiscordBot) validateConceptDelete(name string) error {
	return b.validateRegistryWith(nil, search.Normalize(name))
}

// validateRegistryWith rebuilds the registry with one concept replaced (when
// override is non-nil) and one name hidden, without persisting anything.
func (b *DiscordBot) validateRegistryWith(override []search.Concept, hidden string) error {
	existing, err := b.db.ListConcepts()
	if err != nil {
		return err
	}
	custom := append([]search.Concept(nil), override...)
	removed := []string{}
	if hidden != "" {
		removed = append(removed, hidden)
	}
	for _, other := range existing {
		if search.Normalize(other.Name) == hidden {
			continue
		}
		if other.Deleted {
			removed = append(removed, other.Name)
			continue
		}
		custom = append(custom, search.Concept{
			Name: other.Name, Description: other.Description, Aliases: other.Aliases, Refs: other.Refs,
		})
	}
	_, _, err = search.BuildRegistry(b.conceptsPath(), custom, removed)
	return err
}

// splitList parses a comma-separated Discord option value.
func splitList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// handleConceptsRemove hides a concept (tombstone). The row keeps the
// knowledge that it was removed, so /concepts restore can bring it back.
func (b *DiscordBot) handleConceptsRemove(session *discordgo.Session, event *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	if !b.conceptsAvailable(session, event.Interaction) {
		return
	}
	name := search.Normalize(subcommandString(options, "navn"))
	if name == "" {
		b.replyEphemeral(session, event.Interaction, "Angiv et konceptnavn.")
		return
	}
	row := store.ConceptRow{Name: name, Deleted: true}
	// Validate before persisting: a tombstone that breaks a reference would
	// leave the stored registry unloadable.
	if err := b.validateConceptDelete(name); err != nil {
		b.replyEphemeral(session, event.Interaction, b.userError("Konceptet kan ikke skjules", userFacing(err)))
		return
	}
	if err := b.db.UpsertConcept(row); err != nil {
		b.replyEphemeral(session, event.Interaction, b.userError("Kunne ikke skjule konceptet", err))
		return
	}
	if _, err := b.rebuildConcepts(); err != nil {
		b.replyEphemeral(session, event.Interaction, b.userError("Skjult, men register kunne ikke genindlæses", err))
		return
	}
	b.replyEphemeral(session, event.Interaction, fmt.Sprintf("Skjulte koncept %q. Det er væk fra søgningen med det samme.", name))
}

// handleConceptsRestore drops the database override for a concept,
// returning it to its concepts.toml state (or removing it entirely when
// it only ever existed in the database).
func (b *DiscordBot) handleConceptsRestore(session *discordgo.Session, event *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	if !b.conceptsAvailable(session, event.Interaction) {
		return
	}
	name := search.Normalize(subcommandString(options, "navn"))
	if name == "" {
		b.replyEphemeral(session, event.Interaction, "Angiv et konceptnavn.")
		return
	}
	removed, err := b.db.DeleteConcept(name)
	if err != nil {
		b.replyEphemeral(session, event.Interaction, b.userError("Kunne ikke nulstille konceptet", err))
		return
	}
	if !removed {
		b.replyEphemeral(session, event.Interaction,
			fmt.Sprintf("Koncept %q har ingen database-ændring; det er allerede i concepts.toml-tilstand.", name))
		return
	}
	if _, err := b.rebuildConcepts(); err != nil {
		b.replyEphemeral(session, event.Interaction, b.userError("Nulstillet, men register kunne ikke genindlæses", err))
		return
	}
	b.replyEphemeral(session, event.Interaction, fmt.Sprintf("Nulstillede koncept %q til concepts.toml.", name))
}

// handleConceptsReload re-reads concepts.toml from disk (edited on the
// mounted /data volume in Docker) together with the database overrides,
// without restarting the bot.
func (b *DiscordBot) handleConceptsReload(session *discordgo.Session, event *discordgo.InteractionCreate) {
	source, err := b.rebuildConcepts()
	if err != nil {
		b.replyEphemeral(session, event.Interaction, b.userError("Kunne ikke genindlæse koncepter", err))
		return
	}
	registry := b.registry
	count := 0
	if registry != nil {
		count = len(registry.Concepts())
	}
	b.replyEphemeral(session, event.Interaction,
		fmt.Sprintf("Genindlæste %d koncepter fra %s (inkl. database-tilpasninger).", count, sourceLabel(source)))
}

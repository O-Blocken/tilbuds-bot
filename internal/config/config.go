package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"spar-tilbuds-bot/internal/match"
)

const Path = "config.toml"

type BotConfig struct {
	ChannelID        int64
	FotexChannelID   int64
	LidlChannelID    int64
	LastWeek         string
	FotexLastWeek    string
	LidlLastWeek     string
	PingRoleID       int64
	FotexPingRoleID  int64
	LidlPingRoleID   int64
	CheckCron        string
	NotificationTime string
	Matcher          string
	Keywords         []string
	Wants            []match.Want
	PostedMessages   []PostedMessage
}

type PostedMessage struct {
	ChannelID string
	MessageID string
	// Retailer is the feed ("spar", "fotex", "lidl"); empty means the
	// legacy SPAR-only format.
	Retailer string
}

type ConfigStore struct {
	path string
	mu   sync.Mutex
}

func NewConfigStore(path string) *ConfigStore {
	if path == "" {
		path = Path
	}
	return &ConfigStore{path: path}
}

// Path returns the config file location. Sidecar files (the SQLite
// state, an optional concepts.toml override) live next to it, so the
// Docker bind mount at /data persists all of them together.
func (s *ConfigStore) Path() string { return s.path }

func (s *ConfigStore) Load() (BotConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadUnlocked()
}

func (s *ConfigStore) Save(config BotConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveUnlocked(config)
}

func (s *ConfigStore) Update(mutate func(*BotConfig)) (BotConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	config, err := s.loadUnlocked()
	if err != nil {
		return BotConfig{}, err
	}
	mutate(&config)
	config = config.withDefaults()
	if err := s.saveUnlocked(config); err != nil {
		return BotConfig{}, err
	}
	return config, nil
}

// Markers this package writes and must not re-read as user content, so a
// save/load cycle stays idempotent.
const (
	configHeader = "# Bot configuration"
	extrasBanner = "# Preserved verbatim by the bot (unknown sections and keys):"
)

// configExtras is content this package does not model. It is collected on
// every read and written back on every save, so hand edits in a
// bot-managed file (extra sections, unknown keys, comments) survive instead
// of being silently dropped by the generated template.
type configExtras struct {
	// preamble holds comment lines before the first section header.
	preamble []string
	// bot holds unknown keys and comments from the [bot] table. They are
	// re-emitted inside [bot], which keeps their meaning intact.
	bot []string
	// tail holds whole unknown sections plus comment lines for keys this
	// package ignores inside [[want]] blocks.
	tail []string
}

func (e configExtras) empty() bool {
	return len(e.preamble) == 0 && len(e.bot) == 0 && len(e.tail) == 0
}

// knownBotKeys are the [bot] keys the generated template owns.
var knownBotKeys = map[string]bool{
	"channel_id":         true,
	"fotex_channel_id":   true,
	"lidl_channel_id":    true,
	"last_week":          true,
	"fotex_last_week":    true,
	"lidl_last_week":     true,
	"ping_role_id":       true,
	"fotex_ping_role_id": true,
	"lidl_ping_role_id":  true,
	"check_cron":         true,
	"notification_time":  true,
	"matcher":            true,
	"keywords":           true,
	"posted_messages":    true,
}

// knownWantKeys are the [[want]] keys this package models.
var knownWantKeys = map[string]bool{
	"id":              true,
	"describe":        true,
	"include":         true,
	"exclude":         true,
	"query":           true,
	"max_price":       true,
	"max_unit_price":  true,
	"allow_app_price": true,
	"threshold":       true,
	"retailers":       true,
}

func (s *ConfigStore) loadUnlocked() (BotConfig, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return BotConfig{}, nil
		}
		return BotConfig{}, err
	}
	config, _ := parseConfig(data)
	return config, nil
}

// configParser is the line-oriented state machine that decodes the TOML
// subset this package owns and records every line it does not own.
type configParser struct {
	values   map[string]map[string]string
	wants    []map[string]string
	wantIDs  []string
	extras   configExtras
	section  string
	inWant   bool
	known    bool // current table is owned by this package
	preserve bool // current region is copied to extras verbatim
}

// parseConfig decodes the config file and reports content it does not model.
func parseConfig(data []byte) (BotConfig, configExtras) {
	parser := &configParser{values: map[string]map[string]string{}}
	lines := strings.Split(string(data), "\n")
	for index := 0; index < len(lines); index++ {
		index = parser.consume(lines, index)
	}
	return parser.config(), parser.extras
}

// consume handles one logical line (a multi-line array spans several) and
// returns the index of the last physical line it used.
func (p *configParser) consume(lines []string, index int) int {
	trimmed := strings.TrimSpace(lines[index])
	line := strings.TrimSpace(stripComment(lines[index]))
	if line == "" {
		p.comment(trimmed)
		return index
	}
	if p.tableHeader(trimmed, line) {
		return index
	}
	return p.assignment(lines, index, line)
}

// comment records a user comment, ignoring blank and generated lines.
func (p *configParser) comment(trimmed string) {
	if !strings.HasPrefix(trimmed, "#") || isGeneratedMarker(trimmed) {
		return
	}
	switch {
	case p.preserve:
		p.extras.tail = append(p.extras.tail, trimmed)
	case p.section == "" && !p.known:
		p.extras.preamble = append(p.extras.preamble, trimmed)
	case p.section == "bot" && !p.inWant:
		p.extras.bot = append(p.extras.bot, trimmed)
	}
}

// tableHeader recognises [table] and [[array of tables]] headers.
func (p *configParser) tableHeader(trimmed, line string) bool {
	if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") {
		p.startWant(strings.TrimSpace(line[2:len(line)-2]) == "want", trimmed)
		return true
	}
	if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
		p.startTable(strings.TrimSpace(line[1:len(line)-1]), trimmed)
		return true
	}
	return false
}

func (p *configParser) startWant(isWant bool, trimmed string) {
	p.inWant = isWant
	p.known = isWant
	p.preserve = !isWant
	if p.preserve {
		p.extras.tail = append(p.extras.tail, trimmed)
		return
	}
	p.wants = append(p.wants, map[string]string{})
	p.wantIDs = append(p.wantIDs, "")
}

func (p *configParser) startTable(name, trimmed string) {
	p.inWant = false
	p.section = name
	p.known = name == "bot"
	p.preserve = !p.known
	if p.preserve {
		p.extras.tail = append(p.extras.tail, trimmed)
	}
	if _, ok := p.values[name]; !ok {
		p.values[name] = map[string]string{}
	}
}

// assignment parses key = value, joining a multi-line array, and routes the
// result to the owning table or to the preserved extras.
func (p *configParser) assignment(lines []string, index int, line string) int {
	key, value, ok := strings.Cut(line, "=")
	if !ok || p.section == "" {
		if p.preserve {
			p.extras.tail = append(p.extras.tail, strings.TrimSpace(line))
		}
		return index
	}

	value = strings.TrimSpace(value)
	continuation := []string{strings.TrimSpace(line)}
	for strings.HasPrefix(value, "[") && !strings.Contains(value, "]") && index+1 < len(lines) {
		index++
		value += "\n" + strings.TrimSpace(stripComment(lines[index]))
		continuation = append(continuation, strings.TrimSpace(lines[index]))
	}

	key = strings.TrimSpace(key)
	switch {
	case p.preserve:
		p.extras.tail = append(p.extras.tail, continuation...)
	case p.inWant:
		p.wantField(key, value, continuation)
	case key == "" || !knownBotKeys[key]:
		p.extras.bot = append(p.extras.bot, continuation...)
	default:
		p.values[p.section][key] = value
	}
	return index
}

func (p *configParser) wantField(key, value string, continuation []string) {
	last := len(p.wants) - 1
	if last < 0 {
		return
	}
	if !knownWantKeys[key] {
		// A want block is regenerated from the fields this package models,
		// so an unknown key cannot stay machine-readable. Keep it as a
		// comment instead of dropping it.
		p.extras.tail = append(p.extras.tail,
			fmt.Sprintf("#   want %q: %s", p.wantIDs[last], strings.Join(continuation, " ")))
		return
	}
	p.wants[last][key] = value
	if key == "id" {
		p.wantIDs[last] = parseTomlString(value)
	}
}

// config builds the typed config from the parsed tables.
func (p *configParser) config() BotConfig {
	bot := p.values["bot"]
	config := BotConfig{
		ChannelID:        parseInt64(bot["channel_id"]),
		FotexChannelID:   parseInt64(bot["fotex_channel_id"]),
		LidlChannelID:    parseInt64(bot["lidl_channel_id"]),
		LastWeek:         parseTomlString(bot["last_week"]),
		FotexLastWeek:    parseTomlString(bot["fotex_last_week"]),
		LidlLastWeek:     parseTomlString(bot["lidl_last_week"]),
		PingRoleID:       parseInt64(bot["ping_role_id"]),
		FotexPingRoleID:  parseInt64(bot["fotex_ping_role_id"]),
		LidlPingRoleID:   parseInt64(bot["lidl_ping_role_id"]),
		CheckCron:        parseTomlString(bot["check_cron"]),
		NotificationTime: parseTomlString(bot["notification_time"]),
		Matcher:          parseTomlString(bot["matcher"]),
		Keywords:         nonEmptyStrings(parseTomlStringArray(bot["keywords"])),
		Wants:            parseWants(p.wants),
		PostedMessages:   parsePostedMessages(parseTomlStringArray(bot["posted_messages"])),
	}
	return config.withDefaults()
}

// isGeneratedMarker reports whether a comment line is written by this
// package itself, so re-reading a saved file does not duplicate it.
func isGeneratedMarker(line string) bool {
	return line == configHeader || line == extrasBanner
}

// EffectiveWants returns the configured wants, or one normalized want per
// legacy keyword when no [[want]] blocks are defined.
func (c BotConfig) EffectiveWants() []match.Want {
	if len(c.Wants) > 0 {
		return c.Wants
	}
	return match.WantsFromKeywords(c.Keywords)
}

// nonEmptyStrings normalizes empty slices to nil so TOML round-trips
// stay stable regardless of how an empty list was written.
func nonEmptyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return values
}

func parseWants(blocks []map[string]string) []match.Want {
	wants := make([]match.Want, 0, len(blocks))
	for _, block := range blocks {
		id := parseTomlString(block["id"])
		if strings.TrimSpace(id) == "" {
			continue
		}
		want := match.Want{
			ID:        id,
			Describe:  parseTomlString(block["describe"]),
			Include:   nonEmptyStrings(parseTomlStringArray(block["include"])),
			Exclude:   nonEmptyStrings(parseTomlStringArray(block["exclude"])),
			Retailers: nonEmptyStrings(parseTomlStringArray(block["retailers"])),
		}
		if value, ok := parseTomlFloat(block["max_price"]); ok {
			value := value
			want.MaxPrice = &value
		}
		if value, ok := parseTomlFloat(block["max_unit_price"]); ok {
			value := value
			want.MaxUnitPrice = &value
		}
		if value, ok := parseTomlBool(block["allow_app_price"]); ok {
			value := value
			want.AllowAppPrice = &value
		}
		if value, ok := parseTomlFloat(block["threshold"]); ok {
			want.Threshold = value
		}
		wants = append(wants, want)
	}
	if len(wants) == 0 {
		return nil
	}
	return wants
}

// saveUnlocked rewrites the whole config file atomically. Content this
// package does not model (unknown sections, unknown [bot] keys, comments) is
// read back from the current file and re-emitted, so a hand-edited config is
// never silently truncated to the generated template.
func (s *ConfigStore) saveUnlocked(config BotConfig) error {
	config = config.withDefaults()
	return writeFileAtomic(s.path, renderConfig(config, s.currentExtras()), configFileMode)
}

// configFileMode is readable by the host user that owns the bind mount; the
// file holds no secrets, only channel, role and message IDs.
const configFileMode os.FileMode = 0o644

// currentExtras re-reads the file on disk so preservation does not depend on
// whether the caller ran Load first (Save and Update have the same effect).
func (s *ConfigStore) currentExtras() configExtras {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return configExtras{}
	}
	_, extras := parseConfig(data)
	return extras
}

// renderConfig renders the managed keys followed by whatever the previous
// file contained that this package does not own.
func renderConfig(config BotConfig, extras configExtras) []byte {
	var builder strings.Builder
	for _, line := range extras.preamble {
		builder.WriteString(line)
		builder.WriteByte('\n')
	}

	fmt.Fprintf(&builder, `%s
[bot]
channel_id = %d
fotex_channel_id = %d
lidl_channel_id = %d
last_week = "%s"
fotex_last_week = "%s"
lidl_last_week = "%s"
ping_role_id = %d
fotex_ping_role_id = %d
lidl_ping_role_id = %d
check_cron = "%s"
notification_time = "%s"
matcher = "%s"
keywords = %s
posted_messages = %s
`, configHeader,
		config.ChannelID,
		config.FotexChannelID,
		config.LidlChannelID,
		escapeTomlString(config.LastWeek),
		escapeTomlString(config.FotexLastWeek),
		escapeTomlString(config.LidlLastWeek),
		config.PingRoleID,
		config.FotexPingRoleID,
		config.LidlPingRoleID,
		escapeTomlString(config.CheckCron),
		escapeTomlString(config.NotificationTime),
		escapeTomlString(config.Matcher),
		formatTomlStringArray(config.Keywords),
		formatTomlStringArray(formatPostedMessages(config.PostedMessages)),
	)
	for _, line := range extras.bot {
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	builder.WriteString(formatWants(config.Wants))

	if len(extras.tail) > 0 {
		builder.WriteString("\n")
		builder.WriteString(extrasBanner)
		builder.WriteByte('\n')
		for _, line := range extras.tail {
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
	}
	return []byte(builder.String())
}

// writeFileAtomic writes data to a temp file in the target directory, flushes
// it, keeps a best-effort .bak of the previous contents and renames over the
// target, so a crash mid-write cannot leave a truncated state file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	writeBackup(path, perm)
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}

// writeBackup keeps the previous state file as path+”.bak”. Best effort: a
// failed backup must not block the save that follows it.
func writeBackup(path string, perm os.FileMode) {
	previous, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_ = os.WriteFile(path+".bak", previous, perm)
}

// syncDir flushes the directory entry created by the rename. Unsupported on
// some platforms, so errors are ignored on purpose.
func syncDir(dir string) {
	directory, err := os.Open(dir)
	if err != nil {
		return
	}
	defer directory.Close()
	_ = directory.Sync()
}

func formatWants(wants []match.Want) string {
	var builder strings.Builder
	for _, want := range wants {
		builder.WriteString("[[want]]\n")
		builder.WriteString(`id = "`)
		builder.WriteString(escapeTomlString(want.ID))
		builder.WriteString("\"\n")
		if strings.TrimSpace(want.Describe) != "" {
			builder.WriteString(`describe = "`)
			builder.WriteString(escapeTomlString(want.Describe))
			builder.WriteString("\"\n")
		}
		if len(want.Include) > 0 {
			builder.WriteString("include = ")
			builder.WriteString(formatTomlStringArray(want.Include))
			builder.WriteString("\n")
		}
		if len(want.Exclude) > 0 {
			builder.WriteString("exclude = ")
			builder.WriteString(formatTomlStringArray(want.Exclude))
			builder.WriteString("\n")
		}
		if want.MaxPrice != nil {
			builder.WriteString("max_price = ")
			builder.WriteString(formatTomlFloat(*want.MaxPrice))
			builder.WriteString("\n")
		}
		if want.MaxUnitPrice != nil {
			builder.WriteString("max_unit_price = ")
			builder.WriteString(formatTomlFloat(*want.MaxUnitPrice))
			builder.WriteString("\n")
		}
		if want.AllowAppPrice != nil {
			builder.WriteString(fmt.Sprintf("allow_app_price = %t\n", *want.AllowAppPrice))
		}
		if want.Threshold > 0 {
			builder.WriteString("threshold = ")
			builder.WriteString(formatTomlFloat(want.Threshold))
			builder.WriteString("\n")
		}
		if len(want.Retailers) > 0 {
			builder.WriteString("retailers = ")
			builder.WriteString(formatTomlStringArray(want.Retailers))
			builder.WriteString("\n")
		}
		builder.WriteString("\n")
	}
	return builder.String()
}

func (c BotConfig) withDefaults() BotConfig {
	if strings.TrimSpace(c.CheckCron) == "" {
		c.CheckCron = DefaultCheckCron
	}
	if strings.TrimSpace(c.NotificationTime) == "" {
		c.NotificationTime = DefaultNotificationTime
	}
	if strings.TrimSpace(c.Matcher) == "" {
		c.Matcher = match.DefaultMatcher
	}
	if len(c.Keywords) == 0 {
		c.Keywords = append([]string(nil), DefaultKeywords...)
	}
	return c
}

func parsePostedMessages(values []string) []PostedMessage {
	messages := make([]PostedMessage, 0, len(values))
	for _, value := range values {
		parts := strings.Split(strings.TrimSpace(value), ":")
		switch len(parts) {
		case 3:
			if parts[0] == "" || parts[1] == "" || parts[2] == "" {
				continue
			}
			messages = append(messages, PostedMessage{Retailer: parts[0], ChannelID: parts[1], MessageID: parts[2]})
		case 2:
			// Legacy "channel:message" entries predate per-retailer feeds.
			if parts[0] == "" || parts[1] == "" {
				continue
			}
			messages = append(messages, PostedMessage{Retailer: "spar", ChannelID: parts[0], MessageID: parts[1]})
		default:
			continue
		}
	}
	return messages
}

func formatPostedMessages(messages []PostedMessage) []string {
	values := make([]string, 0, len(messages))
	for _, message := range messages {
		if message.ChannelID == "" || message.MessageID == "" {
			continue
		}
		retailer := strings.TrimSpace(message.Retailer)
		if retailer == "" {
			retailer = "spar"
		}
		values = append(values, retailer+":"+message.ChannelID+":"+message.MessageID)
	}
	return values
}

// FilterPostedMessages keeps tracked messages for one retailer. Legacy
// entries without a retailer belong to the SPAR feed.
func FilterPostedMessages(messages []PostedMessage, retailer string) []PostedMessage {
	filtered := make([]PostedMessage, 0, len(messages))
	for _, message := range messages {
		owner := strings.TrimSpace(message.Retailer)
		if owner == "" {
			owner = "spar"
		}
		if owner == retailer {
			filtered = append(filtered, message)
		}
	}
	return filtered
}

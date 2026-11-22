package search

// Limits are defensive query caps. They keep
// pathological or accidental queries from exploding concept expansion or
// matching cost.
type Limits struct {
	// MaxQueryRunes caps the raw query length.
	MaxQueryRunes int
	// MaxTerms caps positive + excluded terms.
	MaxTerms int
	// MaxFilters caps structured filters.
	MaxFilters int
	// MaxPhraseRunes caps a single quoted phrase.
	MaxPhraseRunes int
	// MaxExpansionAliases caps the normalized alternatives one term may
	// expand to (concept aliases plus nested references).
	MaxExpansionAliases int
}

// DefaultLimits returns the built-in caps.
func DefaultLimits() Limits {
	return Limits{
		MaxQueryRunes:       400,
		MaxTerms:            24,
		MaxFilters:          12,
		MaxPhraseRunes:      64,
		MaxExpansionAliases: 48,
	}
}

// Config tunes engine behavior that must not be hardcoded. The zero value is
// not usable; start from DefaultConfig.
type Config struct {
	// Mode is the default positive-term semantics for parsed queries.
	Mode PositiveMode

	// SearchDescription includes offer descriptions in searchable text.
	SearchDescription bool
	// SearchDepartment includes the department slug in searchable text.
	SearchDepartment bool
	// SearchAttributes includes attribute values in searchable text.
	SearchAttributes bool

	// MissingFieldFails decides structured-filter behavior when the
	// offer lacks the required field: true rejects (the documented
	// default for the query language), false never vetoes (legacy
	// keyword behavior).
	MissingFieldFails bool

	// Limits caps query size and expansion.
	Limits Limits

	// StoreAliases maps normalized store spellings ("føtex", "fotex",
	// "foetex") to a canonical identifier ("fotex").
	StoreAliases map[string]string
	// DepartmentAliases maps normalized words ("kød", "frugt") to
	// canonical department slugs ("meat-and-fish").
	DepartmentAliases map[string]string
}

// DefaultConfig returns the documented defaults: AND semantics, name and
// description searchable, missing structured data fails explicit filters,
// and the standard store/department alias tables.
func DefaultConfig() Config {
	return Config{
		Mode:              ModeAll,
		SearchDescription: true,
		SearchAttributes:  true,
		MissingFieldFails: true,
		Limits:            DefaultLimits(),
		StoreAliases:      DefaultStoreAliases(),
		DepartmentAliases: DefaultDepartmentAliases(),
	}
}

// DefaultStoreAliases maps normalized user-facing store spellings to the
// canonical identifiers used by the bot's feeds ("spar", "fotex",
// "lidl"). Keys are written in normalized form ("føtex" folds to
// "foetex"), matching how Prepare canonicalizes offer stores.
func DefaultStoreAliases() map[string]string {
	return map[string]string{
		"spar":   "spar",
		"fotex":  "fotex",
		"foetex": "fotex",
		"lidl":   "lidl",
	}
}

// DefaultDepartmentAliases maps normalized Danish and English words to
// the department slugs used by the upstream offer data. Keys are in
// normalized form (diacritics folded, e.g. "paalaeg" for "pålæg").
func DefaultDepartmentAliases() map[string]string {
	return map[string]string{
		"broed":                 "bread-and-cakes",
		"brod":                  "bread-and-cakes",
		"broed og kager":        "bread-and-cakes",
		"bread":                 "bread-and-cakes",
		"bread-and-cakes":       "bread-and-cakes",
		"beverages":             "beverages",
		"drikkevarer":           "beverages",
		"drik":                  "beverages",
		"cold-cuts":             "cold-cuts",
		"paalaeg":               "cold-cuts",
		"household":             "household",
		"husholdning":           "household",
		"dairy-and-cold":        "dairy-and-cold",
		"mejeri":                "dairy-and-cold",
		"koel":                  "dairy-and-cold",
		"beauty":                "beauty",
		"skoenhed":              "beauty",
		"colonial":              "colonial",
		"kolonial":              "colonial",
		"electronics":           "electronics",
		"elektronik":            "electronics",
		"fruits-and-vegetables": "fruits-and-vegetables",
		"frugt":                 "fruits-and-vegetables",
		"groent":                "fruits-and-vegetables",
		"frugt og groent":       "fruits-and-vegetables",
		"frozen":                "frozen",
		"frost":                 "frozen",
		"meat-and-fish":         "meat-and-fish",
		"koed":                  "meat-and-fish",
		"fisk":                  "meat-and-fish",
		"koed og fisk":          "meat-and-fish",
		"entertainment":         "entertainment",
		"underholdning":         "entertainment",
		"apparel":               "apparel",
		"toej":                  "apparel",
		"pharmacy":              "pharmacy",
		"apotek":                "pharmacy",
		"snacks-and-candies":    "snacks-and-candies",
		"snacks":                "snacks-and-candies",
		"slik":                  "snacks-and-candies",
		"home":                  "home",
		"hjem":                  "home",
		"bolig":                 "home",
		"baby":                  "baby",
		"outdoor":               "outdoor",
		"udendoers":             "outdoor",
		"garden":                "garden",
		"have":                  "garden",
		"animal":                "animal",
		"kaeledyr":              "animal",
	}
}

package ortfodb

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v2"

	"github.com/anaskhan96/soup"
	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/parser"
	"github.com/mitchellh/mapstructure"

	"github.com/jaevor/go-nanoid"
	"github.com/k3a/html2text"
	"github.com/metal3d/go-slugify"
)

const (
	PatternLanguageMarker         string = `^::\s+(.+)$`
	PatternAbbreviationDefinition string = `^\s*\*\[([^\]]+)\]:\s+(.+)$`
	RuneLoop                      rune   = '~'
	RuneAutoplay                  rune   = '>'
	RuneHideControls              rune   = '='
)

// ParseYAMLHeader parses the YAML header of a description markdown file and returns the rest of the content (all except the YAML header).
func ParseYAMLHeader(descriptionRaw string) (WorkMetadata, string) {
	var inYAMLHeader bool
	var rawYAMLPart string
	var markdownPart string
	for _, line := range strings.Split(descriptionRaw, "\n") {
		// Replace tabs with four spaces
		for strings.HasPrefix(line, "\t") {
			line = strings.Repeat(" ", 4) + strings.TrimPrefix(line, "\t")
		}
		// A YAML header separator is 3 or more dashes on a line (without anything else on the same line)
		if strings.Trim(line, "-") == "" && strings.Count(line, "-") >= 3 {
			inYAMLHeader = !inYAMLHeader
			continue
		}
		if inYAMLHeader {
			rawYAMLPart += line + "\n"
		} else {
			markdownPart += line + "\n"
		}
	}
	var parsedYAMLPart map[string]interface{}
	yaml.Unmarshal([]byte(rawYAMLPart), &parsedYAMLPart)
	if parsedYAMLPart == nil {
		parsedYAMLPart = make(map[string]interface{})
	}

	metadata := WorkMetadata{}
	for key, value := range parsedYAMLPart {
		if strings.Contains(key, " ") {
			parsedYAMLPart[strings.ReplaceAll(key, " ", "_")] = value
			delete(parsedYAMLPart, key)
		}
	}
	mapstructure.Decode(parsedYAMLPart, &metadata)

	return metadata, markdownPart
}

// ParseDescription parses the markdown string from a description.md file and returns a ParsedDescription.
func (ctx *RunContext) ParseDescription(markdownRaw string) ParsedWork {
	metadata, markdownRaw := ParseYAMLHeader(markdownRaw)
	// notLocalizedRaw: raw markdown before the first language marker
	notLocalizedRaw, localizedRawBlocks := SplitOnLanguageMarkers(markdownRaw)
	localized := len(localizedRawBlocks) > 0
	var allLanguages []string
	if localized {
		allLanguages = mapKeys(localizedRawBlocks)
	} else {
		allLanguages = make([]string, 1)
		allLanguages[0] = "default" // TODO: make this configurable
	}
	paragraphs := make(map[string][]Paragraph)
	mediaEmbedDeclarations := make(map[string][]MediaEmbedDeclaration)
	links := make(map[string][]Link)
	title := make(map[string]HTMLString)
	footnotes := make(map[string]Footnotes)
	abbreviations := make(map[string]Abbreviations)
	orders := make(map[string][]string)
	for _, language := range allLanguages {
		// Unlocalized stuff appears the same in every language.
		raw := notLocalizedRaw
		if localized {
			raw += localizedRawBlocks[language]
		}
		title[language], paragraphs[language], mediaEmbedDeclarations[language], links[language], footnotes[language], abbreviations[language], orders[language] = ParseSingleLanguageDescription(raw)
	}
	return ParsedWork{
		Metadata:               metadata,
		Paragraphs:             paragraphs,
		Links:                  links,
		Title:                  title,
		MediaEmbedDeclarations: mediaEmbedDeclarations,
		Footnotes:              footnotes,
		ContentBlocksOrders:    orders,
	}
}

// Abbreviations represents the abbreviations declared in a description.md file.
type Abbreviations map[string]string

// Footnotes represents the footnote declarations in a description.md file.
type Footnotes map[string]HTMLString

// Paragraph represents a paragraph declaration in a description.md file.
type Paragraph struct {
	ID      string     `json:"id"`
	Index   int        `json:"index"`
	Anchor  string     `json:"anchor"`
	Content HTMLString `json:"content"` // html
}

// Link represents an (isolated) link declaration in a description.md file.
type Link struct {
	ID     string     `json:"id"`
	Anchor string     `json:"anchor"`
	Text   HTMLString `json:"text"`
	Title  string     `json:"title"`
	URL    string     `json:"url"`
}

// AnalyzedWork represents a complete work, with analyzed mediae.
type AnalyzedWork struct {
	ID        string                          `json:"id"`
	Metadata  WorkMetadata                    `json:"metadata"`
	Localized map[string]LocalizedWorkContent `json:"localized"`
}

type WorkMetadata struct {
	Aliases            []string                        `json:"aliases"`
	Finished           string                          `json:"finished"`
	Started            string                          `json:"started"`
	MadeWith           []string                        `json:"made_with"`
	Tags               []string                        `json:"tags"`
	Thumbnail          ThisOrtfoFolderRelativeFilePath `json:"thumbnail"`
	TitleStyle         TitleStyle                      `json:"title_style"`
	Colors             ColorPalette                    `json:"colors"`
	PageBackground     string                          `json:"page background"`
	WIP                bool                            `json:"wip"`
	Private            bool                            `json:"private"`
	AdditionalMetadata map[string]interface{}          `json:"additional_metadata" mapstructure:",remain"`
}

// Parsed returns a parsed work from the analyzed work, dropping analysis information.
func (w AnalyzedWork) Parsed() (p ParsedWork) {
	p.Metadata = w.Metadata
	for language, localized := range w.Localized {
		p.Paragraphs[language] = make([]Paragraph, 0, len(localized.Blocks))
		p.MediaEmbedDeclarations[language] = make([]MediaEmbedDeclaration, 0, len(localized.Blocks))
		p.Links[language] = make([]Link, 0, len(localized.Blocks))
		p.Title[language] = localized.Title
		p.Footnotes[language] = localized.Footnotes
		p.ContentBlocksOrders[language] = make([]string, 0, len(localized.Blocks))
		for _, block := range localized.Blocks {
			switch block.Type {
			case "paragraph":
				p.Paragraphs[language] = append(p.Paragraphs[language], block.Paragraph)
			case "media":
				p.MediaEmbedDeclarations[language] = append(p.MediaEmbedDeclarations[language], block.Media.EmbedDeclaration())
			case "link":
				p.Links[language] = append(p.Links[language], block.Link)
			}
			p.ContentBlocksOrders[language] = append(p.ContentBlocksOrders[language], block.ID())
		}
	}
	return
}

type TitleStyle string

type LocalizedWorkContent struct {
	Layout    Layout
	Blocks    []ContentBlock
	Title     HTMLString
	Footnotes Footnotes
}

type ContentBlock struct {
	Type ContentBlockType
	Media
	Paragraph
	Link
}

func (b ContentBlock) AsMedia() Media {
	if b.Type != "media" {
		panic("ContentBlock is not a media")
	}

	return Media{
		ID:             b.Media.ID,
		Anchor:         b.Media.Anchor,
		Alt:            b.Alt,
		Title:          b.Media.Title,
		DistSource:     b.DistSource,
		RelativeSource: b.RelativeSource,
		ContentType:    b.ContentType,
		Size:           b.Size,
		Dimensions:     b.Dimensions,
		Online:         b.Online,
		Duration:       b.Duration,
		Colors:         b.Colors,
		Thumbnails:     b.Thumbnails,
		Attributes:     b.Attributes,
	}
}

func (b ContentBlock) AsLink() Link {
	if b.Type != "link" {
		panic("ContentBlock is not a link")
	}

	return Link{
		ID:     b.Link.ID,
		Anchor: b.Link.Anchor,
		Text:   b.Text,
		Title:  b.Link.Title,
		URL:    b.URL,
	}
}

func (b ContentBlock) AsParagraph() Paragraph {
	if b.Type != "paragraph" {
		panic("ContentBlock is not a paragraph")
	}

	return Paragraph{
		ID:      b.Paragraph.ID,
		Anchor:  b.Paragraph.Anchor,
		Content: b.Content,
	}
}

func (b ContentBlock) ID() string {
	switch b.Type {
	case "media":
		return b.Media.ID
	case "paragraph":
		return b.Paragraph.ID
	case "link":
		return b.Link.ID
	default:
		panic("unknown content block type")
	}
}

type ThumbnailsMap map[int]MediaRootRelativeFilePath

type ThisOrtfoFolderRelativeFilePath string
type MediaRootRelativeFilePath string

func (f ThisOrtfoFolderRelativeFilePath) Absolute(ctx *RunContext, workID string) string {
	result, _ := filepath.Abs(filepath.Join(ctx.DatabaseDirectory, "..", string(f)))
	return result
}

type HTMLString string

func (s HTMLString) String() string {
	return html2text.HTML2Text(string(s))
}

// ContentBlockType is one of "paragraph", "media" or "link"
type ContentBlockType string

// Layout is a 2D array of content block IDs
type Layout [][]LayoutCell

// LayoutCell is a single cell in the layout. It corresponds to the content block's ID.
type LayoutCell string

// MediaEmbedDeclaration represents media embeds. (abusing the ![]() syntax to extend it to any file).
// Only stores the info extracted from the syntax, no filesystem interactions.
type MediaEmbedDeclaration struct {
	Anchor     string
	ID         string
	Alt        string
	Title      string
	Source     ThisOrtfoFolderRelativeFilePath
	Attributes MediaAttributes
}

// MediaAttributes stores which HTML attributes should be added to the media.
type MediaAttributes struct {
	Loop        bool // Controlled with attribute character ~ (adds)
	Autoplay    bool // Controlled with attribute character > (adds)
	Muted       bool // Controlled with attribute character > (adds)
	Playsinline bool // Controlled with attribute character = (adds)
	Controls    bool // Controlled with attribute character = (removes)
}

// ParsedWork represents a work, but without analyzed media. All it contains is information from the description.md file.
type ParsedWork struct {
	Metadata               WorkMetadata
	Title                  map[string]HTMLString
	Paragraphs             map[string][]Paragraph
	MediaEmbedDeclarations map[string][]MediaEmbedDeclaration
	Links                  map[string][]Link
	Footnotes              map[string]Footnotes
	ContentBlocksOrders    map[string][]string // nanoids of the content blocks
}

// SplitOnLanguageMarkers returns two values:
//  1. the text before any language markers
//  2. a map with language codes as keys and the content as values.
func SplitOnLanguageMarkers(markdownRaw string) (string, map[string]string) {
	lines := strings.Split(markdownRaw, "\n")
	pattern := regexp.MustCompile(PatternLanguageMarker)
	currentLanguage := ""
	before := ""
	markdownRawPerLanguage := map[string]string{}
	for _, line := range lines {
		if pattern.MatchString(line) {
			currentLanguage = pattern.FindStringSubmatch(line)[1]
			markdownRawPerLanguage[currentLanguage] = ""
		}
		if currentLanguage == "" {
			before += line + "\n"
		} else {
			markdownRawPerLanguage[currentLanguage] += line + "\n"
		}
	}
	return before, markdownRawPerLanguage
}

// ParseSingleLanguageDescription takes in raw markdown without language markers (called on splitOnLanguageMarker's output).
// and returns parsed arrays of structs that make up each language's part in ParsedDescription's maps.
// order contains an array of nanoids that represent the order of the content blocks as they are in the original file.
func ParseSingleLanguageDescription(markdownRaw string) (title HTMLString, paragraphs []Paragraph, mediae []MediaEmbedDeclaration, links []Link, footnotes Footnotes, abbreviations Abbreviations, order []string) {
	markdownRaw = HandleAltMediaEmbedSyntax(markdownRaw)
	htmlRaw := MarkdownToHTML(markdownRaw)
	htmlTree := soup.HTMLParse(htmlRaw)
	paragraphs = make([]Paragraph, 0)
	mediae = make([]MediaEmbedDeclaration, 0)
	links = make([]Link, 0)
	footnotes = make(Footnotes)
	abbreviations = make(Abbreviations)
	paragraphLike := make([]soup.Root, 0)
	paragraphLikeTagNames := "p ol ul h2 h3 h4 h5 h6 dl blockquote hr pre"
	order = make([]string, 0)
	idGenerator, _ := nanoid.Standard(5)
	for _, element := range htmlTree.Find("body").Children() {
		// Check if it's a paragraph-like tag
		if strings.Contains(paragraphLikeTagNames, element.NodeValue) {
			paragraphLike = append(paragraphLike, element)
		}
	}
	for _, paragraph := range paragraphLike {
		childrenCount := len(paragraph.Children())
		firstChild := soup.Root{}
		id := idGenerator()
		if childrenCount >= 1 {
			firstChild = paragraph.Children()[0]
		}
		if childrenCount == 1 && firstChild.NodeValue == "img" {
			// A media embed
			alt, attributes := ExtractAttributesFromAlt(firstChild.Attrs()["alt"])
			mediae = append(mediae, MediaEmbedDeclaration{
				Anchor:     slugify.Marshal(firstChild.Attrs()["src"]),
				ID:         id,
				Alt:        alt,
				Title:      firstChild.Attrs()["title"],
				Source:     ThisOrtfoFolderRelativeFilePath(firstChild.Attrs()["src"]),
				Attributes: attributes,
			})
			order = append(order, id)
		} else if childrenCount == 1 && firstChild.NodeValue == "a" {
			// An isolated link
			links = append(links, Link{
				ID:     id,
				Anchor: slugify.Marshal(firstChild.FullText(), true),
				Text:   innerHTML(firstChild),
				Title:  firstChild.Attrs()["title"],
				URL:    firstChild.Attrs()["href"],
			})
			order = append(order, id)
		} else if regexpMatches(PatternAbbreviationDefinition, string(innerHTML(paragraph))) {
			// An abbreviation definition
			groups := regexpGroups(PatternAbbreviationDefinition, string(innerHTML(paragraph)))
			abbreviations[groups[1]] = groups[2]
		} else if regexpMatches(PatternLanguageMarker, string(innerHTML(paragraph))) {
			// A language marker (ignored)
			continue
		} else {
			// A paragraph (anything else)
			paragraphs = append(paragraphs, Paragraph{
				ID:      id,
				Anchor:  paragraph.Attrs()["id"],
				Content: HTMLString(paragraph.HTML()),
			})
			order = append(order, id)
		}
	}
	if h1 := htmlTree.Find("h1"); h1.Error == nil {
		title = innerHTML(h1)
		for _, div := range htmlTree.FindAll("div") {
			if div.Attrs()["class"] == "footnotes" {
				for _, li := range div.FindAll("li") {
					footnotes[strings.TrimPrefix(li.Attrs()["id"], "fn:")] = trimHTMLWhitespace(innerHTML(li))
				}
			}
		}
	}
	processedParagraphs := make([]Paragraph, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		if strings.HasPrefix(string(paragraph.Content), "<pre>") && strings.HasSuffix(string(paragraph.Content), "</pre>") {
			// Dont insert <abbr>s while in <pre> text
			continue
		}
		processedParagraphs = append(processedParagraphs, ReplaceAbbreviations(paragraph, abbreviations))
	}
	return
}

// trimHTMLWhitespace removes whitespace from the beginning and end of an HTML string, also removing leading & trailing <br> tags.
func trimHTMLWhitespace(rawHTML HTMLString) HTMLString {
	rawHTML = HTMLString(strings.TrimSpace(string(rawHTML)))
	for _, toRemove := range []string{"<br>", "<br />", "<br/>"} {
		for strings.HasPrefix(string(rawHTML), toRemove) {
			rawHTML = HTMLString(strings.TrimPrefix(string(rawHTML), toRemove))
		}
		for strings.HasSuffix(string(rawHTML), toRemove) {
			rawHTML = HTMLString(strings.TrimSuffix(string(rawHTML), toRemove))
		}
	}
	return rawHTML
}

// HandleAltMediaEmbedSyntax handles the >[...](...) syntax by replacing it in htmlRaw with ![...](...).
func HandleAltMediaEmbedSyntax(markdownRaw string) string {
	pattern := regexp.MustCompile(`(?m)^>(\[[^\]]+\]\([^)]+\)\s*)$`)
	return pattern.ReplaceAllString(markdownRaw, "!$1")
}

// ExtractAttributesFromAlt extracts sigils from the end of the alt atetribute, returns the alt without them as well as the parse result.
func ExtractAttributesFromAlt(alt string) (string, MediaAttributes) {
	attrs := MediaAttributes{
		Controls: true, // Controls is added by default, others aren't
	}
	lastRune, _ := utf8.DecodeLastRuneInString(alt)
	// If there are no attributes in the alt string, the first (last in the alt string) will not be an attribute character.
	if !isMediaEmbedAttribute(lastRune) {
		return alt, attrs
	}
	altText := ""
	// We iterate backwardse:
	// if there are attributes, they'll be at the end of the alt text separated by a space
	inAttributesZone := true
	for i := len([]rune(alt)) - 1; i >= 0; i-- {
		char := []rune(alt)[i]
		if char == ' ' && inAttributesZone {
			inAttributesZone = false
			continue
		}
		if inAttributesZone {
			if char == RuneAutoplay {
				attrs.Autoplay = true
				attrs.Muted = true
			} else if char == RuneLoop {
				attrs.Loop = true
			} else if char == RuneHideControls {
				attrs.Controls = false
				attrs.Playsinline = true
			}
		} else {
			altText = string(char) + altText
		}
	}
	return altText, attrs
}

func isMediaEmbedAttribute(char rune) bool {
	return char == RuneAutoplay || char == RuneLoop || char == RuneHideControls
}

// innerHTML returns the HTML string of what's _inside_ the given element, just like JS' `element.innerHTML`.
func innerHTML(element soup.Root) HTMLString {
	var innerHTML string
	for _, child := range element.Children() {
		innerHTML += child.HTML()
	}
	if innerHTML == "" {
		innerHTML = element.HTML()
	}
	return HTMLString(innerHTML)
}

// MarkdownToHTML converts markdown markdownRaw into an HTML string.
func MarkdownToHTML(markdownRaw string) string {
	// TODO: add (ctx *RunContext) receiver, take markdown configuration into account when activating extensions
	extensions := parser.CommonExtensions | // Common stuff
		parser.Footnotes | // [^1]: footnotes
		parser.AutoHeadingIDs | // Auto-add [id] to headings
		parser.Attributes | // Specify attributes manually with {} above block
		parser.HardLineBreak | // \n becomes <br>
		parser.OrderedListStart | // Starting an <ol> with 5. will make them start at 5 in the output HTML
		parser.EmptyLinesBreakList // 2 empty lines break out of list
		// TODO: smart fractions, LaTeX-style dash parsing, smart quotes (see https://pkg.go.dev/github.com/gomarkdown/markdown@v0.0.0-20210514010506-3b9f47219fe7#readme-extensions)

	return string(markdown.ToHTML([]byte(markdownRaw), parser.NewWithExtensions(extensions), nil))
}

// ReplaceAbbreviations processes the given Paragraph to replace abbreviations.
func ReplaceAbbreviations(paragraph Paragraph, currentLanguageAbbreviations Abbreviations) Paragraph {
	processed := paragraph.Content
	for name, definition := range currentLanguageAbbreviations {
		var replacePattern = regexp.MustCompile(`\b` + name + `\b`)
		processed = HTMLString(replacePattern.ReplaceAllString(string(paragraph.Content), "<abbr title=\""+definition+"\">"+name+"</abbr>"))
	}

	return Paragraph{Content: processed}
}

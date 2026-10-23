package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	clientAnnouncementsOption         = "console_setting.announcements"
	clientAnnouncementsEnabledOption  = "console_setting.announcements_enabled"
	clientAnnouncementsMetadataOption = "ClientAnnouncementsMetadata"
	clientAnnouncementsMaxBytes       = 1024 * 1024
)

var (
	ErrClientAnnouncementsUnavailable      = common.NewMessage("Client announcements are temporarily unavailable")
	errClientAnnouncementsMetadataReadOnly = common.NewMessage("Client announcement timestamps are managed by the server")
)

// ClientAnnouncement contains only display data. Even legacy HTML is returned
// as inert text; unrecognized stored fields are never exposed to clients.
type ClientAnnouncement struct {
	ID          int64   `json:"id"`
	Title       string  `json:"title"`
	Content     string  `json:"content"`
	PublishDate string  `json:"publishDate"`
	UpdatedAt   *string `json:"updatedAt"`
	Type        string  `json:"type"`
	Extra       string  `json:"extra"`
}

type ClientAnnouncementsFeed struct {
	SchemaVersion        int                  `json:"schema_version"`
	ContentFormat        string               `json:"content_format"`
	AnnouncementsEnabled bool                 `json:"announcements_enabled"`
	Announcements        []ClientAnnouncement `json:"announcements"`
	UpdatedAt            *string              `json:"updated_at"`
}

type clientAnnouncementRevision struct {
	Fingerprint string  `json:"fingerprint"`
	UpdatedAt   *string `json:"updated_at"`
}

type clientAnnouncementsMetadata struct {
	SchemaVersion     int                                  `json:"schema_version"`
	ConfigFingerprint string                               `json:"config_fingerprint"`
	Entries           map[int64]clientAnnouncementRevision `json:"entries"`
	UpdatedAt         *string                              `json:"updated_at"`
}

func isClientAnnouncementOption(key string) bool {
	return key == clientAnnouncementsOption || key == clientAnnouncementsEnabledOption
}

func isClientAnnouncementsMetadataOption(key string) bool {
	return strings.EqualFold(strings.TrimSpace(key), clientAnnouncementsMetadataOption)
}

func clientAnnouncementsFingerprint(raw string, enabled bool) string {
	sum := sha256.Sum256([]byte(strconv.FormatBool(enabled) + "\n" + raw))
	return hex.EncodeToString(sum[:])
}

func clientAnnouncementFingerprint(entry ClientAnnouncement) string {
	entry.UpdatedAt = nil
	encoded, _ := common.Marshal(entry)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// Missing/duplicate legacy IDs remain readable. Their deterministic fallback
// is only an identity for this feed; the stored administrator JSON is unchanged.
func decodeClientAnnouncements(raw string) ([]ClientAnnouncement, error) {
	list := []ClientAnnouncement{}
	if strings.TrimSpace(raw) == "" {
		return list, nil
	}
	if len(raw) > clientAnnouncementsMaxBytes {
		return nil, ErrClientAnnouncementsUnavailable
	}
	if err := console_setting.ValidateConsoleSettings(raw, "Announcements"); err != nil {
		return nil, ErrClientAnnouncementsUnavailable
	}
	var rows []map[string]any
	if err := common.UnmarshalJsonStr(raw, &rows); err != nil {
		return nil, ErrClientAnnouncementsUnavailable
	}
	used := make(map[int64]bool, len(rows))
	for _, row := range rows {
		if id, ok := row["id"].(float64); ok && id > 0 && id <= 9007199254740991 && id == float64(int64(id)) {
			used[int64(id)] = true
		}
	}
	assigned := make(map[int64]bool, len(rows))
	var nextID int64 = 1
	for _, row := range rows {
		var id int64
		if number, ok := row["id"].(float64); ok && number > 0 && number <= 9007199254740991 && number == float64(int64(number)) {
			id = int64(number)
		}
		if id == 0 || assigned[id] {
			for used[nextID] {
				nextID++
			}
			id = nextID
			used[id] = true
		}
		assigned[id] = true
		title, _ := row["title"].(string)
		content, _ := row["content"].(string)
		publishDate, _ := row["publishDate"].(string)
		kind, _ := row["type"].(string)
		if kind == "" {
			kind = "default"
		}
		extra, _ := row["extra"].(string)
		list = append(list, ClientAnnouncement{ID: id, Title: title, Content: content, PublishDate: publishDate, Type: kind, Extra: extra})
	}
	return list, nil
}

func trustedClientAnnouncementsMetadata(raw, value string, enabled bool) clientAnnouncementsMetadata {
	var metadata clientAnnouncementsMetadata
	if len(value) > clientAnnouncementsMaxBytes || common.UnmarshalJsonStr(value, &metadata) != nil || metadata.SchemaVersion != 1 || metadata.ConfigFingerprint != clientAnnouncementsFingerprint(raw, enabled) {
		return clientAnnouncementsMetadata{}
	}
	return metadata
}

func validClientAnnouncementTime(value *string) *string {
	if value == nil {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, *value)
	if err != nil {
		return nil
	}
	utc := parsed.UTC().Format(time.RFC3339Nano)
	return &utc
}

// ProjectClientAnnouncementsForAdmin preserves the administrator's JSON while
// replacing any submitted timestamps with trusted server metadata, including
// when the public feed is disabled. An old in-memory option snapshot can only
// receive timestamps if its exact content and enable flag match that metadata.
func ProjectClientAnnouncementsForAdmin(ctx context.Context, raw string, enabled bool) string {
	if strings.TrimSpace(raw) == "" || len(raw) > clientAnnouncementsMaxBytes {
		return raw
	}
	var rows []map[string]any
	if common.UnmarshalJsonStr(raw, &rows) != nil || len(rows) > 100 {
		return raw
	}
	var metadata clientAnnouncementsMetadata
	if DB != nil {
		var options []Option
		if DB.WithContext(ctx).Where(clause.IN{Column: clause.Column{Name: "key"}, Values: []any{clientAnnouncementsOption, clientAnnouncementsEnabledOption, clientAnnouncementsMetadataOption}}).Find(&options).Error == nil {
			databaseRaw, databaseEnabled, metadataRaw, valid := "", true, "", true
			for _, option := range options {
				switch option.Key {
				case clientAnnouncementsOption:
					databaseRaw = option.Value
				case clientAnnouncementsEnabledOption:
					var err error
					databaseEnabled, err = strconv.ParseBool(option.Value)
					valid = valid && err == nil
				case clientAnnouncementsMetadataOption:
					metadataRaw = option.Value
				default:
					valid = false
				}
			}
			if valid && databaseRaw == raw && databaseEnabled == enabled {
				metadata = trustedClientAnnouncementsMetadata(raw, metadataRaw, enabled)
			}
		}
	}
	entries, err := decodeClientAnnouncements(raw)
	for i, row := range rows {
		if row == nil {
			continue
		}
		row["updatedAt"] = nil
		if err != nil {
			continue
		}
		entry := entries[i]
		row["id"] = entry.ID
		if revision := metadata.Entries[entry.ID]; revision.Fingerprint == clientAnnouncementFingerprint(entry) {
			row["updatedAt"] = validClientAnnouncementTime(revision.UpdatedAt)
		}
	}
	encoded, err := common.Marshal(rows)
	if err != nil {
		return raw
	}
	return string(encoded)
}

// updateClientAnnouncementsMetadata runs inside the caller's existing option
// transaction. The first statement writes the guard row before any reads, so
// SQLite cannot upgrade a stale read snapshot; the same row serializes writers
// on MySQL/PostgreSQL. Content, enable flag and metadata commit or roll back
// together. No process-local state or client timestamp is authoritative.
func updateClientAnnouncementsMetadata(tx *gorm.DB, values map[string]string) error {
	if _, changed := values[clientAnnouncementsOption]; !changed {
		if _, changed = values[clientAnnouncementsEnabledOption]; !changed {
			return nil
		}
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&Option{Key: clientAnnouncementsMetadataOption, Value: "{}"}).Error; err != nil {
		return err
	}
	if err := tx.Model(&Option{}).Where(&Option{Key: clientAnnouncementsMetadataOption}).UpdateColumn("value", gorm.Expr("value")).Error; err != nil {
		return err
	}
	var guard Option
	if err := lockForUpdate(tx).Where(&Option{Key: clientAnnouncementsMetadataOption}).Take(&guard).Error; err != nil {
		return err
	}
	if guard.Key != clientAnnouncementsMetadataOption {
		return ErrClientAnnouncementsUnavailable
	}
	var options []Option
	if err := lockForUpdate(tx).Where(clause.IN{Column: clause.Column{Name: "key"}, Values: []any{clientAnnouncementsOption, clientAnnouncementsEnabledOption}}).Find(&options).Error; err != nil {
		return err
	}
	oldRaw, oldEnabled := "", true
	oldEnabledValid := true
	for _, option := range options {
		switch option.Key {
		case clientAnnouncementsOption:
			oldRaw = option.Value
		case clientAnnouncementsEnabledOption:
			var err error
			oldEnabled, err = strconv.ParseBool(option.Value)
			if err != nil {
				if _, repairing := values[clientAnnouncementsEnabledOption]; !repairing {
					return ErrClientAnnouncementsUnavailable
				}
				oldEnabled, oldEnabledValid = true, false
			}
		default:
			return ErrClientAnnouncementsUnavailable
		}
	}
	newRaw, newEnabled := oldRaw, oldEnabled
	if value, changed := values[clientAnnouncementsOption]; changed {
		newRaw = value
	}
	if value, changed := values[clientAnnouncementsEnabledOption]; changed {
		var err error
		newEnabled, err = strconv.ParseBool(value)
		if err != nil {
			return ErrClientAnnouncementsUnavailable
		}
	}
	oldEntries, _ := decodeClientAnnouncements(oldRaw)
	newEntries, err := decodeClientAnnouncements(newRaw)
	if err != nil {
		// Turning off a damaged legacy configuration remains possible.
		if newEnabled || newRaw != oldRaw {
			return err
		}
	}
	previous := trustedClientAnnouncementsMetadata(oldRaw, guard.Value, oldEnabled)
	if !oldEnabledValid {
		previous = clientAnnouncementsMetadata{}
	}
	oldFingerprints := make(map[int64]string, len(oldEntries))
	for _, entry := range oldEntries {
		oldFingerprints[entry.ID] = clientAnnouncementFingerprint(entry)
	}
	metadata := clientAnnouncementsMetadata{SchemaVersion: 1, ConfigFingerprint: clientAnnouncementsFingerprint(newRaw, newEnabled), Entries: make(map[int64]clientAnnouncementRevision, len(newEntries)), UpdatedAt: validClientAnnouncementTime(previous.UpdatedAt)}
	changed := !oldEnabledValid || oldEnabled != newEnabled || len(oldEntries) != len(newEntries)
	if !changed {
		for i, entry := range newEntries {
			if clientAnnouncementFingerprint(oldEntries[i]) != clientAnnouncementFingerprint(entry) {
				changed = true
				break
			}
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, entry := range newEntries {
		fingerprint := clientAnnouncementFingerprint(entry)
		var updatedAt *string
		if oldFingerprints[entry.ID] != fingerprint {
			updatedAt = &now
			changed = true
		} else if revision := previous.Entries[entry.ID]; revision.Fingerprint == fingerprint {
			updatedAt = validClientAnnouncementTime(revision.UpdatedAt)
		}
		metadata.Entries[entry.ID] = clientAnnouncementRevision{Fingerprint: fingerprint, UpdatedAt: updatedAt}
	}
	if changed {
		metadata.UpdatedAt = &now
	}
	encoded, err := common.Marshal(metadata)
	if err != nil {
		return err
	}
	return tx.Model(&Option{}).Where(&Option{Key: clientAnnouncementsMetadataOption}).Update("value", string(encoded)).Error
}

// GetClientAnnouncements uses one database statement for a consistent snapshot
// and never creates options or invents update times for historical content.
func GetClientAnnouncements(ctx context.Context) (ClientAnnouncementsFeed, error) {
	feed := ClientAnnouncementsFeed{SchemaVersion: 1, ContentFormat: "plain_text", AnnouncementsEnabled: true, Announcements: []ClientAnnouncement{}}
	if DB == nil {
		return feed, ErrClientAnnouncementsUnavailable
	}
	var options []Option
	if err := DB.WithContext(ctx).Where(clause.IN{Column: clause.Column{Name: "key"}, Values: []any{clientAnnouncementsOption, clientAnnouncementsEnabledOption, clientAnnouncementsMetadataOption}}).Find(&options).Error; err != nil {
		return feed, ErrClientAnnouncementsUnavailable
	}
	var raw, metadataRaw string
	for _, option := range options {
		switch option.Key {
		case clientAnnouncementsOption:
			raw = option.Value
		case clientAnnouncementsEnabledOption:
			var err error
			feed.AnnouncementsEnabled, err = strconv.ParseBool(option.Value)
			if err != nil {
				return feed, ErrClientAnnouncementsUnavailable
			}
		case clientAnnouncementsMetadataOption:
			metadataRaw = option.Value
		default:
			return feed, ErrClientAnnouncementsUnavailable
		}
	}
	metadata := trustedClientAnnouncementsMetadata(raw, metadataRaw, feed.AnnouncementsEnabled)
	feed.UpdatedAt = validClientAnnouncementTime(metadata.UpdatedAt)
	if !feed.AnnouncementsEnabled {
		return feed, nil
	}
	entries, err := decodeClientAnnouncements(raw)
	if err != nil {
		return feed, err
	}
	for _, entry := range entries {
		if revision := metadata.Entries[entry.ID]; revision.Fingerprint == clientAnnouncementFingerprint(entry) {
			entry.UpdatedAt = validClientAnnouncementTime(revision.UpdatedAt)
		}
		entry.Title = clientAnnouncementPlainText(entry.Title)
		entry.Content = clientAnnouncementPlainText(entry.Content)
		entry.Extra = clientAnnouncementPlainText(entry.Extra)
		if entry.Title == "" {
			preview := strings.Join(strings.Fields(entry.Content), " ")
			var title strings.Builder
			count := 0
			for _, r := range preview {
				width := utf16.RuneLen(r)
				if count+width > 120 {
					break
				}
				title.WriteRune(r)
				count += width
			}
			entry.Title = title.String()
		}
		feed.Announcements = append(feed.Announcements, entry)
	}
	sort.SliceStable(feed.Announcements, func(i, j int) bool {
		left, _ := time.Parse(time.RFC3339, feed.Announcements[i].PublishDate)
		right, _ := time.Parse(time.RFC3339, feed.Announcements[j].PublishDate)
		return left.After(right)
	})
	return feed, nil
}

// HTML parsing discards attributes, comments and entire active/embedded
// subtrees. Only text nodes and paragraph boundaries can enter the response.
func clientAnnouncementPlainText(value string) string {
	nodes, err := html.ParseFragment(strings.NewReader(value), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return ""
	}
	var text strings.Builder
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "script", "style", "iframe", "object", "embed", "svg", "math", "template", "noscript", "audio", "video", "canvas", "frame", "frameset", "head", "title", "link", "meta", "input", "button", "select", "textarea", "applet", "base", "form":
				return
			case "br", "p", "div", "li", "ul", "ol", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre", "hr", "tr":
				text.WriteByte('\n')
			}
		}
		if node.Type == html.TextNode {
			text.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
		if node.Type == html.ElementNode {
			switch node.Data {
			case "p", "div", "li", "ul", "ol", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre", "tr":
				text.WriteByte('\n')
			}
		}
	}
	for _, node := range nodes {
		visit(node)
	}
	clean := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, strings.ReplaceAll(text.String(), "\r\n", "\n"))
	return strings.TrimSpace(clean)
}

// AdminAnnouncementsConfig is a narrow view of the existing announcement
// options. It cannot carry unrelated options or server-owned timestamp metadata.
type AdminAnnouncementsConfig struct {
	Announcements string `json:"console_setting.announcements"`
	Enabled       string `json:"console_setting.announcements_enabled"`
}

const MaxAdminAnnouncementsBodyBytes = 4 * 1024 * 1024

func ParseAdminAnnouncementsConfig(data []byte) (AdminAnnouncementsConfig, error) {
	invalid := AdminAnnouncementsConfig{}
	if len(data) == 0 || len(data) > MaxAdminAnnouncementsBodyBytes || !utf8.Valid(data) {
		return invalid, common.NewMessage("Invalid parameters")
	}
	var object map[string]common.RawMessage
	if common.Unmarshal(data, &object) != nil || len(object) != 2 {
		return invalid, common.NewMessage("Invalid parameters")
	}
	for key := range object {
		if key != clientAnnouncementsOption && key != clientAnnouncementsEnabledOption {
			return invalid, common.NewMessage("Invalid parameters")
		}
	}
	var wire struct {
		Announcements clientModelConfigField `json:"console_setting.announcements"`
		Enabled       clientModelConfigField `json:"console_setting.announcements_enabled"`
	}
	if common.Unmarshal(data, &wire) != nil || !wire.Announcements.seen || !wire.Enabled.seen {
		return invalid, common.NewMessage("Invalid parameters")
	}
	var config AdminAnnouncementsConfig
	if common.Unmarshal(wire.Announcements.raw, &config.Announcements) != nil || common.Unmarshal(wire.Enabled.raw, &config.Enabled) != nil ||
		len(config.Announcements) > clientAnnouncementsMaxBytes || (config.Enabled != "true" && config.Enabled != "false") {
		return invalid, common.NewMessage("Invalid parameters")
	}
	if err := console_setting.ValidateConsoleSettings(config.Announcements, "Announcements"); err != nil {
		return invalid, err
	}
	return config, nil
}

func GetAdminAnnouncementsConfig(ctx context.Context) (AdminAnnouncementsConfig, error) {
	config := AdminAnnouncementsConfig{Announcements: "[]", Enabled: "true"}
	if DB == nil {
		return config, ErrClientAnnouncementsUnavailable
	}
	var options []Option
	if err := DB.WithContext(ctx).Where(clause.IN{Column: clause.Column{Name: "key"}, Values: []any{clientAnnouncementsOption, clientAnnouncementsEnabledOption}}).Find(&options).Error; err != nil {
		return config, ErrClientAnnouncementsUnavailable
	}
	for _, option := range options {
		switch option.Key {
		case clientAnnouncementsOption:
			config.Announcements = option.Value
		case clientAnnouncementsEnabledOption:
			enabled, err := strconv.ParseBool(option.Value)
			if err != nil {
				return config, ErrClientAnnouncementsUnavailable
			}
			config.Enabled = strconv.FormatBool(enabled)
		default:
			return config, ErrClientAnnouncementsUnavailable
		}
	}
	config.Announcements = ProjectClientAnnouncementsForAdmin(ctx, config.Announcements, config.Enabled == "true")
	return config, nil
}

func SaveAdminAnnouncementsConfig(config AdminAnnouncementsConfig) error {
	return UpdateOptionsBulk(map[string]string{clientAnnouncementsOption: config.Announcements, clientAnnouncementsEnabledOption: config.Enabled})
}

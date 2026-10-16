package system_setting

import (
	"errors"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/setting/config"
)

const DesktopUpdatePrefix = "desktop_update."

type DesktopUpdateSettings struct {
	Enabled     bool   `json:"enabled"`
	MacARM64URL string `json:"mac_arm64_url"`
	MacX64URL   string `json:"mac_x64_url"`
	WinX64URL   string `json:"win_x64_url"`
}

type DesktopUpdateStatus struct {
	Enabled bool              `json:"enabled"`
	Channel string            `json:"channel"`
	Feeds   map[string]string `json:"feeds"`
}

var desktopUpdateSettings DesktopUpdateSettings
var desktopUpdateEncodedUnsafeCharacters = regexp.MustCompile(`(?i)%(?:0[0-9a-f]|1[0-9a-f]|7f|5c|2f)`)

func init() {
	config.GlobalConfig.Register("desktop_update", &desktopUpdateSettings)
}

// ValidateDesktopUpdateOption validates only this namespace and never fetches a URL.
func ValidateDesktopUpdateOption(key, value string) error {
	switch key {
	case DesktopUpdatePrefix + "enabled":
		if value != "true" && value != "false" {
			return errors.New("Invalid desktop update enabled value")
		}
	case DesktopUpdatePrefix + "mac_arm64_url", DesktopUpdatePrefix + "mac_x64_url", DesktopUpdatePrefix + "win_x64_url":
		return ValidateDesktopUpdateFeedURL(value)
	}
	return nil
}

func ValidateDesktopUpdateFeedURL(value string) error {
	if value == "" {
		return nil
	}
	invalid := errors.New("Invalid desktop update feed URL")
	if len(value) > 2048 || strings.ContainsAny(value, "\\?#") || strings.ContainsFunc(value, unicode.IsSpace) || strings.ContainsFunc(value, unicode.IsControl) || desktopUpdateEncodedUnsafeCharacters.MatchString(value) {
		return invalid
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.Opaque != "" || parsed.Hostname() == "" {
		return invalid
	}
	if strings.ContainsAny(parsed.Path, "\\") || strings.ContainsFunc(parsed.Path, unicode.IsControl) {
		return invalid
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return invalid
		}
	}
	if parsed.Scheme != "https" {
		if parsed.Scheme != "http" {
			return invalid
		}
		host := strings.ToLower(parsed.Hostname())
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			return invalid
		}
	}
	// A feed is a directory containing updater metadata and artifacts.
	switch strings.ToLower(path.Ext(strings.TrimRight(parsed.Path, "/"))) {
	case ".exe", ".dmg", ".zip", ".yml", ".yaml", ".blockmap", ".json":
		return invalid
	}
	return nil
}

// GetDesktopUpdateStatus is called while common.OptionMapRWMutex is held.
func GetDesktopUpdateStatus() DesktopUpdateStatus {
	feeds := map[string]string{
		"mac-arm64": desktopUpdateSettings.MacARM64URL,
		"mac-x64":   desktopUpdateSettings.MacX64URL,
		"win-x64":   desktopUpdateSettings.WinX64URL,
	}
	for platform, feed := range feeds {
		// Old or manually edited database values must not expose URL credentials.
		if ValidateDesktopUpdateFeedURL(feed) != nil {
			feeds[platform] = ""
		}
	}
	return DesktopUpdateStatus{Enabled: desktopUpdateSettings.Enabled, Channel: "nightly", Feeds: feeds}
}

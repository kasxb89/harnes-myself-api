package model

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/creationcontent"
	_ "golang.org/x/image/webp"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	keyCustomerServiceConfig           = "CustomerServiceConfig"
	MaxCustomerServiceBodyBytes        = 16 * 1024
	MaxCustomerServiceImagePixels      = 16777216
	MaxCustomerServiceTitleUnits       = 64
	MaxCustomerServiceDescriptionUnits = 256
	MaxCustomerServiceURLBytes         = 2048
)

var (
	ErrInvalidCustomerService      = errors.New("Invalid customer service configuration")
	ErrCustomerServiceImage        = errors.New("Customer service QR code must be a verified uploaded PNG, JPEG or WebP image")
	ErrCustomerServiceSave         = errors.New("Failed to save customer service configuration")
	errCustomerServiceDedicatedAPI = errors.New("Use the customer service settings page to change this setting")
	customerServiceAssetPath       = regexp.MustCompile(`^/api/creation/assets/(cc_[a-f0-9]{64})$`)
)

// CustomerServiceConfig is independent of the QR code used for WeChat login.
// Text is inert display data. Images are immutable, locally verified assets.
type CustomerServiceConfig struct {
	SchemaVersion int    `json:"schema_version"`
	Enabled       bool   `json:"enabled"`
	QRCodeURL     string `json:"qr_code_url"`
	Title         string `json:"title"`
	Description   string `json:"description"`
}

func ParseCustomerServiceConfig(data []byte) (CustomerServiceConfig, error) {
	invalid := CustomerServiceConfig{}
	if len(data) == 0 || len(data) > MaxCustomerServiceBodyBytes || !utf8.Valid(data) {
		return invalid, ErrInvalidCustomerService
	}
	var object map[string]common.RawMessage
	if common.Unmarshal(data, &object) != nil || len(object) != 5 {
		return invalid, ErrInvalidCustomerService
	}
	for key := range object {
		switch key {
		case "schema_version", "enabled", "qr_code_url", "title", "description":
		default:
			return invalid, ErrInvalidCustomerService
		}
	}
	// The field wrapper rejects duplicate properties (including escaped names)
	// and null before any value is decoded through the shared JSON codec.
	var wire struct {
		SchemaVersion clientModelConfigField `json:"schema_version"`
		Enabled       clientModelConfigField `json:"enabled"`
		QRCodeURL     clientModelConfigField `json:"qr_code_url"`
		Title         clientModelConfigField `json:"title"`
		Description   clientModelConfigField `json:"description"`
	}
	if common.Unmarshal(data, &wire) != nil || !wire.SchemaVersion.seen || !wire.Enabled.seen || !wire.QRCodeURL.seen || !wire.Title.seen || !wire.Description.seen {
		return invalid, ErrInvalidCustomerService
	}
	var config CustomerServiceConfig
	if common.Unmarshal(wire.SchemaVersion.raw, &config.SchemaVersion) != nil || config.SchemaVersion != 1 ||
		common.Unmarshal(wire.Enabled.raw, &config.Enabled) != nil ||
		common.Unmarshal(wire.QRCodeURL.raw, &config.QRCodeURL) != nil ||
		common.Unmarshal(wire.Title.raw, &config.Title) != nil ||
		common.Unmarshal(wire.Description.raw, &config.Description) != nil {
		return invalid, ErrInvalidCustomerService
	}
	if len(utf16.Encode([]rune(config.Title))) > MaxCustomerServiceTitleUnits ||
		len(utf16.Encode([]rune(config.Description))) > MaxCustomerServiceDescriptionUnits ||
		strings.ContainsFunc(config.Title, unicode.IsControl) || strings.ContainsFunc(config.Description, unicode.IsControl) ||
		len(config.QRCodeURL) > MaxCustomerServiceURLBytes ||
		(config.QRCodeURL != "" && !customerServiceAssetPath.MatchString(config.QRCodeURL)) ||
		(config.Enabled && config.QRCodeURL == "") {
		return invalid, ErrInvalidCustomerService
	}
	return config, nil
}

func CurrentCustomerServiceConfig() CustomerServiceConfig {
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[keyCustomerServiceConfig]
	common.OptionMapRWMutex.RUnlock()
	if config, err := ParseCustomerServiceConfig([]byte(raw)); err == nil {
		return config
	}
	return CustomerServiceConfig{SchemaVersion: 1}
}

// PublicCustomerService does not publish an administrator's disabled draft.
func PublicCustomerService() CustomerServiceConfig {
	config := CurrentCustomerServiceConfig()
	if !config.Enabled {
		return CustomerServiceConfig{SchemaVersion: 1}
	}
	return config
}

// verifyCustomerServiceImage verifies the bytes, rather than trusting a suffix,
// a submitted MIME type, or the creation store's lightweight serving metadata.
func verifyCustomerServiceImage(path string) error {
	if path == "" {
		return nil
	}
	match := customerServiceAssetPath.FindStringSubmatch(path)
	if len(match) != 2 {
		return ErrCustomerServiceImage
	}
	asset, file, err := creationcontent.Default().OpenAsset(match[1])
	if err != nil {
		return ErrCustomerServiceImage
	}
	defer file.Close()
	if asset.Size <= 0 || asset.Size > creationcontent.MaxImageSize ||
		(asset.MediaType != "image/png" && asset.MediaType != "image/jpeg" && asset.MediaType != "image/webp") {
		return ErrCustomerServiceImage
	}
	data, err := io.ReadAll(io.LimitReader(file, creationcontent.MaxImageSize+1))
	if err != nil || int64(len(data)) != asset.Size || int64(len(data)) > creationcontent.MaxImageSize || fmt.Sprintf("cc_%x", sha256.Sum256(data)) != match[1] {
		return ErrCustomerServiceImage
	}
	_, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || asset.MediaType != "image/"+format {
		return ErrCustomerServiceImage
	}
	if err := validateCustomerServiceImageData(data); err != nil {
		return err
	}
	return nil
}

func validateCustomerServiceImageData(data []byte) error {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > MaxCustomerServiceImagePixels || config.Height > MaxCustomerServiceImagePixels/config.Width {
		return ErrCustomerServiceImage
	}
	if format != "png" && format != "jpeg" && format != "webp" {
		return ErrCustomerServiceImage
	}
	decoded, decodedFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil || decodedFormat != format || decoded.Bounds().Dx() != config.Width || decoded.Bounds().Dy() != config.Height {
		return ErrCustomerServiceImage
	}
	return nil
}

func SaveCustomerServiceConfig(config CustomerServiceConfig) error {
	data, err := common.Marshal(config)
	if err != nil {
		return ErrInvalidCustomerService
	}
	if _, err := ParseCustomerServiceConfig(data); err != nil {
		return err
	}
	if err := verifyCustomerServiceImage(config.QRCodeURL); err != nil {
		return err
	}
	// The periodic options snapshot uses the same lock, so it cannot publish
	// pre-commit data after this complete object has been committed and cached.
	clientModelConfigMutex.Lock()
	defer clientModelConfigMutex.Unlock()
	if DB == nil {
		return ErrCustomerServiceSave
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"}),
		}).Create(&Option{Key: keyCustomerServiceConfig, Value: string(data)}).Error; err != nil {
			return err
		}
		var stored Option
		if err := tx.Select("key").Where(&Option{Key: keyCustomerServiceConfig}).Take(&stored).Error; err != nil {
			return err
		}
		if stored.Key != keyCustomerServiceConfig {
			return ErrCustomerServiceSave
		}
		return nil
	}); err != nil {
		return ErrCustomerServiceSave
	}
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMap[keyCustomerServiceConfig] = string(data)
	common.OptionMapRWMutex.Unlock()
	return nil
}

func isCustomerServiceOption(key string) bool {
	return strings.EqualFold(strings.TrimSpace(key), keyCustomerServiceConfig)
}

// Check the database's collation after the caller's tentative insert. This
// catches aliases that EqualFold cannot, and the rejected insert rolls back.
func validateCustomerServiceOptionWrite(tx *gorm.DB, storedKey string) error {
	var count int64
	if err := tx.Model(&Option{}).Where(&Option{Key: storedKey}).Where(&Option{Key: keyCustomerServiceConfig}).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return errCustomerServiceDedicatedAPI
	}
	return nil
}

// UploadCustomerServiceImage shares the immutable creation asset store while
// admitting only fully decoded, bounded QR image formats before writing a file.
func UploadCustomerServiceImage(reader io.Reader) (creationcontent.Asset, error) {
	data, err := io.ReadAll(io.LimitReader(reader, creationcontent.MaxImageSize+1))
	if err != nil || len(data) == 0 || int64(len(data)) > creationcontent.MaxImageSize {
		return creationcontent.Asset{}, ErrCustomerServiceImage
	}
	if err := validateCustomerServiceImageData(data); err != nil {
		return creationcontent.Asset{}, err
	}
	return creationcontent.Default().Upload(bytes.NewReader(data))
}

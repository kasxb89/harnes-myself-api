package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

const SurveyDynamicSchema = 2

var ErrSurveyConfigChanged = errors.New("survey configuration changed")
var ErrSurveyUnsupported = errors.New("survey protocol unsupported")
var surveyOptionID = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)

type SurveyConfigOption struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	LabelEN string `json:"label_en"`
	Enabled bool   `json:"enabled"`
}
type SurveyConfiguration struct {
	SchemaVersion int                  `json:"schema_version"`
	SurveyID      string               `json:"survey_id"`
	Revision      string               `json:"config_revision"`
	Options       []SurveyConfigOption `json:"options"`
}

// Versions are immutable. Answers additionally retain their exact public options
// snapshot, so changing the current labels never reinterprets historical data.
type OnboardingSurveyConfigVersion struct {
	Revision    string            `gorm:"type:varchar(64);primaryKey"`
	OptionsJSON SurveyOptionsJSON `gorm:"not null"`
	CreatedAt   int64             `gorm:"not null"`
}
type OnboardingSurveyConfigState struct {
	ID       int    `gorm:"primaryKey;autoIncrement:false"`
	Revision string `gorm:"type:varchar(64);not null"`
}

func SurveyConfigurationModels() []any {
	return []any{&OnboardingSurveyConfigVersion{}, &OnboardingSurveyConfigState{}}
}
func LegacySurveyConfiguration() SurveyConfiguration {
	options := make([]SurveyConfigOption, 0, 9)
	for _, o := range OnboardingSurveyOptions() {
		options = append(options, SurveyConfigOption{o.ID, o.Label, o.LabelEN, true})
	}
	cfg, _ := normalizeSurveyConfiguration(options)
	return cfg
}
func DefaultSurveyConfiguration() SurveyConfiguration {
	options := []SurveyConfigOption{
		{"daily_qa", "日常问答", "Daily questions", true},
		{"brainstorming", "创意脑暴", "Brainstorming", true},
		{"translation", "翻译与润色", "Translation and polishing", true},
		{"document_writing", "文档与报告", "Documents and reports", true},
		{"spreadsheet_analysis", "表格与数据分析", "Spreadsheets and data analysis", true},
		{"presentations", "演示文稿制作", "Presentations", true},
		{"copywriting", "文案写作", "Copywriting", true},
		{"social_content", "自媒体内容", "Social media content", true},
		{"code_generation", "代码生成", "Code generation", true},
		{"code_debugging", "代码排错", "Code debugging", true},
		{"web_development", "网站与应用开发", "Web and app development", true},
		{"study_tutoring", "学习辅导", "Study tutoring", true},
		{"research_summary", "资料检索与总结", "Research and summaries", true},
		{"image_generation", "图片生成", "Image generation", true},
		{"image_editing", "图片修改与修复", "Image editing and restoration", true},
		{"product_images", "商品图片", "Product images", true},
		{"graphic_design", "海报与平面设计", "Posters and graphic design", true},
		{"video_generation", "视频生成", "Video generation", true},
		{"storyboard_planning", "分镜规划", "Storyboard planning", true},
		{"video_script", "视频脚本", "Video scripts", true},
		{"ecommerce_listing", "商品上架与详情", "Product listings and details", true},
		{"marketing_campaign", "营销推广", "Marketing campaigns", true},
		{"customer_support", "客户服务", "Customer support", true},
		{"workflow_automation", "工作流程自动化", "Workflow automation", true},
		{"data_processing", "批量数据处理", "Batch data processing", true},
		{"other", "其他", "Other", true},
	}
	for _, o := range OnboardingSurveyOptions() {
		if o.ID != "daily_qa" && o.ID != "other" {
			options = append(options, SurveyConfigOption{o.ID, o.Label, o.LabelEN, false})
		}
	}
	cfg, _ := normalizeSurveyConfiguration(options)
	return cfg
}
func validSurveyLabel(value string, limit int) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > limit {
		return false
	}
	for _, ch := range value {
		if unicode.IsControl(ch) {
			return false
		}
	}
	return true
}
func normalizeSurveyConfiguration(options []SurveyConfigOption) (SurveyConfiguration, error) {
	cfg := SurveyConfiguration{SchemaVersion: 2, SurveyID: OnboardingSurveyID, Options: make([]SurveyConfigOption, 0, len(options))}
	if len(options) < 1 || len(options) > 64 {
		return cfg, ErrSurveyInvalid
	}
	seen := map[string]bool{}
	active := 0
	for _, o := range options {
		o.Label = strings.TrimSpace(o.Label)
		o.LabelEN = strings.TrimSpace(o.LabelEN)
		if !surveyOptionID.MatchString(o.ID) || seen[o.ID] || !validSurveyLabel(o.Label, 80) || !validSurveyLabel(o.LabelEN, 160) {
			return cfg, ErrSurveyInvalid
		}
		seen[o.ID] = true
		if o.Enabled {
			active++
		}
		cfg.Options = append(cfg.Options, o)
	}
	if active == 0 {
		return cfg, ErrSurveyInvalid
	}
	encoded, err := common.Marshal(cfg.Options)
	if err != nil {
		return cfg, err
	}
	cfg.Revision = fmt.Sprintf("%x", sha256.Sum256(encoded))
	return cfg, nil
}
func (cfg SurveyConfiguration) ActiveOptions() []SurveyOption {
	options := make([]SurveyOption, 0, len(cfg.Options))
	for _, o := range cfg.Options {
		if o.Enabled {
			options = append(options, SurveyOption{o.ID, o.Label, o.LabelEN})
		}
	}
	return options
}
func InitializeSurveyConfiguration() error {
	for _, cfg := range []SurveyConfiguration{LegacySurveyConfiguration(), DefaultSurveyConfiguration()} {
		encoded, err := common.Marshal(cfg.Options)
		if err != nil {
			return err
		}
		if err := surveyDB(DB).Clauses(clause.OnConflict{DoNothing: true}).Create(&OnboardingSurveyConfigVersion{Revision: cfg.Revision, OptionsJSON: SurveyOptionsJSON(encoded), CreatedAt: time.Now().Unix()}).Error; err != nil {
			return err
		}
	}
	return surveyDB(DB).Clauses(clause.OnConflict{DoNothing: true}).Create(&OnboardingSurveyConfigState{ID: 1, Revision: DefaultSurveyConfiguration().Revision}).Error
}
func readSurveyConfiguration(db *gorm.DB) (SurveyConfiguration, error) {
	var state OnboardingSurveyConfigState
	var version OnboardingSurveyConfigVersion
	if err := surveyDB(db).First(&state, 1).Error; err != nil {
		return SurveyConfiguration{}, err
	}
	if err := surveyDB(db).First(&version, "revision = ?", state.Revision).Error; err != nil {
		return SurveyConfiguration{}, err
	}
	var options []SurveyConfigOption
	if err := common.UnmarshalJsonStr(string(version.OptionsJSON), &options); err != nil {
		return SurveyConfiguration{}, err
	}
	cfg, err := normalizeSurveyConfiguration(options)
	if err == nil && cfg.Revision != state.Revision {
		return cfg, errors.New("invalid persisted survey configuration")
	}
	return cfg, err
}
func GetSurveyConfiguration() (SurveyConfiguration, error) { return readSurveyConfiguration(DB) }

// The first operation is a write to the singleton configuration guard. It
// serializes edits and submissions on SQLite, MySQL and PostgreSQL; no process
// mutex or stale read transaction can accept an answer against a retired view.
func guardSurveyConfiguration(tx *gorm.DB) error {
	r := surveyDB(tx).Model(&OnboardingSurveyConfigState{}).Where("id = ?", 1).Update("revision", gorm.Expr("revision"))
	if r.Error != nil {
		return r.Error
	}
	// MySQL may report zero for a no-op update; existence is checked by the read.
	return nil
}
func SaveSurveyConfiguration(expected string, options []SurveyConfigOption) (SurveyConfiguration, error) {
	cfg, err := normalizeSurveyConfiguration(options)
	if err != nil {
		return cfg, err
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := guardSurveyConfiguration(tx); err != nil {
			return err
		}
		current, err := readSurveyConfiguration(tx)
		if err != nil {
			return err
		}
		if expected != current.Revision {
			return ErrSurveyConfigChanged
		}
		ids := map[string]bool{}
		for _, o := range cfg.Options {
			ids[o.ID] = true
		}
		for _, o := range current.Options {
			if !ids[o.ID] {
				return ErrSurveyInvalid
			}
		}
		encoded, err := common.Marshal(cfg.Options)
		if err != nil {
			return err
		}
		if err := surveyDB(tx).Clauses(clause.OnConflict{DoNothing: true}).Create(&OnboardingSurveyConfigVersion{Revision: cfg.Revision, OptionsJSON: SurveyOptionsJSON(encoded), CreatedAt: time.Now().Unix()}).Error; err != nil {
			return err
		}
		return surveyDB(tx).Model(&OnboardingSurveyConfigState{}).Where("id = ?", 1).Update("revision", cfg.Revision).Error
	})
	return cfg, err
}

func ValidSurveyConfigRevision(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}

// Full option snapshots can exceed MySQL TEXT's 64KiB limit after JSON
// escaping. Preserve all allowed names with a native large text column, and
// ordinary TEXT on the other supported engines. No JSON driver codec is used.
type SurveyOptionsJSON string

func (SurveyOptionsJSON) GormDataType() string { return "string" }
func (SurveyOptionsJSON) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if db.Dialector.Name() == "mysql" {
		return "longtext"
	}
	return "text"
}

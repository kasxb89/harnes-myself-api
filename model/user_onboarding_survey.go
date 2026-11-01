package model

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	OnboardingSurveyID            = "product-use-v1"
	OnboardingSurveySchemaVersion = 1
	OnboardingSurveyMaxNotes      = 1000
	SurveyPending                 = "pending"
	SurveyDeferred                = "deferred"
	SurveySubmitted               = "submitted"
)

var (
	ErrSurveyInvalid          = errors.New("invalid survey request")
	ErrSurveyNotEligible      = errors.New("survey not eligible")
	ErrSurveyAlreadySubmitted = errors.New("survey already submitted")
)

type SurveyOption struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	LabelEN string `json:"label_en"`
}

func OnboardingSurveyOptions() []SurveyOption {
	return []SurveyOption{
		{"daily_qa", "日常问答", "Daily questions"},
		{"office_writing", "办公文档与内容写作", "Office documents and writing"},
		{"coding", "编程开发", "Software development"},
		{"study_research", "学习与资料研究", "Learning and research"},
		{"image_design", "图片创作与设计", "Image creation and design"},
		{"video_creation", "视频与分镜创作", "Video and storyboard creation"},
		{"ecommerce_marketing", "电商与营销推广", "E-commerce and marketing"},
		{"automation", "客服与工作自动化", "Customer support and work automation"},
		{"other", "其他", "Other"},
	}
}

// Enrollment is created only by the verified, genuinely new phone-account
// transaction. Migration and reads never backfill eligibility for older users.
// One primary key per account also makes re-login and challenge replay harmless.
type UserOnboardingSurvey struct {
	UserID              int    `gorm:"primaryKey;autoIncrement:false" json:"-"`
	SurveyID            string `gorm:"type:varchar(64);not null" json:"-"`
	PhoneE164           string `gorm:"type:varchar(32);not null" json:"-"`
	RegisteredAt        int64  `gorm:"not null;index:idx_survey_status_registered,priority:2" json:"-"`
	Status              string `gorm:"type:varchar(16);not null;index:idx_survey_status_registered,priority:1" json:"-"`
	SelectedOptionsJSON string `gorm:"type:text;not null" json:"-"`
	Notes               string `gorm:"type:text;not null" json:"-"`
	SubmittedAt         int64  `gorm:"not null" json:"-"`
	DeferredAt          int64  `gorm:"not null" json:"-"`
}

type OnboardingSurveyView struct {
	SchemaVersion     int            `json:"schema_version"`
	SurveyID          string         `json:"survey_id"`
	Eligible          bool           `json:"eligible"`
	Status            string         `json:"status"`
	Options           []SurveyOption `json:"options"`
	MaxSelections     int            `json:"max_selections"`
	MaxNotesLength    int            `json:"max_notes_length"`
	SelectedOptionIDs []string       `json:"selected_option_ids"`
	Notes             string         `json:"notes"`
	RegisteredAt      int64          `json:"registered_at"`
	SubmittedAt       int64          `json:"submitted_at"`
	DeferredAt        int64          `json:"deferred_at"`
}

// Questionnaire notes and registration phone numbers must not be interpolated
// into ORM error logs. Handlers expose a stable, non-sensitive failure code.
func surveyDB(db *gorm.DB) *gorm.DB {
	return db.Session(&gorm.Session{Logger: db.Logger.LogMode(logger.Silent)})
}

func EnrollPhoneOnboardingSurveyWithTx(tx *gorm.DB, user *User) error {
	if user.Id <= 0 || user.PhoneE164 == nil || *user.PhoneE164 == "" || user.CreatedAt <= 0 {
		return ErrSurveyInvalid
	}
	return surveyDB(tx).Create(&UserOnboardingSurvey{
		UserID: user.Id, SurveyID: OnboardingSurveyID, PhoneE164: *user.PhoneE164,
		RegisteredAt: user.CreatedAt, Status: SurveyPending, SelectedOptionsJSON: "[]",
	}).Error
}

func (s *UserOnboardingSurvey) view() (OnboardingSurveyView, error) {
	options := OnboardingSurveyOptions()
	view := OnboardingSurveyView{
		SchemaVersion: OnboardingSurveySchemaVersion, SurveyID: OnboardingSurveyID,
		Status: "not_eligible", Options: options, MaxSelections: len(options),
		MaxNotesLength: OnboardingSurveyMaxNotes, SelectedOptionIDs: []string{},
	}
	if s == nil {
		return view, nil
	}
	if s.SurveyID != OnboardingSurveyID || (s.Status != SurveyPending && s.Status != SurveyDeferred && s.Status != SurveySubmitted) {
		return view, errors.New("invalid persisted survey state")
	}
	if err := common.UnmarshalJsonStr(s.SelectedOptionsJSON, &view.SelectedOptionIDs); err != nil || view.SelectedOptionIDs == nil {
		return view, errors.New("invalid persisted survey answer")
	}
	view.Eligible, view.Status, view.Notes = true, s.Status, s.Notes
	view.RegisteredAt, view.SubmittedAt, view.DeferredAt = s.RegisteredAt, s.SubmittedAt, s.DeferredAt
	return view, nil
}

func loadOnboardingSurvey(userID int) (*UserOnboardingSurvey, error) {
	var row UserOnboardingSurvey
	err := surveyDB(DB).Where("user_id = ?", userID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &row, err
}

func GetOnboardingSurvey(userID int) (OnboardingSurveyView, error) {
	row, err := loadOnboardingSurvey(userID)
	if err != nil {
		return OnboardingSurveyView{}, err
	}
	return row.view()
}

func canonicalSurveyAnswer(selected []string, notes string) (string, string, error) {
	options := OnboardingSurveyOptions()
	if len(selected) < 1 || len(selected) > len(options) || !utf8.ValidString(notes) || utf8.RuneCountInString(notes) > OnboardingSurveyMaxNotes {
		return "", "", ErrSurveyInvalid
	}
	for _, ch := range notes {
		if unicode.IsControl(ch) && ch != '\n' && ch != '\r' && ch != '\t' {
			return "", "", ErrSurveyInvalid
		}
	}
	seen := make(map[string]bool, len(selected))
	for _, id := range selected {
		if seen[id] {
			return "", "", ErrSurveyInvalid
		}
		seen[id] = true
	}
	canonical := make([]string, 0, len(selected))
	for _, option := range options {
		if seen[option.ID] {
			canonical = append(canonical, option.ID)
		}
	}
	if len(canonical) != len(selected) {
		return "", "", ErrSurveyInvalid
	}
	encoded, err := common.Marshal(canonical)
	return string(encoded), strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(notes, "\r\n", "\n"), "\r", "\n")), err
}

func SubmitOnboardingSurvey(userID int, selected []string, notes string) (OnboardingSurveyView, error) {
	answer, notes, err := canonicalSurveyAnswer(selected, notes)
	if err != nil {
		return OnboardingSurveyView{}, err
	}
	// The first database operation is an atomic conditional write. This avoids
	// lost updates on every engine and SQLite deferred read-to-write upgrades.
	err = surveyDB(DB).Model(&UserOnboardingSurvey{}).
		Where("user_id = ? AND survey_id = ? AND status IN ?", userID, OnboardingSurveyID, []string{SurveyPending, SurveyDeferred}).
		Updates(map[string]any{"status": SurveySubmitted, "selected_options_json": answer, "notes": notes, "submitted_at": time.Now().Unix()}).Error
	if err != nil {
		return OnboardingSurveyView{}, err
	}
	row, err := loadOnboardingSurvey(userID)
	if err != nil {
		return OnboardingSurveyView{}, err
	}
	if row == nil {
		return OnboardingSurveyView{}, ErrSurveyNotEligible
	}
	if row.Status != SurveySubmitted || row.SelectedOptionsJSON != answer || row.Notes != notes {
		return OnboardingSurveyView{}, ErrSurveyAlreadySubmitted
	}
	return row.view()
}

func DeferOnboardingSurvey(userID int) (OnboardingSurveyView, error) {
	if err := surveyDB(DB).Model(&UserOnboardingSurvey{}).
		Where("user_id = ? AND survey_id = ? AND status = ?", userID, OnboardingSurveyID, SurveyPending).
		Updates(map[string]any{"status": SurveyDeferred, "deferred_at": time.Now().Unix()}).Error; err != nil {
		return OnboardingSurveyView{}, err
	}
	row, err := loadOnboardingSurvey(userID)
	if err != nil {
		return OnboardingSurveyView{}, err
	}
	if row == nil {
		return OnboardingSurveyView{}, ErrSurveyNotEligible
	}
	return row.view()
}

type OnboardingSurveyFilter struct{ Phone, Status, OptionID string }

type OnboardingSurveyRegistration struct {
	UserID            int      `json:"user_id"`
	PhoneE164         string   `json:"phone_e164"`
	Status            string   `json:"status"`
	SelectedOptionIDs []string `json:"selected_option_ids"`
	Notes             string   `json:"notes"`
	RegisteredAt      int64    `json:"registered_at"`
	SubmittedAt       int64    `json:"submitted_at"`
	DeferredAt        int64    `json:"deferred_at"`
}

func ListOnboardingSurveys(filter OnboardingSurveyFilter, offset, limit int) ([]OnboardingSurveyRegistration, int64, error) {
	query := surveyDB(DB).Model(&UserOnboardingSurvey{}).Where("survey_id = ?", OnboardingSurveyID)
	if filter.Phone != "" {
		query = query.Where("phone_e164 LIKE ?", "%"+filter.Phone+"%")
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.OptionID != "" {
		query = query.Where("selected_options_json LIKE ?", "%\""+filter.OptionID+"\"%")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []UserOnboardingSurvey
	if err := query.Order("registered_at DESC").Order("user_id DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	items := make([]OnboardingSurveyRegistration, 0, len(rows))
	for i := range rows {
		view, err := rows[i].view()
		if err != nil {
			return nil, 0, err
		}
		items = append(items, OnboardingSurveyRegistration{
			UserID: rows[i].UserID, PhoneE164: rows[i].PhoneE164, Status: view.Status,
			SelectedOptionIDs: view.SelectedOptionIDs, Notes: view.Notes,
			RegisteredAt: view.RegisteredAt, SubmittedAt: view.SubmittedAt, DeferredAt: view.DeferredAt,
		})
	}
	return items, total, nil
}

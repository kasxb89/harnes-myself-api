package common

import (
	"os"
	"strings"
)

// Phone sign-in is an optional, self-contained alternative to password login:
// the phone number itself is the account identity, so no external account
// service is required. Real SMS delivery is delegated to an HTTP hook; for
// local development a fixed-code bypass can be enabled instead.
var (
	// PhoneLoginEnabled turns the phone endpoints on.
	PhoneLoginEnabled = GetEnvOrDefaultBool("PHONE_LOGIN_ENABLED", false)
	// PhoneSmsDevBypass skips real delivery and accepts PhoneSmsDevCode for
	// numbers listed in PhoneSmsDevAllowedPhones.
	PhoneSmsDevBypass = GetEnvOrDefaultBool("PHONE_SMS_DEV_BYPASS", false)
	// PhoneSmsDevCode is the fixed code used while the bypass is on.
	PhoneSmsDevCode = strings.TrimSpace(os.Getenv("PHONE_SMS_DEV_CODE"))
	// PhoneSmsDevAllowedPhones is a comma-separated E.164 allowlist for the
	// bypass. The bypass refuses every number when this list is empty.
	PhoneSmsDevAllowedPhones = strings.TrimSpace(os.Getenv("PHONE_SMS_DEV_ALLOWED_PHONES"))
	// PhoneSmsWebhookUrl receives {phone, code} as JSON when the bypass is off.
	PhoneSmsWebhookUrl = strings.TrimSpace(os.Getenv("PHONE_SMS_WEBHOOK_URL"))
	// PhoneSmsWebhookToken is sent as a bearer token to the webhook, if set.
	PhoneSmsWebhookToken = strings.TrimSpace(os.Getenv("PHONE_SMS_WEBHOOK_TOKEN"))
	// PhoneLoginDefaultGroup is the group new phone accounts are created in.
	PhoneLoginDefaultGroup = GetEnvOrDefaultString("PHONE_LOGIN_DEFAULT_GROUP", "default")
	// PhoneRegistrationBonusYuan is the one-off credit granted to a phone
	// account created by its first successful verification. Production grants
	// 10000 points, i.e. 10 yuan at the fixed 1000 points = 1 yuan rate.
	PhoneRegistrationBonusYuan = GetEnvOrDefault("PHONE_REGISTRATION_BONUS_YUAN", 10)
)

const (
	// PhoneLoginPurpose namespaces the verification codes for phone sign-in.
	PhoneLoginPurpose = "pn"
)

// PhoneSmsBypassAllows reports whether the given E.164 number may use the
// development bypass.
func PhoneSmsBypassAllows(phoneE164 string) bool {
	if !PhoneSmsDevBypass || PhoneSmsDevCode == "" {
		return false
	}
	for _, allowed := range strings.Split(PhoneSmsDevAllowedPhones, ",") {
		if strings.TrimSpace(allowed) == phoneE164 {
			return true
		}
	}
	return false
}
